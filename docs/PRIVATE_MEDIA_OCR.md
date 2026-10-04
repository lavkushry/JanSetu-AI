# Private report photos and OCR — local implementation

The local reporting flow accepts up to four private JPEG, PNG, or WebP photos. Residents can request experimental English OCR, select and correct words, add reviewed text to their description, and submit photos with a private report. Photo upload, image processing, and OCR failure leave the manual text flow available. Public feeds and reviewed progress cards receive neither the photos nor raw OCR.

This is a working local adapter, not completion of the production media/AI gates in the [canonical backend specification](spec/BACKEND_IMPLEMENTATION.md). The application still requires local/test mode and fictional reports.

## Run and try it

```bash
docker compose up --build -d
docker compose logs --tail=20 media-worker
```

Compose adds the persistent `jansetu-media` volume without replacing `jansetu-data`. Only the API and media worker mount the media volume; the web server, social projection worker, identity provider, and vault do not. The media worker runs as UID 65532, with a read-only root filesystem, dropped capabilities, one CPU, 768 MiB memory, a 64-process limit, and a bounded temporary filesystem. It joins the database network and receives neither vault credentials nor account/social/operations/publication credentials.

Sign in as Ananya, open **Report an issue**, and choose **Add photos**. A supported file displays a private preview after processing. Choose **Read text from photo**, edit or deselect words, and choose **Add reviewed text to description**. Confirm location, category, description, and publication preference yourself. Submitting without requesting OCR is supported. A coordinator can view attached photos in Service intake; other residents, agency officers, publishers without the coordinator grant, and public readers cannot.

Original fictional fixtures are available at [notice.png](../services/backend/internal/media/testdata/notice.png) and [instructions.png](../services/backend/internal/media/testdata/instructions.png). The latter deliberately contains instructions: OCR returns the photographed words as data and cannot execute them, publish anything, change case status, fetch a URL, or select an agency.

For host development, install `tesseract-ocr` and `tesseract-ocr-eng`, then run `make media-worker` alongside the existing services. API and worker must use the same `JANSETU_MEDIA_DIR`, which defaults to `/tmp/jansetu-media`. Environment variables are in [.env.example](../.env.example). Compose pins Tesseract 5.3.0 and the Debian English pack; host versions can differ. Startup verifies the engine and English asset, and records the engine version plus the actual trained-data SHA-256 in task results. Tesseract and its English trained data use Apache 2.0 licensing; redistribution and production model evaluation still require the release inventory described in the specification. See the upstream [engine license](https://github.com/tesseract-ocr/tesseract/blob/main/LICENSE) and [trained-data repository](https://github.com/tesseract-ocr/tessdata_fast).

## Upload and storage boundary

The [OpenAPI contract](../contracts/openapi/core.yaml) defines allocation, status, part renewal, upload, completion, abort, media metadata/content, and analysis commands. This local provider intentionally uses **one bounded part per photo**, rather than claiming a cloud multipart implementation.

1. Allocation requires a live session, `purpose: REPORT`, the draft's `clientSubmissionId`, supported declared MIME, and 1–10 MiB declared bytes. The vault supplies a stable alias for that submission. Media and analysis records use this alias and carry no public profile/principal ownership reference. Allocation enforces four active photos per submission, 40 active photos and 100 MiB declared input bytes per resident across aliases.
2. The returned same-origin PUT URL has a random 256-bit capability. Only its hash is stored. PUT also requires the uploader's current authenticated session, vault ownership, CSRF/origin checks, the exact byte count, and an open, unexpired upload. Expiry is 30 minutes. Renewing rotates the capability without discarding a completed part; identical PUT retries reuse the part's SHA-256 ETag. A different payload conflicts. Completion retries return the same media item.
3. Completion verifies stored bytes and the part hash and moves the asset to quarantine. The worker determines the actual format independently of the declared MIME. Go reads the image configuration before decoding; it rejects malformed images, unsupported formats, dimensions above 8192 pixels per side, and images above 12 million pixels. SVG, HTML, audio, and video are unavailable in this slice.
4. The worker fully decodes supported pixels and re-encodes them as PNG, removing EXIF, GPS, text chunks, and trailing file content. Derivatives are bounded to 50 MiB. It records the original-byte SHA-256, decoded dimensions, and an identity pixel transform. EXIF orientation is not applied: previews and OCR use the same decoded pixel grid. This is a documented limitation for rotated phone images.
5. Only this derivative is served, through a current cookie/vault/role check on every request, with private/no-store and nosniff headers. The directory has no static/public endpoint. `APPROVED` means eligible for this private processing flow; it does **not** mean virus-scanned, privacy-redacted, publicly publishable, authentic, or independently verified.
6. Draft removal revokes authorization, aborts the capability, cancels analyses, and removes stored files. Expired incomplete uploads and unattached photos older than 24 hours are cleaned by the worker. Database cleanup markers let bounded batches resume after restart. Attachment to a submitted report prevents ordinary draft deletion and 24-hour expiry; submitted-photo retention/deletion requires a future governed workflow.

Quarantined originals are deleted after successful re-encoding or rejection. Submitted reports retain the decoded photo, original source hash, and correction provenance; they do not retain a forensic original or establish legal chain of custody. The 100 MiB quota measures declared input bytes, not expanded derivative disk usage. Abandoned temporary/orphan files after a process or host crash still need full filesystem reconciliation. Cloud object storage, real multipart resume, idempotent initial allocation after a lost allocation response, encrypted storage/backups, malware scanning, redaction, retention governance, and disk-capacity enforcement remain production work.

## Analysis and human review

OCR uses an actual Tesseract subprocess with the English pack, sparse-text segmentation, TSV output, one thread, a 25-second timeout, and a 1 MiB output bound. It receives only an internally selected decoded PNG path and fixed arguments. There is no shell command built from a request, image-supplied configuration, external model request, or text-to-action connection. The implementation follows upstream [TSV usage](https://tesseract-ocr.github.io/tessdoc/Command-Line-Usage.html) and Go's [untrusted-image guidance](https://pkg.go.dev/image#hdr-Security_Considerations).

Each task returns a discriminated JSON result with schema/model version, original source hash, `ORIGINAL_PIXELS`, decoded size, and at most 500 bounded regions. Empty extraction is explicit successful-empty. Confidence is `null`: raw Tesseract confidence has not been calibrated for this product. Results are capped at 256 KiB. The only quality rule currently checks resolution; blur and lighting assessment are not claimed.

Jobs pin source hash, media authorization version, language, and the requesting session's hash. Workers claim renewable-by-recovery two-minute leases with unique tokens and incremented job versions; an expired claim can be recovered after restart. Before each result and final state are saved, the worker rechecks live session/account/provider binding, source hash, media state/version, job expiry, lease token/version, and cancellation. Media-before-job row locking fences cancellation and photo revocation. Status reads return a consistent authorized job/task snapshot. Revoking the requesting session before completion cancels the work; a later new session of the same resident may access permitted completed results.

Independent tasks retain their own state. A failed OCR task can be retried up to three attempts without replacing a successful quality result. Unavailable task kinds/languages return `UNSUPPORTED` and no fabricated result. Partial success produces `PARTIAL`. Cancellation requires the current If-Match version, clears the unfinished job's results, and prevents stale output from applying.

Residents review an accessible word list with selection checkboxes and text inputs. Added words remain editable in the report statement. Corrections retain `{taskId,regionId,originalText,correctedText,appliedAt}` in private intake metadata. Submission checks that every referenced region belongs to a successful, current analysis of a photo attached to that exact report. Corrections never rewrite raw OCR. The API applies report creation, attachment, provenance validation, intake, idempotency receipt, and outbox in one transaction; failed attachment leaves no report.

Trusted-device draft storage is opt-in and owner-scoped. It stores text, opaque photo/job IDs, the stable submission ID, and correction provenance. It stores no photo bytes, cookies, signed URLs, upload capabilities, or provider tokens. Restoring reauthorizes photos and analyses through the API. Upload retry can reuse an already uploaded complete part while the current page still holds the selected File; an incomplete upload after page reload requires selecting the file again or removing it.

## Capability honesty and verification

`mediaUpload` is enabled when the local media module is configured. The legacy `ocr` readiness flag remains false; `analysisCapabilities` advertises the English preview as `EVALUATING`, independently of UI/text language labels. Hindi, Kannada, Tamil, Telugu, Marathi, Bengali and other OCR languages are unavailable. Image recognition, redaction, and voice remain planned. There are no evaluated per-language production claims or automatic agency-routing/restoration decisions.

Two dedicated database roles extend the [database isolation boundary](DATABASE_VAULT_ISOLATION.md): `js_media` handles scoped uploads/analysis requests; `js_media_worker` processes only media/upload/derivative/analysis tables. Forced RLS protects owners and attached coordinator reads. The worker cannot read reports, account/session tables, social content, or the vault. Narrow security-definer helpers return only permission/retention decisions. Migration 00009 tightens foreign attachment checks and adds durable cleanup progress without resetting existing volumes.

```bash
make check
make test-integration
make generate
npm run test:e2e
```

Checks exercise actual OCR on original fictional signs, photographed instructions as inert text, empty/bounded results, metadata removal, decode bombs, private file/path bounds, quarantined/foreign media denial, unsupported languages, report attachment and deduplication, immutable raw OCR under correction, expired capabilities, coordinator access, failed-task retry preserving success, cancellation and session-revocation fencing, and restricted worker credentials. Browser journeys cover private previews, editable OCR, trusted-device restoration without capabilities, 320px reflow, private report attachments, invalid-photo removal, and manual reporting with unavailable Hindi OCR. These focused checks do not replace production OCR accuracy/calibration evaluation, redaction validation, a full accessibility audit, load/recovery testing, or all canonical acceptance contracts.
