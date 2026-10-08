# Bounded Rust ranking work

A per-connection gRPC limit does not bound CPU work across many connections. The Rust service now shares one admission gate across all recommendation calls in the process. `JANSETU_RECOMMENDATION_MAX_IN_FLIGHT` sets the maximum admitted ranking jobs, including any waiting for a blocking worker. The default is 8; valid explicit values are 1–128. Zero, an empty value, malformed input and larger values stop startup. Compose forwards the setting to Rust.

## Request behavior

An available slot admits the request to Tokio's blocking worker pool, keeping synchronous scoring off the asynchronous I/O executor. With no slot, Rust immediately returns gRPC `RESOURCE_EXHAUSTED`; it does not queue callers on the admission semaphore. Go makes one ranking attempt and uses its existing authorized chronological fallback on that error. Eligibility checks, stable snapshots, civic composition and the shared rollback switch still apply.

The worker owns the slot until its closure finishes. Dropping a caller, canceling an RPC or reaching the transport timeout therefore cannot free a slot while detached CPU work continues. Worker panic returns a generic internal error and releases its slot; invalid/expired requests also release capacity. The existing request deadline is checked by the ranker inside the worker, after any worker scheduling delay. Rust retains its 120 ms transport timeout, 1 MiB request limit, 128 KiB response limit and per-connection concurrency limit of 128.

This bounds admitted ranking jobs, not the number of sockets, TLS handshakes, decoded messages awaiting dispatch or all process memory. It is a per-process control, not a fleet quota. Deployment ingress/connection budgets, replica sizing and representative load/cost tests remain necessary. The configurable ceiling is not an automatically safe CPU/thread/memory setting: choose a value appropriate to container CPU, PID and memory limits. Restart the ranker to change it; use the [shared rollback](ROLLOUT.md) during operational replacement if needed.

Blocking work cannot be forcibly canceled by dropping a Rust future. This release ranks bounded candidate lists with a finite rules function. Future model/retrieval adapters must preserve bounded execution or provide their own cooperative cancellation/isolation. A hung blocking task would retain its slot and may delay process shutdown, rather than allowing canceled clients to grow unbounded CPU work.

Completed handler logs now include a gRPC status code alongside latency and success. They contain no viewer IDs, feature values or content. Canceled handlers may not produce a completion log; these logs are not a complete admission/accounting metrics system.

## Validation

Deterministic Rust tests hold workers with channels, fill shared capacity through separate ranker clones, and verify immediate rejection without invoking excess work. They check recovery after completion, cancellation while a worker still runs, panic, expired input and invalid capacity. These are synchronization tests, not timing-based capacity benchmarks.

The real Go/Rust mutual-TLS proof verifies successful ranking with an explicitly configured single-worker limit and rejects invalid limits at process startup. The PostgreSQL integration test verifies that `RESOURCE_EXHAUSTED` produces a nonempty fallback feed after exactly one ranking attempt. CI retains the full backend, stream, contract and browser regression suites. No sustained throughput or million-user capacity result is implied by this control.
