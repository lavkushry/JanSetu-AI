# Shared recommendation rollback

The deployment still defaults to `JANSETU_RECOMMENDATION_MODE=shadow` and rollout `0`. Mode/percentage configuration selects shadow → 1% → 5% → 25% → full eligible traffic with stable viewer/browser assignment. Changes to those deployment settings require restarting/replacing the API. The shared PostgreSQL switch adds an immediate override for all upgraded API replicas, without a Redis dependency or an API restart.

## Operator command

Use `services/backend/cmd/recommendation-control` against an explicitly migrated development database. Its database URL must authenticate as the separate non-owner `js_recommendation_control` login. Do not give this credential to the API, Rust service, stream consumer or media/vault services. The API's existing `js_social` login can only read the control through a fixed function.

```sh
cd services/backend
go run ./cmd/recommendation-control -mode get
# Example response: {"disabled":false,"version":1}
go run ./cmd/recommendation-control -mode disable -if-version 1
# Example response: {"disabled":true,"version":2}
go run ./cmd/recommendation-control -mode enable -if-version 2
```

Read the actual version before making a change; the example numbers are illustrative. Set `JANSETU_RECOMMENDATION_CONTROL_DATABASE_URL` when the endpoint differs. Only local/test mode is supported. The backend image includes `/app/recommendation-control`; the pilot Compose does not start it or supply its credential.

Writes compare the expected version while locking only the control aggregate. A stale expected version fails without changing state; competing changes cannot both advance the same version. A no-op retains the version. Every actual transition atomically records version, disabled state, time and authenticated database login in a private audit table. Neither API nor operator login can directly rewrite/read those tables. The operator cannot read user preferences/content/private cases, claim exported events, or connect to the vault.

## Serving behavior

Each new Recommended snapshot reads the live switch through a fixed PostgreSQL function with a 50 ms deadline. Disabled or unavailable control skips Rust/shadow ranking and creates an authorized chronological snapshot. Existing Following, Latest and Top behavior is unchanged. Enabling the shared switch allows the deployment's configured mode/percentage again; it does not increase rollout or bypass consent/permissions.

Ranked snapshots record the control version. Final hydration reads current control again and returns `410 CURSOR_EXPIRED` when ranking is disabled, the lookup fails, or the version differs. This also applies to a Redis cache hit. The client refreshes to obtain chronological fallback. A disable during ranking is caught before exposures/child cursor commit. A later enable cannot revive an older ranked snapshot; fresh requests use the current epoch. Chronological/shadow snapshots retain their frozen order.

Snapshots from older code without a control version require refresh if they were ranked. Deploy the capability to every API replica before relying on fleet-wide rollback. Requests that already passed the final control check can finish in flight; the switch governs subsequent reads. No control value is cached between requests, and a model result cannot override it. Failure to read permissions/consent still fails closed through the authoritative Go/database path.

## Verification and remaining gates

The isolated PostgreSQL proof executes the actual CLI, verifies role/vault isolation, audit records, live behavior on two API instances, stale and competing operator updates, disabled ranker calls, chronological snapshot stability, control-read failure and an in-flight disable. `make recommendation-stream-proof` additionally verifies invalidation while a ranked snapshot remains in real Redis, including after re-enabling.

Production still requires authenticated service transport, securely provisioned operator credentials/identity, monitoring and rollback drills, replicated infrastructure, feature parity, representative latency/throughput/cost tests and the documented privacy/intake launch gates. This switch is a tested rollout primitive for the local baseline, not evidence of production readiness or recommendation-quality improvement.
