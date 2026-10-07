# Governed prerequisite additions

A coordinator can now add necessary prerequisites to proposed restoration work,
with a reviewed reason. This extends [task sequencing](TASK_PREREQUISITES.md) and
[reviewed scope splitting](PARTIAL_ACCEPTANCE.md). Original proposal content stays
intact; a separate, immutable decision records each additional requirement.

## Workflow and boundaries

- Add one to seven distinct existing required restoration tasks in the same case.
  The target must remain proposed, required and in an open case. Additions are
  unavailable after acceptance and during a pending partial-acceptance review.
- Existing prerequisites cannot be removed, replaced or bypassed. Additions do not
  change task scope, agency, due date, original case age or other tasks' versions.
  Each decision increments the target task and case versions once.
- Already verified prerequisites are eligible. Cancelled, optional, foreign-case,
  non-restoration, already recorded and self prerequisites are ineligible.
- Cycle checks include explicit prerequisites and approved split replacement
  edges. For example, if resurfacing waits on original drainage work that was
  split, its drainage remainder cannot be made to wait on that resurfacing.
- Agencies accept using the current task version and may commit while blocked.
  Starting work, completion claims and verification wait for every prerequisite's
  independently verified work, including all required replacement leaves.
- A later scope split inherits the current full prerequisite set. Coordinator
  additions to a proposed split remainder follow the same rules. Scope review
  remains independent, with no changes permitted while it is pending.

This is a local synthetic workflow. It provides additions before acceptance;
governed removal, dependency repair, changes to accepted work, responsibility
transfers, disputes, SLA clocks and real mandates remain future work.

## API and retry contract

OpenAPI **0.25.0** adds one command. Browser mutations use `/api`, a session cookie
and `X-JanSetu-CSRF: 1`; direct Go calls use `/v1`.

```text
POST /api/authority/obligations/{taskId}/prerequisite-amendments
If-Match: "current target task version"
{
  "clientAmendmentId": "stable amendment UUID",
  "addedPrerequisiteTaskIds": ["existing prerequisite UUID"],
  "reason": "Coordinator reviewed that resurfacing requires earlier drainage work",
  "reviewed": true
}
```

The reason is trimmed and must contain 10–2000 Unicode characters. The response is
201 with amendment ID, target task ID, recorded added IDs and current task/case
versions. A matching original-coordinator retry accepts the original `If-Match`
even after later work or resolution, without extra edges, version increments,
history or outbox events. ID order is immaterial. Sessions and coordinator grants
are rechecked in the transaction before receipt replay; another coordinator cannot
reuse the same amendment identity.

Changed reason or added set for that ID returns `PREREQUISITE_AMENDMENT_CONFLICT`
(409). Fresh requests with an old task version return 412; missing version returns
428. An existing edge under a new identity returns `TASK_PREREQUISITE_EXISTS`
(409); a readiness cycle returns `TASK_PREREQUISITE_CYCLE` (422). Invalid/inaccessible
prerequisites receive the same safe validation response. Accepted/in-progress/
completed targets return `INVALID_TRANSITION` (409), closed cases `CASE_CLOSED`
(409), pending scope reviews `TASK_SPLIT_PENDING` (409). Agencies and residents
cannot amend the sequence.

The original task proposal's lifetime retry still compares its original
prerequisite set. Additions never rewrite that receipt. Resubmitting the original
proposal after an amendment recovers the original task; changing the original
proposal to include the new set conflicts. Current staff task detail always shows
the full live sequence.

## Coordinator interface and privacy

Eligible task cards offer **Add prerequisite tasks**. Expand the panel, select
compatible additional work, record a reason and confirm that the requirements
are necessary before work begins. The server supplies compatible choices and
rechecks them on submission. Every saved decision appears in private history with
its reason, time and links to the additional tasks. Agency readers see this
history and readiness without amendment controls.

Failed requests retain the selection, reason, confirmation and client identity
while the form remains mounted. Matching retries recover lost acknowledgements.
**Refresh case** preserves a pending draft through version conflicts; a saved
amendment appears in history. Drafts do not persist across reloads/devices. The
interface uses native labels/checkboxes, disabled saving controls, existing
light/dark themes and a 320px layout without horizontal overflow.

Private amendment reasons, task/client/amendment IDs, history and readiness stay
in the staff workspace. Resident progress remains an aggregate of required work.
Existing public receipts remain unchanged until a separate publisher reviews
progress. The reviewed timeline uses fixed safe text for prerequisite additions,
with no private amendment payload. Staff DTOs omit principal and client retry IDs.

## Persistence and upgrade

Migration [30](../db/migrations/00030_prerequisite_amendments.sql) adds an append-only
forced-RLS amendment ledger and same-case/task amendment links on prerequisite
edges. Original edges retain null amendment links. Initial edges may be inserted
only in the same transaction that creates the task; immutable creation timestamps
prevent disguising later edges as initial ones. Later edges require a new
matching amendment record. A deferred guard requires every declared addition and
the task version change to commit atomically. Update/delete privileges are absent,
and immutability guards also protect history against direct privileged rewrites.
Cycle guards include split replacement edges. The migration also requires an
explicit non-null reason for finalized split reviews, strengthening their existing
reasoned-decision contract.

1. Build `docker compose build api web`.
2. Run `make migrate` using existing migration credentials and vault keys; this
   installs the new restricted privileges. Do not reseed or reset data.
3. Replace API/web together: `docker compose up -d --no-deps --wait api web`.
4. Check application schema 30, vault schema 3, readiness and preserved records.
   Keep database, identity, vault, workers, keys and volumes. Public bindings remain
   `0.0.0.0:3100` and `0.0.0.0:8081`.

The command uses compatible `ObligationChanged` version 1. Older binaries cannot
create or expose amendment decisions; update API/web before using them. Downgrade
refuses to discard any amendment history; use a reviewed forward repair after use.

## Verification

Integration tests cover concurrent and reordered retries, changed content,
original proposal replay after amendments and resolution, cross-agency gates,
unchanged scope/deadline/age, explicit and split readiness cycles, inherited scope
work, pending split review, role/version validation, direct SQL audit guards,
atomic rollback, immutable history, runtime privileges, RLS, captured-actor
revocation and resident/public privacy. Existing split and sequencing flows remain
regression checks. Browser flows use real OIDC sessions for lost acknowledgements,
stale draft recovery, compatible choices, agency permission denial, stale
acceptance, independent verification release and 320px light/dark overflow checks.
These fictional inspections do not provide evidence of physical restoration.
