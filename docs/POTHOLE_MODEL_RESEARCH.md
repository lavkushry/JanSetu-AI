# Local pothole model and fixture research

**Checked:** 6 October 2026. **Decision:** Daniels Stulpe's published Faster R-CNN checkpoint is a practical candidate for an **experimental CPU-local adapter**. It is a real trained object detector with downloadable weights and inspectable training code. This research does not establish production accuracy, calibrated confidence, real-time drive performance, Indian-road coverage, or physical defect dimensions.

Only primary source text, metadata, and Commons photo previews were inspected for this note. Upstream scripts were not executed. Runtime, export parity, downloaded-artifact hashes, and fixture results belong in the implementation's separate reproducible smoke report.

## Selected checkpoint and implementation contract

| Item | Verified value |
|---|---|
| Publisher | Daniels Stulpe; [Hugging Face model repository](https://huggingface.co/DanielsStulpe/pothole-detection/tree/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9) |
| Model revision | `42b586f1ff38944fdd5fcc8fd6112451bbe15fb9` |
| Actual filename | `baseline_faster_rcnn.pth`; the model card uses a different filename, so provision from the actual file tree |
| Download | [Immutable checkpoint URL](https://huggingface.co/DanielsStulpe/pothole-detection/resolve/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9/baseline_faster_rcnn.pth) |
| Published size | 165,729,959 bytes |
| Published SHA-256 | `c8f5de9a18d1e5980b3b0f1febe653583b19843b7d7ed549d0dc3eb2e2fc79e5` |
| Upload | 17 May 2026, 17:59:15 UTC, file commit `90b753b11851b5b89413427f461d5e1bf6aec5fe` |

Size, digest, and upload time come from the publisher's [immutable file metadata](https://huggingface.co/api/models/DanielsStulpe/pothole-detection/tree/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9?recursive=true&expand=true). A followed HEAD request reached the artifact successfully. Verify the digest after downloading; metadata and security scans alone are not artifact validation.

The [pinned training source](https://github.com/DanielsStulpe/pothole-detection/blob/bd5409faebf18f7c6b559e3eea76c95b9a8abfdf/model_training/train_faster_rcnn.py) builds TorchVision **Faster R-CNN with ResNet-50/FPN**, initializes from COCO `DEFAULT`, and replaces its predictor with two classes: background plus pothole. It saves a `state_dict`, rather than a custom serialized model. The source and artifact naming are not identical; an independent training run or byte-for-byte reproduction was not verified.

The [dataset loader](https://github.com/DanielsStulpe/pothole-detection/blob/bd5409faebf18f7c6b559e3eea76c95b9a8abfdf/model_training/data.py) uses image tensors scaled to `[0,1]`. TorchVision expects RGB `CHW` tensors and returns postprocessed `xyxy` pixel boxes, labels, and scores. Its internal transform applies normalization and resizing. Treat scores as model scores, not probabilities of verified road damage. [Official inference contract](https://docs.pytorch.org/vision/0.19/models/generated/torchvision.models.detection.fasterrcnn_resnet50_fpn.html).

For offline reconstruction, **both** `weights=None` and `weights_backbone=None` are necessary to suppress automatic downloads. However, that constructor path selects ordinary BatchNorm, whereas the author's pretrained path selects **FrozenBatchNorm2d** and sets its epsilon to **0.0** for COCO V1. Build matching normalization explicitly before strict checkpoint loading. [Versioned constructor and normalization selection](https://github.com/pytorch/vision/blob/v0.19.1/torchvision/models/detection/faster_rcnn.py#L560). Load on CPU with restricted `weights_only=True`, validate expected tensor keys/shapes, and refuse mismatches. [Official serialization guidance](https://docs.pytorch.org/docs/stable/notes/serialization.html#torch-load-with-weights-only-true).

TorchVision documents ONNX export for **fixed batch and fixed image sizes**. A JanSetu 640-pixel export is a new serving configuration, not an upstream distributed artifact: preserve its preprocessing/coordinate mapping, record its digest and export versions, and compare Torch/ONNX outputs on real photos and empty detections. Measure CPU latency before making capture-rate promises. [Official export support](https://docs.pytorch.org/vision/0.19/models/generated/torchvision.models.detection.fasterrcnn_resnet50_fpn.html).

## License and training provenance boundaries

The model repository declares the project **MIT**, including an embedded notice identifying Daniels Stulpe. The linked code has a matching MIT license. Preserve the publisher's notice alongside provisioned or exported weights; an export does not change their provenance. TorchVision source is separately **BSD-3-Clause**. These declarations do not relicense training photos or establish every original photographer's permission. [Pinned model-card license](https://huggingface.co/DanielsStulpe/pothole-detection/raw/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9/README.md), [pinned code license](https://github.com/DanielsStulpe/pothole-detection/blob/bd5409faebf18f7c6b559e3eea76c95b9a8abfdf/LICENSE), [TorchVision license](https://github.com/pytorch/vision/blob/v0.19.1/LICENSE).

The author's training dataset is **Bak / `pothole-idtb2`**, publicly declared **CC BY 4.0**: 1,020 images, 2,736 annotated potholes, and a stated 70/15/15 split. It combines 665 Chitholian images with filtered, annotated Sachin Patel images from Kaggle. This is the training lineage, not an independent evaluation dataset. Exact image manifests and checkpoint-specific split hashes were not available in the inspected source tree. [Publisher's dataset and attribution](https://universe.roboflow.com/bak-6b12k/pothole-idtb2), [pinned dataset README](https://github.com/DanielsStulpe/pothole-detection/blob/bd5409faebf18f7c6b559e3eea76c95b9a8abfdf/data/README.md).

Upstream terms remain distinct. Roboflow's original Chitholian collection declares **ODbL 1.0**, and Kaggle lists its database/content licenses separately; the combined CC BY label does not resolve those obligations. Sachin Patel's dataset declares **CC0**, but describes web-scraped images without a per-image original-rights ledger. Do not redistribute either training collection as if JanSetu had verified every image under one license. [Original Chitholian collection](https://public.roboflow.com/object-detection/pothole), [original Kaggle dataset](https://www.kaggle.com/datasets/chitholian/annotated-potholes-dataset), [Patel dataset](https://www.kaggle.com/datasets/sachinpatel21/pothole-image-dataset), [first-party public metadata](https://www.kaggle.com/api/v1/datasets/list?search=pothole-image-dataset).

## Alternatives inspected

| Candidate | Findings and selection boundary |
|---|---|
| [Subhodeep Moitra YOLOv8](https://huggingface.co/subhodeepmoitra/pothole-detection-yolov8/tree/f829c25ade58a613e4a54ccc3c9759fc82b9e292) | Downloadable ONNX and MIT metadata exist, but training/data provenance is absent. `config.json` contains Python expressions instead of valid JSON and contradictory task labels; example preprocessing/filename does not establish a correct decoder. Unsuitable as the default attributable adapter. |
| [Peter Hdd YOLOv8](https://huggingface.co/peterhdd/pothole-detection-yolov8/tree/da7747eea7abb4319a0f55961f31809a16c1b10a) | Published ONNX/PT, YOLOv8s lineage, and Apache-2.0 metadata; [linked code](https://github.com/PeterHdd/pothole-detection-yolo/tree/ab9506301965dc9477b7993005f3b615fe0df74f) also declares Apache-2.0. Its linked [Ryukijano training dataset](https://huggingface.co/datasets/Ryukijano/Pothole-detection-Yolov8) has sparse provenance and an OpenRAIL tag; it is not independent test evidence. |

Ultralytics states that its training code and produced/fine-tuned YOLO weights fall under **AGPL-3.0** by default, with an enterprise alternative. A third-party MIT/Apache tag does not demonstrate permission to remove applicable upstream terms. This is the upstream publisher's licensing position, not an independent legal determination. Selecting the non-YOLO Faster R-CNN branch avoids adding that disputed YOLO lineage to this milestone. [Official Ultralytics licensing](https://www.ultralytics.com/license).

## Independent data candidate: RTK

**Road Traversing Knowledge**, Rateke, von Wangenheim, and Toledo (2023), DOI **10.17632/hssswvmjwf.1**, declares **CC BY 4.0**. Its authors describe 701 densely annotated 352×288 images, split into 561 training/140 validation examples, captured in daylight/good weather in Santa Catarina, Brazil. Twelve semantic classes include road surfaces, potholes, puddles, cracks, and road features. [Author-owned dataset record](https://data.mendeley.com/datasets/hssswvmjwf/1), [authors' related research](https://link.springer.com/article/10.1007/s10514-020-09964-3).

RTK metadata was accessible, but the official individual download and archive storage paths failed to yield a ZIP in this environment. Its **mask class IDs/colors were not verified**; do not guess a pothole mask legend or report evaluation on unavailable masks. RTK is separately published/licensed, but absence from the selected model's training set remains unverified. It would not establish Indian or night coverage even after access is restored.

## Accessible visual smoke samples

Commons primary metadata and downloaded previews were inspected. The following labels are **coding-agent visual-review proposals**, not an independent human-labeled benchmark. Positive means a visible paved-road depression with a recognizable rim; negative means no visible pothole in the shown surface, not a certified safe road. Retain source/licensing metadata and checksum the downloaded originals before using fixtures.

| Suggested label | Original photo and source | Photographer and image license | Review observation |
|---|---|---|---|
| Positive | [Monaghan source](https://commons.wikimedia.org/wiki/File:Pothole_on_local_Road_in_County_Monaghan.jpg) · [original](https://upload.wikimedia.org/wikipedia/commons/3/35/Pothole_on_local_Road_in_County_Monaghan.jpg) | Computerfan0; CC0 1.0 | Dry asphalt depression with exposed aggregate and visible boundary |
| Positive | [Pothole Big source](https://commons.wikimedia.org/wiki/File:Pothole_Big.jpg) · [original](https://upload.wikimedia.org/wikipedia/commons/c/c7/Pothole_Big.jpg) | Uncl3dad; uploader public-domain dedication | Clear cavity and asphalt rim; original is only 400×300 |
| Positive | [Villeray source](https://commons.wikimedia.org/wiki/File:Pothole_in_Villeray,_Montr%C3%A9al.jpg) · [original](https://upload.wikimedia.org/wikipedia/commons/8/86/Pothole_in_Villeray%2C_Montr%C3%A9al.jpg) | Miguel Tremblay; uploader public-domain dedication | Water is present, but cavity walls and broken asphalt rim are visible |
| Negative | [Asphalt texture source](https://commons.wikimedia.org/wiki/File:Asphalt_road_texture.jpg) · [original](https://upload.wikimedia.org/wikipedia/commons/9/9f/Asphalt_road_texture.jpg) | KurtKaiser; CC0 1.0 | Rough asphalt close-up; no visible cavity |
| Negative | [Shiashie source](https://commons.wikimedia.org/wiki/File:Asphalt_road_at_Shiashie.jpg) · [original](https://upload.wikimedia.org/wikipedia/commons/4/4a/Asphalt_road_at_Shiashie.jpg) | Masssly; CC BY-SA 4.0 | Broad paved road with seams/cracks, no visible pothole |

For the CC BY-SA photo, retain attribution, license link, and change notices; modified copies remain subject to the image's share-alike terms. Photo licenses are separate from JanSetu's code license. These public photos must never appear as newly captured resident evidence.

All selected photos predate the checkpoint. **Training overlap is unverified**, especially because one upstream dataset was web-scraped. Select fixtures and labels before inference, record misses and false positives, and do not discard difficult examples to improve a result. This small convenience sample can test model loading, actual image inference, box mapping, and failure handling. It cannot support an accuracy percentage, calibration claim, production readiness, or geographic generalization.
