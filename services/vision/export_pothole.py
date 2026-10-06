"""Build a fixed ONNX artifact from verified tensor weights, without upstream code.

This build-only environment is never shipped to the private media worker.
No URL, constructor, pickle allowlist, or model option comes from a caller.
"""
import hashlib
import json
from pathlib import Path
import tempfile
import urllib.request

import onnx
import numpy as np
import onnxruntime as ort
from PIL import Image
import torch
import torchvision
from torchvision.models.detection import fasterrcnn_resnet50_fpn
from torchvision.ops.misc import FrozenBatchNorm2d

SOURCE_SHA256 = "c8f5de9a18d1e5980b3b0f1febe653583b19843b7d7ed549d0dc3eb2e2fc79e5"
URL = "https://huggingface.co/DanielsStulpe/pothole-detection/resolve/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9/baseline_faster_rcnn.pth"
ROOT = Path(__file__).resolve().parent


def frozen_backbone(module):
    # Match the COCO_V1 constructor used in training, without downloading COCO
    # or ImageNet weights. Regular BatchNorm defaults would change inference.
    for name, child in list(module.named_children()):
        if isinstance(child, torch.nn.BatchNorm2d):
            setattr(module, name, FrozenBatchNorm2d(child.num_features, eps=0.0))
        else:
            frozen_backbone(child)


class ExportAdapter(torch.nn.Module):
    def __init__(self, model):
        super().__init__()
        self.model = model

    def forward(self, pixels):
        result = self.model([pixels[0]])[0]
        return result["boxes"], result["scores"], result["labels"]


def main():
    if (torch.__version__ != "2.10.0+cpu" or torchvision.__version__ != "0.25.0+cpu"
            or onnx.__version__ != "1.20.1"):
        raise RuntimeError("Pinned CPU export toolchain required")
    torch.set_num_threads(1)
    torch.set_num_interop_threads(1)
    recipe_hash = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    target = ROOT / "pothole.onnx"
    manifest_path = ROOT / "pothole-manifest.json"
    if target.exists() and manifest_path.exists():
        manifest = json.loads(manifest_path.read_text())
        if (manifest.get("sourceSha256") == SOURCE_SHA256
                and manifest.get("recipeSha256") == recipe_hash
                and manifest.get("onnxSha256") == hashlib.sha256(target.read_bytes()).hexdigest()):
            return
    with tempfile.TemporaryDirectory() as directory:
        checkpoint = Path(directory) / "weights.pth"
        digest, size = hashlib.sha256(), 0
        with urllib.request.urlopen(URL, timeout=60) as response, checkpoint.open("wb") as output:
            while chunk := response.read(1024 * 1024):
                size += len(chunk)
                if size > 165729959:
                    raise RuntimeError("Checkpoint size")
                digest.update(chunk)
                output.write(chunk)
        if size != 165729959 or digest.hexdigest() != SOURCE_SHA256:
            raise RuntimeError("Checkpoint integrity")
        model = fasterrcnn_resnet50_fpn(weights=None, weights_backbone=None,
                                       num_classes=2, min_size=640, max_size=640)
        frozen_backbone(model.backbone)
        model.load_state_dict(torch.load(checkpoint, map_location="cpu", weights_only=True), strict=True)
        model.eval()
        adapter = ExportAdapter(model).eval()
        temporary = ROOT / "pothole.tmp"
        # Fixed shape; native NMS/ROIAlign have supported ONNX operators. The
        # legacy exporter is deliberate for this torchvision detection model.
        with torch.inference_mode():
            torch.onnx.export(adapter, torch.zeros(1, 3, 640, 640),
                              temporary, opset_version=17, dynamo=False,
                              input_names=["pixels"], output_names=["boxes", "scores", "labels"])
        onnx.checker.check_model(str(temporary))
        # Compare real pixels and empty outputs before publishing the artifact.
        # Both engines receive the identical fixed-size tensor, so a resize
        # library difference cannot conceal export drift.
        fixture = ROOT.parents[1] / "tests/fixtures/pothole-positive.png"
        if hashlib.sha256(fixture.read_bytes()).hexdigest() != "de737604d4b590b86dffb67b1609845381994846e17058f674fd7362eec4cacc":
            raise RuntimeError("Export fixture integrity")
        photo = Image.open(fixture).convert("RGB")
        ratio = min(640 / photo.width, 640 / photo.height)
        resized = photo.resize((max(1, int(photo.width*ratio)), max(1, int(photo.height*ratio))))
        padded = np.full((640, 640, 3), 114, np.uint8)
        padded[:resized.height, :resized.width] = np.asarray(resized)
        pixels = np.ascontiguousarray(padded.transpose(2, 0, 1), np.float32)[None] / 255
        options = ort.SessionOptions()
        options.intra_op_num_threads = options.inter_op_num_threads = 1
        options.enable_cpu_mem_arena = options.enable_mem_pattern = False
        ort.disable_telemetry_events()
        engine = ort.InferenceSession(str(temporary), sess_options=options, providers=["CPUExecutionProvider"])
        for array in (pixels, np.zeros((1, 3, 640, 640), np.float32)):
            with torch.inference_mode():
                expected = adapter(torch.from_numpy(array))
            actual = engine.run(None, {"pixels": array})
            np.testing.assert_allclose(expected[0].numpy(), actual[0], rtol=1e-4, atol=0.02)
            np.testing.assert_allclose(expected[1].numpy(), actual[1], rtol=1e-4, atol=1e-6)
            np.testing.assert_array_equal(expected[2].numpy(), actual[2])
        print("Fixed photo and empty-input Torch/ONNX parity passed")
        manifest = {"sourceSha256": SOURCE_SHA256, "recipeSha256": recipe_hash,
                    "onnxSha256": hashlib.sha256(temporary.read_bytes()).hexdigest()}
        temporary.replace(target)
        temp_manifest = ROOT / "pothole-manifest.tmp"
        temp_manifest.write_text(json.dumps(manifest, sort_keys=True) + "\n")
        temp_manifest.replace(manifest_path)


if __name__ == "__main__":
    main()
