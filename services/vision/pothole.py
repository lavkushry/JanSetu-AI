"""Private, experimental pothole candidates from a fixed local CNN.

Scores select candidates; they are not calibrated probabilities. No text,
severity, dimensions in metres, road ownership, or routing is inferred.
"""
import hashlib
import json
import math
from pathlib import Path
import sys

import cv2
import numpy as np
import onnxruntime as ort

ROOT = Path(__file__).resolve().parent
SOURCE_SHA256 = "c8f5de9a18d1e5980b3b0f1febe653583b19843b7d7ed549d0dc3eb2e2fc79e5"
VERSION = f"pothole-fasterrcnn/sha256:{SOURCE_SHA256}/torch-2.10.0-tv-0.25.0-onnx-1.20.1/ort-1.23.2/opencv-4.12.0/pothole-v1"
SIZE = 640


def session():
    manifest = json.loads((ROOT / "pothole-manifest.json").read_text())
    if (set(manifest) != {"sourceSha256", "recipeSha256", "onnxSha256"}
            or manifest["sourceSha256"] != SOURCE_SHA256
            or manifest["recipeSha256"] != hashlib.sha256((ROOT / "export_pothole.py").read_bytes()).hexdigest()
            or manifest["onnxSha256"] != hashlib.sha256((ROOT / "pothole.onnx").read_bytes()).hexdigest()):
        raise ValueError("Model integrity")
    if ort.__version__ != "1.23.2" or cv2.__version__ != "4.12.0":
        raise ValueError("Runtime version")
    ort.disable_telemetry_events()
    cv2.setNumThreads(1)
    options = ort.SessionOptions()
    options.intra_op_num_threads = options.inter_op_num_threads = 1
    # Avoid retaining transient proposal buffers; measured peak is substantially
    # smaller than ORT's default arena for this detector.
    options.enable_cpu_mem_arena = options.enable_mem_pattern = False
    options.log_severity_level = 4
    engine = ort.InferenceSession(str(ROOT / "pothole.onnx"), sess_options=options,
                                  providers=["CPUExecutionProvider"])
    if ([(i.name, i.shape, i.type) for i in engine.get_inputs()] != [("pixels", [1, 3, SIZE, SIZE], "tensor(float)")]
            or [o.name for o in engine.get_outputs()] != ["boxes", "scores", "labels"]):
        raise ValueError("Model layout")
    return engine, VERSION + "/onnx-sha256:" + manifest["onnxSha256"]


def candidates(outputs, width, height, resized_width, resized_height):
    if len(outputs) != 3:
        raise ValueError("Prediction layout")
    boxes, scores, labels = outputs
    count = len(scores)
    if (boxes.shape != (count, 4) or scores.shape != (count,) or labels.shape != (count,)
            or count > 100 or not all(np.isfinite(a).all() for a in outputs)
            or np.any((scores < 0) | (scores > 1)) or np.any(labels != 1)
            or np.any(boxes[:, 2:] < boxes[:, :2])):
        raise ValueError("Prediction layout")
    regions = []
    # Torchvision already performs class NMS at 0.5; our fixed selection score
    # is 0.5. At most100 detections leave the graph. Never expose these scores.
    for box, score in zip(boxes, scores):
        if score < 0.5:
            continue
        x1, y1, x2, y2 = box
        left, top = max(0, math.floor(x1 * width / resized_width)), max(0, math.floor(y1 * height / resized_height))
        right, bottom = min(width, math.ceil(x2 * width / resized_width)), min(height, math.ceil(y2 * height / resized_height))
        if left < right and top < bottom:
            regions.append({"label": "pothole", "polygon": [[left, top], [right, top], [right, bottom], [left, bottom]]})
    return regions


def detect(engine, path):
    with open(path, "rb") as source:
        if source.read(8) != b"\x89PNG\r\n\x1a\n":
            raise ValueError("Decoded PNG required")
    pixels = cv2.imread(path, cv2.IMREAD_COLOR)
    if pixels is None:
        raise ValueError("Image unavailable")
    height, width = pixels.shape[:2]
    if width * height > 12_000_000 or max(width, height) > 8192:
        raise ValueError("Image limit")
    ratio = min(SIZE / width, SIZE / height)
    rw, rh = max(1, int(width * ratio)), max(1, int(height * ratio))
    resized = cv2.cvtColor(cv2.resize(pixels, (rw, rh)), cv2.COLOR_BGR2RGB)
    padded = np.full((SIZE, SIZE, 3), 114, np.uint8)
    padded[:rh, :rw] = resized
    tensor = np.ascontiguousarray(padded.transpose(2, 0, 1), np.float32)[None] / 255.0
    regions = candidates(engine.run(None, {"pixels": tensor}), width, height, rw, rh)
    return {"width": width, "height": height, "regions": regions, "empty": not regions}


def main():
    if len(sys.argv) != 2:
        raise ValueError("One internal image argument required")
    engine, version = session()
    if sys.argv[1] == "--version":
        candidates(engine.run(None, {"pixels": np.zeros((1, 3, SIZE, SIZE), np.float32)}), SIZE, SIZE, SIZE, SIZE)
        print(version)
    else:
        print(json.dumps(detect(engine, sys.argv[1]), separators=(",", ":"), allow_nan=False))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("Pothole inference unavailable", file=sys.stderr)
        sys.exit(1)
