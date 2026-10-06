# Private road surface reporting

Residents can choose **Potholes & road surface**, record a road name, observed pothole or broken surface, the road type they believe applies, direction/lane, landmark and their own description. The report remains private. Coordinator assessment and a separate agency acceptance still determine operational responsibility. This local demonstration uses fictional accounts and City Works mandates.

## Evidence and review

**Take photo** requests the rear camera on supporting mobile browsers; a browser may offer its file picker instead. Existing JPEG/PNG/WebP uploads, private derivatives, OCR review, resumable recovery and four-photo limits apply. Capture while parked.

**Select frame from video** opens recorded MP4/WebM/MOV footage in the browser. It accepts clips up to 100 MiB, ten minutes, 12 megapixels per frame and 8192 pixels per side; codec support depends on the browser. Playback is muted, does not start automatically and pauses when the tab is hidden. Pause or seek, select **Review this frame**, then inspect the still before **Attach reviewed frame**. The JPEG is bounded to 1600 pixels on its longest edge. Only this explicit selection uses the existing private uploader. Report review waits for active upload allocation/transfers as well as approved photo processing, including delayed allocation responses. The video is never uploaded, saved in a draft or sent to an AI service. Closing/replacing the video, changing category, leaving the step or closing the wizard releases browser object URLs.

Invalid video, failed contact guidance, unavailable OCR and unsupported recognition do not prevent a text report. Existing media recovery handles an interrupted selected-frame upload; after reload its original file must be reselected if parts remain missing. Choose **Take photo** or a saved still when video decoding is unavailable.

Original clips and their timestamps/GPS are not retained as evidence provenance. A frame is a resident-selected photograph, not an authenticated recording or a measured defect. Continuous drive capture and automatic frame screening remain pending.

## Contact guidance and provenance

`GET /api/road-guidance?roadType=...` provides a curated, versioned snapshot. `UNKNOWN` is the default. Selecting a type never locates the road or assigns its owner.

| Resident-selected type | Current result |
| --- | --- |
| `BENGALURU_CITY` | General Greater Bengaluru Authority civic entry point, 1533; city corporation/road officer require confirmation. |
| `NHAI_HIGHWAY` | 1033 entry point for NHAI-managed tolled highway stretches; coverage must be confirmed. |
| `KARNATAKA_PWD` | Contact lookup unavailable; official Karnataka procurement portal link only. |
| `UNKNOWN` | Contact and contractor lookup unavailable. |

Contact sources are the [official GBA website](https://gba.karnataka.gov.in/) and [IHMCL highway helpline page](https://ihmcl.co.in/24x7-national-highways-helpline-1033-page/), reviewed on 6 October 2026. GBA's currently served [application asset](https://gba.karnataka.gov.in/static/js/main.2c80bd9f.js) confirms 1533. These contact entry points do not identify an individual engineer or prove ownership of the resident's road. They are curated data, not live lookup results; review their source/date before using them.

**Contractor status is always `UNAVAILABLE`.** The reference project's snapshot publisher [disclosed incorrect tender-to-contractor matching](https://bengaluru-road-contracts.pages.dev/). JanSetu imports no bidder names from it. The [official KPPP portal](https://kppp.karnataka.gov.in/) is an entry point for further research, not a matching service. Exact official tender/award identifiers, road stretch, asset responsibility and current contract terms must be verified before attribution.

The server derives contact guidance at submission and stores it with the immutable private report metadata. Resident input cannot supply a claimed officer, contractor, source URL or confirmed match. Owner and coordinator views use the saved snapshot; subsequent directory changes cannot rewrite an original report. Contact guidance never enters public receipt DTOs or safe Activity messages.

## Structured complaint and API

OpenAPI version **0.20.0** adds the `ROAD` report/triage category, `RoadDetails`, `RoadGuidance` and `RoadContact`. A `ROAD` submission requires `roadDetails`; other categories reject it. The web wizard retains road fields in explicitly saved private drafts and omits them from submissions after switching to another category.

`RoadDetails` contains `roadName` (3–180 characters), `issueKind` (`POTHOLE` or `BROKEN_SURFACE`), `roadType` (the table above), optional `travelDirection` (100 characters) and optional `observedAt` (nonzero timestamp, at most one minute ahead of the server clock). The UI currently collects the first four fields. Typed observations are not model results and do not select urgency. Existing statement/location bounds, media ownership and lifetime submission deduplication apply to the complete structured input.

Owner-only report DTOs add the landmark and nullable road details/guidance. Older reports return null road fields. The coordinator intake keeps these observations beside the private evidence, then requires the existing assessment reason and explicit agency proposal. The resident can download a private UTF-8 complaint draft from **My reports** containing their statement, road details, landmark, report/receipt time, attachment count and saved contact sources. Photos are excluded from the text file. Downloading does not send a complaint, imply agency acceptance or change publication consent.

## Recognition boundary

Capabilities explicitly advertise `POTHOLE_DETECTION` as `PLANNED`, with an unavailable explanation. It is not an accepted analysis task. The existing opt-in [object candidate adapter](PRIVATE_IMAGE_RECOGNITION.md) remains limited to its COCO objects. It does not identify potholes, assign urgency or route an agency.

The [reference research](POTHOLE_REFERENCE_RESEARCH.md) found hosted vision calls, no verified local CNN weights and conflicting reuse declarations. This milestone uses original JanSetu code and curated official contact facts, with no imported reference code/data. Automatic pothole detection requires separately licensed/provenanced weights, a bounded private adapter and a held-out human-labeled evaluation of positive, negative and abstention behavior. Calibrated confidence, metric size estimation, GPS/boundary and highway ownership lookup, individual officer/verified contractor attribution, live directory refresh, external complaint delivery and continuous drive mode remain pending.

## Upgrade and verification

No migration is required; the schema stays at **25**. Existing immutable JSON intake metadata holds the additive road fields and saved guidance. No report, photo, key, upload quota, runtime role or public-private policy is reset. Build and replace the API and web containers while keeping the media/core workers, vault, identity and volumes. External web/API bindings stay at 3100/8081.

Verification covers bounded/manual observations, unavailable routing and contractor states, forged source/contractor rejection, owner-only metadata, unchanged source snapshots after triage, retry conflicts and operational road-category handoff. Browser coverage exercises real private uploads of a selected synthetic video frame, no clip upload, explicit review, delayed upload allocation, draft recovery/category changes, invalid-video fallback, owner-only complaint downloads and 320px light/dark reflow. The [video fixture](../tests/fixtures/README.md) proves this workflow, not hazard recognition accuracy.
