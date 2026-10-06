# Private pothole recognition — experimental local CNN

Road reports can include **experimental pothole recognition** for each private
photo. It is off by default. Select the checkbox, choose **Analyze text and
potholes**, and review candidate boxes and their list. Camera photos and manually
reviewed video frames use the same private image pipeline. No original video is
sent for inference. Nothing changes the resident's description, category, road
type, urgency, or contact guidance automatically.

The separate `POTHOLE_DETECTION` task runs a real Faster R-CNN ResNet-50/FPN
checkpoint on local CPU, independently of QUALITY, OCR and the COCO object task.
Results are candidates, not verified damage. Scores select boxes internally;
`confidence` remains null. The adapter does not infer depth, dimensions in metres,
severity, road ownership, an officer, contractor, inspection outcome or safety.

## Provision and upgrade

```bash
# Docker builds the export in a separate stage; PyTorch is not in the worker.
docker compose build media-worker api web
docker compose up -d --no-deps media-worker
docker compose logs --tail=5 media-worker
make migrate
docker compose up -d --no-deps --wait api web
```

Upgrade the worker before enabling the new API capability. Check its pinned
pothole model in the readiness log; older workers do not support the new kind.
Migration 26 expands only the analysis task constraint. No records, roles, keys,
volumes, upload parts or completed jobs are reset. Downgrade fails if new task
records exist instead of deleting them. Use the existing volume-preserving
[database upgrade procedure](DATABASE_VAULT_ISOLATION.md).

For host development, Python 3.12 and venv support are required:

```bash
PYTHON=python3.12 make pothole-setup
export JANSETU_POTHOLE_BINARY="$(pwd)/services/vision/pothole-run"
export JANSETU_VISION_BINARY="$(pwd)/services/vision/run"
make vision-test
make media-worker
```

Export the same executable paths before starting the API. Leave
`JANSETU_POTHOLE_BINARY` unset in both processes to disable the adapter: its
capability becomes `PLANNED`, requested tasks are `UNSUPPORTED`, and manual
reporting/OCR remain usable. Enabling a flag advertises configuration, not worker
liveness or evaluated accuracy. A stopped worker leaves tasks queued.

## Artifact and processing contract

[Research and licenses](POTHOLE_MODEL_RESEARCH.md) record the selected publisher,
source commits, alternatives and unresolved training-image provenance.

| Artifact | Pin / behavior |
| --- | --- |
| Checkpoint | [Daniels Stulpe revision42b586f1](https://huggingface.co/DanielsStulpe/pothole-detection/tree/42b586f1ff38944fdd5fcc8fd6112451bbe15fb9), `baseline_faster_rcnn.pth`, 165,729,959 bytes, SHA-256 `c8f5de9a18d1e5980b3b0f1febe653583b19843b7d7ed549d0dc3eb2e2fc79e5` |
| Notice | Publisher declares MIT; [retained notice](../services/vision/licenses/Pothole-MIT.txt). TorchVision/PyTorch BSD notices are also retained; training photos have separate terms, with rights review pending. |
| Export | CPU PyTorch 2.10.0/TorchVision 0.25.0/ONNX 1.20.1, opset 17, fixed `[1,3,640,640]`; no upstream script execution or automatic backbone downloads |
| Reconstruction | Two classes, matching FrozenBatchNorm epsilon 0.0; restricted `weights_only=True` and strict key/shape loading after hash/size verification |
| Build checks | ONNX checker plus real-photo and empty-input Torch/ONNX parity; same tensors, `rtol=1e-4`, coordinate `atol=0.02` pixels |
| Runtime | ONNX Runtime 1.23.2/OpenCV 4.12.0, CPU only, one thread, telemetry off, no network calls or inference credentials |
| Artifact identity | Build manifest records checkpoint, export recipe and ONNX hashes. Runtime verifies all three; modelVersion also records the actual ONNX hash. Export identity can vary across platforms and is not represented as a universal reproducible hash. |

[The runner](../services/vision/pothole.py) receives the internal metadata-stripped
PNG, bounded to 12 million pixels/8192 px per side. It resizes proportionally,
converts BGR to RGB after resizing, pads top-left with 114 and scales pixels to
`[0,1]`; the model applies its own normalization. Native class suppression is 0.5;
candidate selection is fixed 0.5 and at most 100 regions. Rounded resize dimensions
map each axis back to original pixels, with clipped integer quadrilaterals.

The Go adapter uses a 20-second timeout, 32 KiB output limit, strict JSON, exact
dimensions, label allowlist and bounded box geometry. It passes a minimal
environment without database/vault credentials. Invalid output is not retryable.
Failed/time-out detection supports up to three targeted attempts, preserving
completed OCR/object/quality tasks. The existing owner, session, expiry,
authorization-version, source hash, lease and job-version fences apply;
cancellation or revocation discards late results. Results and source photos stay
private and are never copied into the public progress projection.

Compose retains a read-only worker root, no capabilities, bounded temporary
directory and one CPU. Its memory limit is 1 GiB to accommodate the larger CNN:
single-process notice inference measured about 610 MiB peak RSS with ORT's CPU
arena disabled, versus about 865 MiB after two runs with the default arena. These
development measurements are not production capacity or maximum-image guarantees.

## Reproducible smoke observations

```bash
services/vision/.venv/bin/python services/vision/evaluate_potholes.py
```

[Fixture sources/licenses](../tests/fixtures/POTHOLE_SOURCES.md) and
[checksum/box manifest](../tests/fixtures/pothole-smoke.json) reproduce this small
convenience sample. Labels were created by coding-agent visual review, not
independently adjudicated human ground truth; training overlap is unknown.
Loose visible cavity boxes use intersection-over-union 0.3 for smoke matching.
These observations do not establish accuracy, precision/recall, calibration,
Indian-road performance, night performance or field readiness.

| Photo | Candidates | Matched visible cavities | Extra candidates | Observation |
| --- | ---: | ---: | ---: | --- |
| Monaghan |1|1|0|Dry asphalt cavity |
| Pothole Big |1|1|0|Low-resolution asphalt cavity |
| Villeray |2|1|1|Cavity found; glove above it falsely identified |
| Asphalt texture |0|0|0|No visible cavity in crop |
| Shiashie |0|0|0|No visible cavity in shown road |
| Sunset road |0|0|0|No visible cavity; darkness limits assessment |

ARM64 development inference took roughly 5.5 seconds per image in a reused
session under concurrent test/build load, excluding process startup. Real CLI
and Docker readiness also exercise startup. These are observations from one
machine, not throughput promises. An exploratory unpaved puddle photo also
produced a candidate, illustrating why absence or presence of boxes cannot
establish damage or safety. The UI keeps these limitations visible.

CI exercises actual weights, private ownership/disabled states, failed-task retry
preservation, unsupported OCR and late-cancellation fencing. Browser checks cover
explicit opt-in, real positive/empty regions, manual report preservation, private
access, mobile themes, category changes and capability/failure fallback.

Independent human-labeled, verified held-out Indian field evaluation, rights
review, probability calibration, adverse weather/night/occlusion coverage, load
and recovery testing, verified asset ownership and official/contractor matching
remain production gates. Automatic continuous video/drive analysis is pending.
