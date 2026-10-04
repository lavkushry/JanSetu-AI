"""Private, CPU-only object candidates. No routing, generated text, or network calls.

YOLOX's published ONNX layout uses raw BGR pixels, top-left letterboxing,
and undecoded [center, size, objectness, 80 class scores] predictions.
See docs/PRIVATE_IMAGE_RECOGNITION.md for provenance and limitations.
"""
import hashlib
import json
import math
from pathlib import Path
import sys

import cv2
import numpy as np
import onnxruntime as ort

SHA256 = "c789161ed43c8269fcd4e67c67eeeb4e80c622da2eb296a20bc6007bd18a0b7d"
VERSION = f"yolox-nano-0.1.1rc0/sha256:{SHA256}/ort-1.23.2/opencv-4.12.0/road-objects-v1"
# This deliberately excludes unsupported hazard categories and most COCO classes.
LABELS = {0: "person", 1: "bicycle", 2: "car", 3: "motorcycle", 5: "bus",
          7: "truck", 9: "traffic light", 10: "fire hydrant", 11: "stop sign", 13: "bench"}
SIZE = 416


def session():
    model = Path(__file__).resolve().parent / "yolox_nano.onnx"
    if hashlib.sha256(model.read_bytes()).hexdigest() != SHA256:
        raise ValueError("Model integrity")
    if ort.__version__ != "1.23.2" or cv2.__version__ != "4.12.0":
        raise ValueError("Runtime version")
    ort.disable_telemetry_events()
    cv2.setNumThreads(1)
    options = ort.SessionOptions()
    options.intra_op_num_threads = 1
    options.inter_op_num_threads = 1
    options.log_severity_level = 4
    engine = ort.InferenceSession(str(model), sess_options=options,
                                  providers=["CPUExecutionProvider"])
    inputs, outputs = engine.get_inputs(), engine.get_outputs()
    if len(inputs) != 1 or inputs[0].shape != [1, 3, SIZE, SIZE] or len(outputs) != 1:
        raise ValueError("Model layout")
    return engine


def candidates(predictions, width, height, resized_width, resized_height):
    if predictions.shape != (1, 3549, 85) or not np.isfinite(predictions).all():
        raise ValueError("Prediction layout")
    values = predictions[0].copy()
    grids, strides = [], []
    for stride in (8, 16, 32):
        side = SIZE // stride
        y, x = np.mgrid[:side, :side]
        grids.append(np.stack((x, y), axis=-1).reshape(-1, 2))
        strides.append(np.full((side * side, 1), stride))
    grid, stride = np.concatenate(grids), np.concatenate(strides)
    centers = (values[:, :2] + grid) * stride
    sizes = np.exp(np.clip(values[:, 2:4], -20, 20)) * stride
    boxes = np.concatenate((centers - sizes / 2, centers + sizes / 2), axis=1)
    boxes[:, (0, 2)] *= width / resized_width
    boxes[:, (1, 3)] *= height / resized_height
    classes = values[:, 5:].argmax(axis=1)
    scores = values[:, 4] * values[np.arange(len(values)), classes + 5]
    eligible = np.flatnonzero((scores >= 0.45) & np.isin(classes, list(LABELS)))
    ordered = eligible[np.argsort(-scores[eligible], kind="stable")][:1000]
    kept = []
    for index in ordered:
        box = boxes[index]
        suppress = False
        for previous in kept:
            if classes[index] != classes[previous]:
                continue
            other = boxes[previous]
            overlap = np.maximum(0, np.minimum(box[2:], other[2:]) - np.maximum(box[:2], other[:2]))
            intersection = float(np.prod(overlap))
            union = float(np.prod(box[2:] - box[:2]) + np.prod(other[2:] - other[:2])) - intersection
            if union > 0 and intersection / union > 0.45:
                suppress = True
                break
        if not suppress:
            kept.append(index)
        if len(kept) >= 100:
            break
    regions = []
    for index in kept:
        x1, y1, x2, y2 = boxes[index]
        left, top = max(0, math.floor(x1)), max(0, math.floor(y1))
        right, bottom = min(width, math.ceil(x2)), min(height, math.ceil(y2))
        if left >= right or top >= bottom:
            continue
        regions.append({"label": LABELS[int(classes[index])],
                        "polygon": [[left, top], [right, top], [right, bottom], [left, bottom]]})
    return regions


def detect(engine, path):
    # Input is the internal, metadata-stripped PNG, never the user's original path.
    with open(path, "rb") as source:
        if source.read(8) != b"\x89PNG\r\n\x1a\n":
            raise ValueError("Decoded PNG required")
    image = cv2.imread(path, cv2.IMREAD_COLOR)
    if image is None:
        raise ValueError("Image unavailable")
    height, width = image.shape[:2]
    if width * height > 12_000_000 or max(width, height) > 8192:
        raise ValueError("Image limit")
    ratio = min(SIZE / width, SIZE / height)
    resized_width, resized_height = max(1, int(width * ratio)), max(1, int(height * ratio))
    resized = cv2.resize(image, (resized_width, resized_height), interpolation=cv2.INTER_LINEAR)
    padded = np.full((SIZE, SIZE, 3), 114, dtype=np.uint8)
    padded[:resized_height, :resized_width] = resized
    tensor = np.ascontiguousarray(padded.transpose(2, 0, 1), dtype=np.float32)[None]
    predictions = engine.run(None, {engine.get_inputs()[0].name: tensor})[0]
    regions = candidates(predictions, width, height, resized_width, resized_height)
    return {"width": width, "height": height, "regions": regions, "empty": not regions}


def main():
    if len(sys.argv) != 2:
        raise ValueError("One internal image argument required")
    engine = session()
    if sys.argv[1] == "--version":
        # Readiness includes actual inference; corrupt/incompatible weights fail startup.
        output = engine.run(None, {engine.get_inputs()[0].name: np.zeros((1, 3, SIZE, SIZE), np.float32)})[0]
        candidates(output, SIZE, SIZE, SIZE, SIZE)
        print(VERSION)
    else:
        print(json.dumps(detect(engine, sys.argv[1]), separators=(",", ":"), allow_nan=False))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Do not put private filenames, pixels, text, or provider output into logs.
        print("Vision inference unavailable", file=sys.stderr)
        sys.exit(1)
