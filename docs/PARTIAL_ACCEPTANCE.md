# Reviewed partial acceptance

This local milestone extends [multiple required tasks](MULTI_AGENCY_RESTORATION.md)
and [task prerequisites](TASK_PREREQUISITES.md). An agency can offer to accept a
defined part of proposed work. A different coordinator must review complete,
non-overlapping scope coverage before creating the replacement tasks. Completion
of the accepted portion alone cannot resolve the case.

## Workflow

1. An active agent or agency lead requests partial acceptance of its own proposed,
   required restoration task. Supply accepted scope, all remaining scope and a
   private reason. Both scopes must be distinct, 10–1000 Unicode characters after
   trimming; neither may repeat the entire original scope. Reasons are 10–2000
   characters. Only the fictional `synthetic-local-mandate-v1` basis is supported.
2. The original task stays proposed while review is pending. Whole-task acceptance
   is paused, no replacement tasks exist, and all original required work remains.
3. A coordinator different from the proposer either rejects with a reason or
   confirms that both scopes cover the complete original work without overlap.
   Approval requires active agencies for both parts and an explicit agency for
   remaining work. Scope coverage is a human judgment, not an automated proof.
4. Approval atomically retains the original task as cancelled, immutable split
   history and creates two linked required tasks: the offered part is accepted by
   the original agency; the remainder is proposed to the selected agency. Both
   inherit the original prerequisite IDs and due date. Rejection creates no tasks
   and restores ordinary whole-task acceptance of the original proposed work.
5. Agencies perform each current required task. Independent verifiers inspect each
   completion claim; every required leaf must be verified before resolution.
   Replacing scope does not reset the original report time or create new deadlines.

The original task's stored required flag stays intact for history. Staff DTOs mark
only an approved split parent `scopeReplaced: true` and effectively non-required.
Ordinary cancelled required tasks remain unresolved. A proposed remainder can
itself be split, subject to the same independent review and capacity limits.
Existing dependent tasks keep their original prerequisite IDs. A replaced
prerequisite is satisfied only when all its current required descendants are
independently verified. Splitting a dependent task preserves those IDs even when
the prerequisite was already replaced.

[Governed prerequisite additions](PREREQUISITE_AMENDMENTS.md) can now append
requirements before acceptance. Both split children inherit the full current set;
additions are blocked while scope review is pending.

The pilot retains its eight physical task rows per case, including historical
parents. Each approval needs two free slots. Requests do not reserve slots;
competing proposals can consume capacity while review is pending. A coordinator
can still reject when approval exceeds the limit. Prerequisite removal, changes
after acceptance, silent bypass and splitting accepted/in-progress work remain
unsupported.

## Commands and retries

OpenAPI **0.24.0** adds the following commands. Browser requests use `/api`; direct
Go requests use `/v1`. Both require an authenticated session. Browser mutations
also require `X-JanSetu-CSRF: 1`.

```text
POST /api/authority/obligations/{taskId}/partial-acceptances
If-Match: "current task version"
{
  "clientRequestId": "stable request UUID",
  "acceptedScope": "Restore the north pavement portion",
  "remainingScope": "Restore the south pavement portion",
  "authorityBasisRef": "synthetic-local-mandate-v1",
  "reason": "Agency can initially commit to the north portion"
}
```

```text
POST /api/authority/cases/{caseId}/task-split-decisions
If-Match: "current case version"
{
  "requestId": "saved split request UUID",
  "result": "APPROVE",
  "remainingAgencyId": "active agency UUID",
  "reviewed": true,
  "reason": "Independent review confirms complete non-overlapping coverage"
}
```

For rejection, use `result: "REJECT"`, omit `remainingAgencyId`, and provide a
reason. The result includes request ID, state, current task/case versions and
nullable accepted/remaining task IDs. Matching request retries return 201;
matching decision retries return 200 with the original task identities and current
versions. They accept the original `If-Match`, including after case resolution,
without another increment, history entry or outbox event. Fresh requests and
reviews increment both original task and case versions once.

Retry permission is rechecked against live sessions and grants. Only the original
proposer may replay its request, and only the original independent reviewer may
replay the same decision. Changed content returns `TASK_SPLIT_CONFLICT` or
`TASK_SPLIT_DECISION_CONFLICT` (409). Missing `If-Match` returns 428; stale versions
return 412; invalid scope/basis/review/agency returns 422. A second pending request
or whole-task acceptance while pending returns `TASK_SPLIT_PENDING` (409).
Approval at capacity returns `TASK_LIMIT` (409); duplicate active scope returns
`TASK_SCOPE_EXISTS` (409). Closed cases reject fresh changes. Authorization checks
precede receipt replay or version/state checks.

## Interface and privacy

An eligible agency task offers **Accept part of this task** with separate labeled
scope and reason fields. Coordinator review displays the original scope and both
proposed portions, the agency reason, remaining-agency choice, coverage checkbox
and review reason. History links to both replacement tasks. Agency readers see
review history without coordinator actions; proposers cannot review their own
request even if they also hold a coordinator grant.

Failed submissions retain inputs while mounted. Matching retries recover dropped
acknowledgements. **Refresh case** preserves a pending coordinator review draft
through version conflicts. A refreshed, already saved proposal displays its
immutable request in history. Drafts do not persist across reloads/devices. Native
forms, disabled saving controls and the existing light/dark themes support narrow
screens, including 320px width.

Private scopes, reasons, lineage, request/client/task IDs and readiness stay in
the staff workspace. Resident progress contains current agency/task states and
all-required verification, omitting replaced historical parents. Existing public
receipts remain unchanged until a separate publisher reviews current progress.
Approved public projections contain only current task agencies/states/due dates
and fixed safe timeline text, without private split data.

## Persistence and upgrade

Migration [29](../db/migrations/00029_reviewed_task_splits.sql) adds a forced-RLS
split ledger and same-case parent links. Proposal identity and finalized decisions
are immutable. Guards require complete replacement pairs, unchanged prerequisites
and due dates, and a complete atomic confirmation at commit. Archived original
scope and required child identity cannot be rewritten. A recursive readiness
function treats every verified replacement leaf as required; ordinary
cancellation never satisfies it. Restricted runtime privileges permit proposal
inserts and decision-column updates, with no deletion or proposal rewriting.

1. Build with `docker compose build api web`.
2. Apply `make migrate` using existing migration credentials and vault keys. This
   also installs restricted privileges. Do not reseed or reset data.
3. Replace API/web together with `docker compose up -d --no-deps --wait api web`.
4. Check readiness, application schema 29, vault schema 3 and preserved records.
   Keep database, identity, vault, workers, keys, volumes and public bindings
   `0.0.0.0:3100` / `0.0.0.0:8081`.

Workers retain the compatible `ObligationChanged` version 1 payload. Older API/UI
must not operate reviewed split cases; upgrade both before enabling this workflow.
Downgrade refuses to discard any split ledger history, including rejected requests.
Use a reviewed forward repair after adoption.

## Verification and remaining work

Integration tests cover distinct agencies, inherited due dates/prerequisites,
nested remainders, already replaced prerequisite inheritance, all-required closure,
insufficient verification, concurrent retries, capacity conflicts, rejection,
self-review denial, captured-actor revocation, RLS, immutable history, atomic
rollback, original age, resident privacy and separate publication. Browser tests
exercise real OIDC sessions, dropped proposal/approval acknowledgements, stale
review draft recovery, rejection, both required completion flows and mobile themes.

This is a synthetic local slice of FR-20 and AC-53/54. Controlled staff provisioning,
real mandates, inspection evidence, responsibility disputes, reassignment,
adjudication, signed SLA clocks, escalation and reopening verified cases remain
production work. Free-text scope review does not establish real jurisdiction or
physical restoration evidence.
