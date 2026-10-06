# Multiple required restoration tasks

One private report can retain a single case with several required restoration
tasks. A coordinator can propose distinct work to different agencies or separate
tasks to one agency. The original report time, coordinator assignment, history
and independently identified public receipt stay with the case. Assignments use
synthetic local agencies, not confirmed real road ownership or external delivery.

## Staff and resident behavior

In **Staff workspace → Service cases**, coordinators see the required tasks
verified and **Propose another required task**. Select an active agency and
describe 10–1,000 characters of distinct work. New proposals are required for
restoration and start **PROPOSED**. A case has at most eight tasks, including its
intake task and existing optional scaffold rows. Closed cases cannot receive new
proposals. Exact case-insensitive scope duplicates for one agency are rejected;
semantic duplicate detection is not implemented.

Each agency accepts, starts and claims completion only for its own tasks. Each
form has separate input/error state. Other tasks remain visible as staff case
context; that does not authorize their work or access to the original report/photos.

Verification requires a live verifier grant for the task's agency and a principal
different from the completion claimant. Every required task must be **VERIFIED**
before resolution. Outstanding completion claims keep **VERIFICATION_PENDING**
while other agencies accept or start work. Verifying one task leaves required
remainder open. Failed inspection returns that task to work; another claim stays
available for review. Required cancelled work does not count as restoration.
Optional scaffold work does not determine required resolution or resident progress.

Residents see all agency/task states and a deterministic aggregate label. One
verified task with outstanding required work cannot label the report verified.
Task scopes, staff summaries, private reasons and internal task IDs are not added
to the resident DTO. Deadlines display **unavailable** without governed policy.

## API and retries

OpenAPI **0.22.0** adds:

```text
POST /api/authority/cases/{caseId}/obligations
X-JanSetu-CSRF: 1
If-Match: "current case version"
{ "clientTaskId": "UUID", "agencyId": "UUID", "scope": "Distinct required work" }
```

`clientTaskId` is a lifetime proposal identity scoped to the case; no additional
HTTP idempotency key is required. Scope trims outer whitespace. Matching retries
return 201 with the task's current `id`, `state`, `version`, `caseVersion`, without
another task, case increment, history or outbox event. The original `If-Match` is
accepted on these retries, including after resolution. Changed agency/scope for
that ID returns `TASK_PROPOSAL_CONFLICT` (409).

Fresh proposals require the current case version (412), an active agency, an open
case (`CASE_CLOSED`, 409) and capacity (`TASK_LIMIT`, 409). Exact duplicate work
returns `TASK_SCOPE_EXISTS` (409); missing `If-Match` returns 428; invalid input
returns 422. Coordinator authority is rechecked in the transaction before both
writes and retries. Agency/verifier commands retain live session/grant checks,
version fencing and row policies.

The browser retains proposal ID and scope after a failed acknowledgement or
version conflict while the form is mounted. **Refresh case** preserves that input.
Drafts do not persist across reloads/devices; review existing tasks after reload.

## Reviewed public progress

Private work never updates an existing public receipt automatically. A publisher
must review current case/publication versions. The approved projection contains
agency names, states, existing due dates and fixed safe event text. `TASK_PROPOSED`
becomes “A coordinator proposed another required restoration task.” Scopes, work
summaries, private reasons, task IDs and report/case identifiers are excluded.

## Upgrade and preservation

Migration [27](../db/migrations/00027_multiple_restoration_tasks.sql) adds scope,
nullable client proposal ID and creation ordering. Existing task identities,
acceptance, work and verification history remain. Empty legacy scope displays
**Restoration task assessed at intake**. New intake tasks have a generic scope;
new proposals have immutable identity/agency/scope/required flags enforced by a
trigger. The new owner-only helper returns agency/state/required flags under
vault ownership authorization, with execute permission only for operations.
The prior helper remains available; generated SQL selects explicit columns.
Older binaries can read the expanded schema, but their single-task progress
logic cannot safely operate the new multi-task workflow.

1. Build: `docker compose build api web`.
2. Apply `make migrate` with the existing migration administrator and vault keys;
   this also installs the restricted function privilege. Follow the
   [database upgrade procedure](DATABASE_VAULT_ISOLATION.md).
3. Replace API/web: `docker compose up -d --no-deps --wait api web`. Keep the
   database, identity, vault, core/media workers, keys and volumes.
4. Check readiness, schema 27 and preserved records. Web/API remain bound to
   0.0.0.0:3100/8081.

Enable the new API/web after migration and privilege installation. Worker event
contracts stay compatible: proposals reuse `ObligationChanged` version 1.
Schema downgrade refuses to discard scope/proposal data; use a reviewed forward
repair after operators start using the expanded schema.

## Verification and remaining work

Backend race tests use distinct agency grants to exercise scoped work, independent
and stale verification, required remainder, optional scaffold work, original age,
owner privacy and publication isolation. Concurrent retries create one task/event;
competing proposals respect versions and the eight-task cap. Tests also cover
immutable scope, changed content, invalid agency, captured-actor revocation on a
retry and order-independent progress. Browser tests use real OIDC sessions for
lost acknowledgement recovery, separate inputs, agency work, task-by-task review,
stale proposals, mixed agency permissions and 320px light/dark layout.

This is a local slice of FR-20 and required-work portions of AC-53/54, not completion
of those canonical contracts. The local fixture has a City Works agent/verifier;
backend tests provision temporary Water Services grants in isolated databases.
The [prerequisite milestone](TASK_PREREQUISITES.md) now enforces immutable task
sequencing at proposal time. [Reviewed partial acceptance](PARTIAL_ACCEPTANCE.md)
now preserves both required scopes through independent coordinator confirmation.
Controlled staff provisioning, dependency changes,
reassignment, disputes, adjudication, signed SLA clocks, escalation,
reopening verified closure and real agency delivery remain pending. Scopes do not
infer sequencing; explicit prerequisites enforce it. Inspection records fictional
decisions without physical
verification evidence; evidence-backed field verification remains a production gate.
