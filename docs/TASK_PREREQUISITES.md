# Sequenced restoration tasks

This local milestone extends [multiple required agency tasks](MULTI_AGENCY_RESTORATION.md)
with explicit prerequisites. A coordinator can propose resurfacing after a drainage
repair has received independent verification. Agency acceptance records commitment;
it does not authorize dependent work to start before verification.

## Workflow contract

- Select zero to seven distinct existing required restoration tasks when proposing
  a task. Every prerequisite must belong to the same case. Cancelled, optional and
  non-restoration scaffold tasks are ineligible. Already verified work is eligible.
- The original proposal's agency, scope and unordered prerequisite set are
  immutable. [Governed additions](PREREQUISITE_AMENDMENTS.md) can now append reviewed prerequisites before acceptance; removal, bypass and
  reassignment remain unavailable.
- Initial proposals reference existing tasks. Reviewed additions recheck the
  complete readiness graph; database guards reject cycles and self references.
  Chains and several prerequisites are supported within the existing eight-task case limit.
- Agencies can accept responsibility while prerequisites are pending. Starting
  work, claiming completion and verification require every prerequisite to be
  `VERIFIED`. Acceptance, work and completion claims do not satisfy this gate.
- `INSUFFICIENT` or `NOT_RESTORED` inspection results keep dependent work blocked.
  Verification releases readiness without changing the dependent task's state,
  task version, original case age or agreed sequence. The agency still starts work
  explicitly with the current task version.
- All required work must be independently verified before case resolution. The
  existing independent reviewer, live grants, session checks and case/task version
  fences continue to apply. Blocked attempts write no work, history or outbox event.

## API and persistence

OpenAPI **0.23.0** extends the existing proposal command:

```text
POST /api/authority/cases/{caseId}/obligations
X-JanSetu-CSRF: 1
If-Match: "current case version"
{
  "clientTaskId": "stable proposal UUID",
  "agencyId": "agency UUID",
  "scope": "Resurface after drainage restoration",
  "prerequisiteTaskIds": ["existing drainage task UUID"]
}
```

Omitting `prerequisiteTaskIds` means no prerequisites. An identical proposal retry
returns 201 with current task/case versions even with the original case version.
Reordering the IDs is equivalent; a changed set returns `TASK_PROPOSAL_CONFLICT`
(409). Invalid links, duplicate/nil IDs or more than seven prerequisites return
422. Fresh proposals continue to enforce case version, authority, open state and
capacity. Missing/foreign task IDs receive the same safe validation response.

Private staff obligations contain `prerequisiteTaskIds` and `blockedByTaskIds` as
deterministically ordered arrays. The latter contains prerequisites whose live
work has not been independently verified. For a prerequisite replaced by a
[reviewed scope split](PARTIAL_ACCEPTANCE.md), every required replacement leaf must
be verified; the original prerequisite ID remains unchanged. Blocked start/completion/verification commands return
`TASK_PREREQUISITES_PENDING` (409) after authorization and applicable version/state
checks. These checks run inside the existing serialized pilot transaction.

Migration [28](../db/migrations/00028_restoration_task_prerequisites.sql) adds
`ops.task_prerequisite`, composite same-case foreign keys, forced row security,
graph and immutability guards, and a database work-state gate. Operations has only
SELECT/INSERT permission; publication has SELECT for staff case context. Residents,
unassigned agencies and other runtime pools cannot read or rewrite the graph.
Existing tasks have empty prerequisite arrays and preserve their history.

## Staff interface and privacy

The proposal uses labeled keyboard-accessible checkboxes with task number, agency,
scope and verification status. Failure and **Refresh case** preserve the selected
prerequisites and proposal ID while the form remains mounted. Drafts do not persist
across reloads/devices. Successful proposals clear the selection for the next task.

Task cards identify pending prerequisites by agency and scope. **Accept task**
remains available; **Start work** stays disabled until all prerequisites are
independently verified. Refresh the case after another staff member's decision to
see current readiness. Server checks enforce the gate even against a forged request
or a stale screen. Layout supports the existing light/dark themes and 320px width.

Dependency IDs, scopes, readiness fields and private histories stay in the staff
workspace. The resident DTO continues to expose agency/task states only. Public
receipts update only through a separate reviewed publication using the existing
safe projection; sequencing never publishes private task details automatically.

## Upgrade and verification

1. Build API/web with `docker compose build api web`.
2. Run `make migrate` using the existing migration credentials and vault keys.
   This also installs the restricted table privileges. Do not reseed or reset data.
3. Replace API/web with `docker compose up -d --no-deps --wait api web`.
4. Check readiness, application schema 28, vault schema 3 and preserved records.
   Keep database, identity, vault, workers, keys and volumes. Public bindings remain
   0.0.0.0:3100 and 0.0.0.0:8081.

Worker contracts remain compatible with `ObligationChanged` version 1. Upgrade the
API/web together before exposing prerequisite tasks; older UI lacks readiness
indicators and older API cannot provide the safe prerequisite error, although the
database work gate prevents bypass. Downgrade refuses to discard existing links;
use a reviewed forward repair if sequencing has been used.

Backend race tests cover cross-agency chains and multiple prerequisites, failed
and insufficient inspections, concurrent blocked starts and reordered retries,
invalid/private links, database cycle/immutability/work guards, runtime permissions,
original age, all-required resolution and reviewed publication isolation. Browser
flows cover lost acknowledgements and stale proposals retaining prerequisite
choices, blocked acceptance/start, independent verification release, scoped agency
actions and mobile light/dark layout.

[Reviewed partial acceptance](PARTIAL_ACCEPTANCE.md) now inherits prerequisites
for both replacement scopes. [Governed additions](PREREQUISITE_AMENDMENTS.md) are
now available before acceptance.
Dependency removal, changes after acceptance, responsibility disputes,
SLA clocks, escalation and reopening already verified work remain pending. A
cancelled or permanently unavailable prerequisite requires future governed repair;
there is no silent bypass. This synthetic workflow does not establish actual
agency mandates or physical inspection evidence.
