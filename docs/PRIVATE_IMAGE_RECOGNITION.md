# Private image recognition — experimental local adapter

Residents can opt into **Include experimental object recognition** when analyzing a private report photo. **Analyze text and objects** creates independent QUALITY, OCR and ISSUE_DETECTION tasks. Review shows candidate regions on the decoded image and a readable list. Hiding boxes leaves the list available. Nothing is inserted into the statement or category, routed to an agency, verified or published automatically.

This adapter recognizes possible objects, not civic hazards. Its bounded vocabulary is person, bicycle, car, motorcycle, bus, truck, traffic light, fire hydrant, stop sign and bench. It cannot recognize potholes, leaks, waste, broken assets, severity or whether a scene is safe. Empty results mean no supported candidates passed the internal threshold. The separate [experimental pothole adapter](PRIVATE_POTHOLE_RECOGNITION.md) handles pothole candidates; broader issue/category detection and field evaluation remain pending.

## Run and inspect

```bash
docker compose up --build -d
docker compose logs --tail=10 media-worker
```

Compose includes the fixed model and CPU runtime in the media worker. No GPU, cloud account, external inference service or runtime model download is needed. API readiness checks its database, not worker liveness. The worker checks model integrity, runtime versions, input layout and an actual inference at startup, exiting if the configured adapter is unavailable. Jobs can remain queued while the worker is stopped; manual reporting remains usable.

For host development, use Python 3.12 with venv support:

```bash
make vision-setup
export JANSETU_VISION_BINARY="$(pwd)/services/vision/run"
make vision-test
make media-worker
```

Export the same absolute executable path before starting the API. If the host's Python differs, create `services/vision/.venv` with Python 3.12, install [requirements.txt](../services/vision/requirements.txt), then run [download_model.py](../services/vision/download_model.py) with that environment. Models and environments are ignored by Git. Leaving `JANSETU_VISION_BINARY` unset keeps ISSUE_DETECTION `UNSUPPORTED`; OCR remains independent. Existing OCR-only jobs remain OCR-only.

Sign in with a fictional account, open **Report an issue**, attach a photo, select the recognition checkbox and analyze. Review observations yourself. Unsupported OCR languages can coexist with successful object recognition; the job becomes `PARTIAL`. Submit your own description without waiting for analysis. Photos/results retain the [private media access and retention boundary](PRIVATE_MEDIA_OCR.md).

## Inference and validation

The [Go detector adapter](../services/backend/internal/media/detection.go) invokes a fixed local executable with one internally selected decoded PNG path. Its child receives a minimal environment without database/vault credentials. Requests cannot choose models, executable options or external image URLs. Photographed instructions are data. The Python runner uses CPU inference, one ONNX/OpenCV thread and disabled telemetry. Compose retains the worker's read-only root, dropped capabilities, 1 GiB memory limit (shared with the optional pothole adapter), one CPU and bounded temporary directory.

YOLOX's published ONNX interface uses raw BGR pixels, top-left letterboxing and grid/stride decoding. JanSetu maps each axis back with actual rounded resize dimensions, uses fixed 0.45 score and class-aware suppression thresholds, clips boxes and returns at most 100 regions. Unsupported labels are discarded. Selection scores are not calibrated probabilities. [Official inference example](https://github.com/Megvii-BaseDetection/YOLOX/blob/6ddff4824372906469a7fae2dc3206c7aa4bbaee/demo/ONNXRuntime/onnx_inference.py), [preprocessing](https://github.com/Megvii-BaseDetection/YOLOX/blob/6ddff4824372906469a7fae2dc3206c7aa4bbaee/yolox/data/data_augment.py).

Go limits inference to 20 seconds and output to 32 KiB. It rejects unknown fields/labels, mismatched dimensions, missing/inconsistent empty states, extra JSON documents, noninteger coordinates, malformed quadrilaterals, out-of-bounds/zero-area boxes and over 100 regions. Source hash/model metadata come from the job and worker, not provider output. Detection regions contain `id,label,polygon,confidence:null`; OCR retains `id,text,languageTag,polygon,confidence`. No face/identity/plate recognition is enabled.

[OpenAPI v0.4.0](../contracts/openapi/core.yaml) models the region union. Existing source hash, authorization version, session/permission, expiry, lease and job-version fences apply. Failed detection preserves completed OCR/quality. `VISION_FAILED` and `VISION_TIMEOUT` permit targeted retry up to three attempts; invalid results do not. Cancellation or lost authority discards late output. Object candidates remain private analysis, separate from OCR corrections and inspection findings.

## Artifact and license inventory

| Artifact               | Pinned provenance                                                                                                                                                                                                   | Declaration / boundary                                                                                                                                                                                                                                  |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| YOLOX-Nano ONNX        | [Official release 0.1.1rc0](https://github.com/Megvii-BaseDetection/YOLOX/releases/tag/0.1.1rc0), 3,659,407 bytes; SHA-256 `c789161ed43c8269fcd4e67c67eeeb4e80c622da2eb296a20bc6007bd18a0b7d`                       | Upstream project declares Apache-2.0; [license copy](../services/vision/licenses/YOLOX-Apache-2.0.txt). Release assets have no separate model card proving JanSetu accuracy/training-image rights. Production distribution/data review remains pending. |
| ONNX Runtime           | Python CPU `1.23.2`                                                                                                                                                                                                 | [MIT license](https://github.com/microsoft/onnxruntime/blob/v1.23.2/LICENSE); [official ARM64/x64 CPU support](https://onnxruntime.ai/docs/get-started/with-python.html).                                                                               |
| OpenCV                 | `opencv-python-headless 4.12.0.88`                                                                                                                                                                                  | [Apache-2.0](https://github.com/opencv/opencv/blob/4.12.0/LICENSE); installed wheel retains third-party notices.                                                                                                                                        |
| NumPy and dependencies | Complete version pins in requirements.txt; NumPy `2.2.6`                                                                                                                                                            | [NumPy license](https://github.com/numpy/numpy/blob/v2.2.6/LICENSE.txt); package notices remain installed. Platform wheel hashes and complete production SBOM pending.                                                                                  |
| Positive fixture       | [OpenCV basketball1.png](https://github.com/opencv/opencv/blob/cbee6841638edb6fbc8110df7cd52bb8e3d66211/samples/data/basketball1.png), copied unchanged as [vision-people.png](../tests/fixtures/vision-people.png) | Upstream sample, [license copy](../tests/fixtures/OpenCV-Apache-2.0.txt). SHA-256 `ba06f6701f7260998b430c39b6557f775497e6ce7b1a74f0b7ea6af371bf54a6`. Test fixture, not resident data.                                                                  |
| Negative fixtures      | Original fictional notice/instructions fixtures from the OCR milestone                                                                                                                                              | No source copied from the five unlicensed reference projects.                                                                                                                                                                                           |

The model version includes its release, weight hash, runtime versions and `road-objects-v1` processing revision. Setup/build downloads from the fixed official release URL and fails on hash/size mismatch. Runtime checks the local hash again. Python and base-image patch versions are not immutable digest pins; production image reproducibility remains pending.

## Verification and remaining gates

```bash
make vision-test
JANSETU_VISION_BINARY="$(pwd)/services/vision/run" make check
JANSETU_VISION_BINARY="$(pwd)/services/vision/run" make test-integration
npm run test:e2e
```

Python checks actual inference on the positive image and both text-only negatives, non-PNG rejection, invalid/nonfinite predictions, suppression and resize coordinates. The photo produces person candidates; negatives produce explicit empty results. These three images are a technical smoke corpus, not a recall/precision or safety benchmark.

Go checks strict output validation, actual pinned inference, deadlines, OCR preservation after detector failure/targeted retry, successful objects alongside unsupported Hindi OCR, foreign-user denial, private submission and cancellation during actual inference. Browser checks opt-in, real overlay/list, box visibility, unchanged statement/category, partial language state, private access and report submission at 320px. CI requires the pinned adapter on x64; development also exercises ARM64.

Capabilities remain `EVALUATING`. Civic-hazard taxonomy/models, licensed labeled pilot data, condition/language evaluation, confidence calibration, orientation, redaction, public derivative review, full artifact inventory, worker-health/queue-age monitoring and load/recovery proof remain pending. The [canonical suite](spec/README.md) remains the target; this local object preview does not complete FR-28 or the B-13 gate.
