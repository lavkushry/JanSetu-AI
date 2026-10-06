from pathlib import Path
import os
import unittest

import numpy as np
from pothole import candidates, detect, session

ROOT = Path(__file__).resolve().parents[2]


class PotholeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not Path(__file__).with_name("pothole.onnx").exists() and not os.environ.get("JANSETU_POTHOLE_BINARY"):
            raise unittest.SkipTest("make pothole-setup for the optional detector")
        cls.engine, cls.version = session()

    def test_actual_photo_and_negative_scene(self):
        photo = detect(self.engine, str(ROOT / "tests/fixtures/pothole-positive.png"))
        self.assertEqual((photo["width"], photo["height"]), (583, 1200))
        self.assertFalse(photo["empty"])
        # Loose independently reviewed visible depression, not model coordinates.
        ground = [25, 325, 545, 755]
        overlap = []
        for region in photo["regions"]:
            self.assertEqual(region["label"], "pothole")
            self.assertNotIn("confidence", region)
            a, b = region["polygon"][0], region["polygon"][2]
            intersection = max(0, min(b[0], ground[2]) - max(a[0], ground[0])) * max(0, min(b[1], ground[3]) - max(a[1], ground[1]))
            union = (b[0]-a[0])*(b[1]-a[1]) + (ground[2]-ground[0])*(ground[3]-ground[1]) - intersection
            overlap.append(intersection / union)
        self.assertGreater(max(overlap), 0.3)
        for fixture in ("road-texture.png", "road-plain.png"):
            self.assertTrue(detect(self.engine, str(ROOT / "tests/fixtures" / fixture))["empty"])
        # Text remains data; no instruction from a photographed notice executes.
        self.assertTrue(detect(self.engine, str(ROOT / "services/backend/internal/media/testdata/instructions.png"))["empty"])

    def test_invalid_predictions_and_original_pixel_coordinates(self):
        outputs = [np.array([[16, 32, 64, 128]], np.float32), np.array([0.8]), np.array([1])]
        regions = candidates(outputs, 1000, 500, 640, 320)
        self.assertEqual(regions[0]["polygon"], [[25,50],[100,50],[100,200],[25,200]])
        bad_outputs = [outputs[:2], [np.zeros((1, 5)), outputs[1], outputs[2]],
                       [outputs[0], np.array([np.nan]), outputs[2]],
                       [outputs[0], np.array([1.1]), outputs[2]],
                       [outputs[0], outputs[1], np.array([2])]]
        for invalid in bad_outputs:
            with self.assertRaises(ValueError):
                candidates(invalid, 1000, 500, 640, 320)
        with self.assertRaises(ValueError):
            detect(self.engine, str(ROOT / "services/vision/requirements.txt"))
