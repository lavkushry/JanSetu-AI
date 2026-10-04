# Resumable private photo uploads

The report wizard uploads photos in 2 MiB parts, shows committed-part progress, and resumes interrupted uploads within their original 30-minute lifetime. A saved private draft restores the media ID after reload. The resident chooses the original file again; the browser verifies its size, declared MIME and SHA-256 before uploading missing parts. Photo bytes, filenames and capability URLs remain in memory and are never written to the draft.

This is the local private-filesystem adapter. Cloud object storage, encrypted backups, capacity enforcement and crash-orphan reconciliation remain separate production work. Existing photo limits, ownership checks, image decoding, OCR and optional recognition still apply; see the [photo/OCR guide](PRIVATE_MEDIA_OCR.md).

## Allocation and recovery

The browser computes the SHA-256 of the selected file (at most 10 MiB) and creates a random `clientUploadId`. It sends these with the draft's `clientSubmissionId`, `mimeType`, `byteCount` and `purpose: REPORT` to `POST /v1/media/uploads`.

The API serializes allocation/quota checks. A unique index on the private report alias and client upload identity prevents duplicate allocation. A matching retry returns the existing media/upload identity before charging quota again, including completed or expired allocations. A changed hash, MIME or size under the same identity returns `409 UPLOAD_ID_CONFLICT`. The identity is scoped to the exact resident and draft, including when another resident sends the same client IDs.

If the allocation commits but its response is lost, **Retry adding photo** repeats the same request. The selected file, fingerprint and allocation identity are retained only for the current page. An allocation whose response was never received cannot be recovered through the draft after reload; its bytes have not been uploaded, and the abandoned asset follows ordinary expiry. Discarding such a pending allocation can leave an unused server record until expiry. Known media IDs in an opted-in draft can resume after reload.

## Parts and completion

| Operation | Behavior |
| --- | --- |
| `GET /v1/media/{id}/upload` | Owner-only, consistent locked metadata snapshot: original size/MIME/fingerprint, part size, completed ETags, deadline and current version. No capability URLs. |
| `POST /v1/media/{id}/upload-parts` | Requires current `If-Match`, unique in-range part numbers and an open unexpired session. Rotates only requested capabilities, preserves committed parts, increments version once. |
| `PUT /v1/media/{id}/parts/{number}?token=…` | Requires live owner authentication, CSRF/origin checks, that part's random 256-bit capability and exact expected part length. SHA-256 ETag records commitment. |
| `POST /v1/media/{id}/complete` | Requires every distinct part number and recorded ETag. Reads bounded part files in numerical order, verifies each length/hash and the whole-file fingerprint, then atomically writes the original and moves the asset to quarantine. |
| `DELETE /v1/media/{id}/upload` | Requires current version; denies attached evidence. Revokes media, cancels analysis and invalidates capabilities. Removes original, parts and any derivative; worker cleanup retries interrupted removal. |

There are at most five parts. Nonfinal parts are exactly 2 MiB; the final part holds the remainder. Capability rotation does not extend expiry. Tokens for one part cannot authorize another. Expiry, abort and completion clear part capabilities. Coordinator access to attached private derivatives does not confer upload mutation rights.

The browser sends parts sequentially and displays progress after each acknowledgement. Retry first reads status, skips committed parts, renews capabilities for missing parts and completes with all ETags. A lost PUT acknowledgement therefore does not require resending that committed part. A lost completion acknowledgement is recovered from `COMPLETE`, even after the worker removed input files. If all parts were committed before reload, **Finish upload** needs no local file.

Identical PUT retries keep the same ETag/version and rewrite the same bounded bytes, allowing repair of a missing or corrupted part file. Different bytes conflict. Missing/corrupt stored parts or a mismatched whole fingerprint return `422 UPLOAD_INTEGRITY`; a failed assembly never replaces an existing original. The browser tells the resident to remove and add the photo again. Parts remain private during quarantine, then worker cleanup removes them following approval or rejection. Database cleanup progress is marked only after all required removals succeed.

## Schema, privileges and compatibility

[Migration 00010](../db/migrations/00010_multipart_uploads.sql) adds the nullable allocation identity, nullable expected fingerprint and `infra.upload_part`. Forced RLS restricts part metadata to owners through `js_media`; `js_media_worker` has only the processing/cleanup privileges. Account, social, operations, publication and social-worker roles receive no part-table privileges. Every part mutation also locks the parent media/upload rows, serializing completion, token renewal, PUT and abort.

The [OpenAPI 0.5.0 contract](../contracts/openapi/core.yaml) and generated web types expose this behavior. Capabilities advertise `LOCAL_PRIVATE_MULTIPART`, 2 MiB part size and five parts. API callers that omit **both** new input fields retain the existing single-part protocol (up to 10 MiB). Existing sessions have a null expected fingerprint and use their old capability, ETag and original storage key; no file conversion/backfill is needed. Supplying just one new field is rejected. Legacy drafts with incomplete uploads must remove/re-add their file in the new browser; already committed legacy parts can still complete.

Apply the migration and install runtime grants before replacing the API/media-worker. A targeted local upgrade, preserving both volumes, is:

```bash
docker compose build migrate api media-worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api media-worker web
```

The ordinary `docker compose up --build -d` path also applies migrations through service dependencies. The migration has a forward-only rollback guard; schema reversal requires a reviewed forward migration. Older binaries can continue legacy sessions after schema addition, but cannot process new multipart allocations. Drain new allocations before any application rollback.

The declared-byte quota remains 100 MiB across 40 active photos per resident and four photos per draft. Parts plus assembled original may temporarily use twice the declared input space, before derivative expansion. This quota does not measure total disk usage. Temporary/orphan files left by process or host crashes still require filesystem reconciliation. Shared storage across API/worker replicas, storage durability, EXIF orientation, scanning/redaction and governed evidence retention remain pending.

## Verification

```bash
make check
JANSETU_VISION_BINARY="$PWD/services/vision/run" make test-integration
make generate
npm run test:e2e
```

Storage tests verify bounded assembly, path validation, per-part and whole-file hashes, atomic failure and idempotent cleanup. Database/API tests cover concurrent matching allocation retries, changed-content conflicts, quota recovery, owner isolation, capability scope/rotation, stale versions, exact lengths, identical/different retries, incomplete/duplicate completion, missing-file repair, full source hashes, completion after cleanup, expiry removal and role isolation. Existing legacy upload/OCR/vision/report tests continue to run.

Browser tests use real authenticated services. They lose an allocation response after server commit and confirm the retry reuses the identity. They interrupt the second part, restore the private draft after reload, reject a different same-size file before sending bytes, upload only the missing part, run actual OCR and submit the private report. A committed part with a lost acknowledgement completes after reload without selecting a file or resending bytes. The suite also preserves the lost-completion-response regression check, draft privacy checks and 320 px reflow. These checks do not establish production capacity, cloud recovery or complete accessibility coverage.
