# Baseline validation — 2026-10-07

## Passed

- Full Go `go vet ./...` and PostgreSQL integration suite with `JANSETU_INTEGRATION=1 go test -race ./...`. The integration harness creates and removes isolated databases; it does not migrate the active pilot database.
- Targeted recommendation integration suite after final deduplication changes, with the actual release Rust container through `JANSETU_RECOMMENDATION_TEST_TARGET=127.0.0.1:50051` and the race detector.
- Consent off by default, required/versioned preferences, reset/withdrawal generations, stale and foreign cursors, stable pagination/model changes, author cap, civic allocation, guest cookie binding, replay, expiry/retention, live hide/block/mute checks, duplicate/conflicting events, served-revision validation, duration manipulation, logical account deactivation and restricted-role denial.
- Invalid ranker IDs/revisions, explanations, duplicate content and dependency timeouts cause fallback. Rust rejects expired deadlines, over-budget requests and nonfinite/out-of-range features.
- Rust formatting, Clippy with warnings denied and three ranker tests; Python's three evaluation tests.
- TypeScript typecheck and Next.js production build.
- [Browser proof](../../scripts/recommendation_browser_proof.py): real browser → BFF → Go → Rust → isolated PostgreSQL, including consent, explanations, More feedback, reset, stale cursor rejection and chronological Following. One Chromium test passed; browser page errors were absent.
- Go protobuf generation with pinned generators, SQL generation, OpenAPI type generation, Markdown spec validation, Compose configuration validation and whitespace check.
- Container SIGTERM shutdown after live RPC requests: clean exit code 0 within the three-second stop budget.
- Rust Docker image build; read-only nonroot container with capabilities dropped, two-CPU limit and 512 MiB memory limit exercised through the Go client.

## Measured limits

The [transport smoke result](benchmark-local.json) uses 2,000 synthetic candidates/request and 200 ranked references, 1,000 requests at concurrency eight. It measured p95 14.893 ms, p99 17.071 ms, 718.87 requests/second and zero errors on a four-vCPU ARM host with a two-CPU container limit. This is a short transport test, not a steady-state complete-feed benchmark. Hardware/build/budget metadata is in the JSON artifact. Serving/event costs are unavailable.

No real session-satisfaction study, learned-model promotion, complete-feed 10,000-RPS test or million-DAU capacity claim is included. [Delivery status](README.md#delivery-status-and-gates) records those dependencies. Deployment remains local/test, with the recommendation rollout defaulting to shadow/0.
