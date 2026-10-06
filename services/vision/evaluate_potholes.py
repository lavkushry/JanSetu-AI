"""Reproduce the fixed public-fixture smoke observations, never resident data."""
import hashlib
import json
from pathlib import Path
import time

from pothole import detect, session

ROOT = Path(__file__).resolve().parents[2] / "tests/fixtures"


def iou(first, second):
    intersection = max(0, min(first[2], second[2]) - max(first[0], second[0])) * max(0, min(first[3], second[3]) - max(first[1], second[1]))
    union = (first[2]-first[0])*(first[3]-first[1]) + (second[2]-second[0])*(second[3]-second[1]) - intersection
    return intersection / union if union else 0


def main():
    fixtures = json.loads((ROOT / "pothole-smoke.json").read_text())
    engine, version = session()
    observations = []
    for case in fixtures["cases"]:
        path = ROOT / case["file"]
        if path.parent != ROOT or hashlib.sha256(path.read_bytes()).hexdigest() != case["sha256"]:
            raise ValueError("Fixture integrity")
        started = time.monotonic()
        result = detect(engine, str(path))
        unmatched = list(case["boxes"])
        extra = 0
        for region in result["regions"]:
            a, b = region["polygon"][0], region["polygon"][2]
            box = a + b
            matches = [iou(box, target) for target in unmatched]
            if matches and max(matches) >= fixtures["iouThreshold"]:
                unmatched.pop(matches.index(max(matches)))
            else:
                extra += 1
        observations.append({"file": case["file"], "candidates": len(result["regions"]),
                             "matchedVisibleBoxes": len(case["boxes"])-len(unmatched),
                             "missedVisibleBoxes": len(unmatched), "unmatchedCandidates": extra,
                             "seconds": round(time.monotonic()-started, 3)})
    print(json.dumps({"modelVersion": version, "scope": fixtures["scope"], "observations": observations}, indent=2))


if __name__ == "__main__":
    main()
