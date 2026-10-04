from pathlib import Path
import unittest

import numpy as np
from detect import candidates, detect, session

ROOT = Path(__file__).resolve().parents[2]


class DetectionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.engine = session()

    def test_real_photo_and_no_text_instruction_execution(self):
        photo = detect(self.engine, str(ROOT / "tests/fixtures/vision-people.png"))
        self.assertEqual((photo["width"], photo["height"]), (640, 480))
        self.assertFalse(photo["empty"])
        self.assertGreaterEqual(sum(r["label"] == "person" for r in photo["regions"]), 1)
        for fixture in ("notice.png", "instructions.png"):
            notice = detect(self.engine, str(ROOT / "services/backend/internal/media/testdata" / fixture))
            self.assertTrue(notice["empty"])
            self.assertEqual(notice["regions"], [])

    def test_decoded_png_required(self):
        with self.assertRaises(ValueError):
            detect(self.engine, str(ROOT / "services/vision/requirements.txt"))

    def test_layout_and_nan_rejected(self):
        for invalid in (np.zeros((1, 1, 85)), np.full((1, 3549, 85), np.nan)):
            with self.assertRaises(ValueError):
                candidates(invalid, 640, 480, 416, 312)

    def test_letterbox_clipping_nms_and_supported_labels(self):
        values = np.zeros((1, 3549, 85), np.float32)
        # Two identical person boxes from adjacent stride-8 cells, plus a dog
        # excluded by the deliberately bounded road-object vocabulary.
        values[0, 0, :4] = [10, 10, np.log(10), np.log(10)]
        values[0, 1, :4] = [9, 10, np.log(10), np.log(10)]
        values[0, :2, 4] = 0.9
        values[0, :2, 5] = 0.9
        values[0, 2, 4] = 0.9
        values[0, 2, 5 + 16] = 0.9
        result = candidates(values, 1000, 500, 416, 208)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["label"], "person")
        # Each axis uses the actual resized dimensions, with floor/ceil bounds.
        self.assertEqual(result[0]["polygon"], [[96, 96], [289, 96], [289, 289], [96, 289]])


if __name__ == "__main__":
    unittest.main()
