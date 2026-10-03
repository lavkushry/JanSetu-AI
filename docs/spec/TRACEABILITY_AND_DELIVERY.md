# JanSetu AI — Traceability, Validation, and Delivery

Version: 3.0. Date: 3 October 2026. Status: canonical design for implementation.

Audience: Delivery leads, implementers, QA, program owners, and release reviewers.

This document owns: Acceptance evidence, failure coverage, requirement mapping, dependencies, and phased rollout. Read the [suite guide](README.md) and [domain glossary](../../CONTEXT.md) first. Requirements describe intended behavior; application code, agency integrations, and model evaluation remain implementation work. P0 is the controlled pilot, P1 is the next validated release, and P2 is later expansion.


## Contents

- [Deployment, observability, and verification](#deployment-observability-and-verification)
- [Pilot legal and governance readiness](#pilot-legal-and-governance-readiness)
- [Delivery plan and implementation backlog](#delivery-plan-and-implementation-backlog)
- [Architecture decisions and handoff](#architecture-decisions-and-handoff)
- [Extended acceptance contracts](#extended-acceptance-contracts)
- [Functional requirement implementation map](#functional-requirement-implementation-map)
- [Business and nonfunctional coverage](#business-and-nonfunctional-coverage)
- [Deployment inputs and release gates](#deployment-inputs-and-release-gates)
- [UX scenario catalog](#ux-scenario-catalog)
- [Documentation validation](#documentation-validation)

## Deployment, observability, and verification

### Deployment environments

Use local development with synthetic fixtures, isolated staging with synthetic or explicitly approved de-identified examples, and production with separate secrets, accounts, keys, and audit destinations. Never clone protected production data into ordinary development.

For the pilot, run containerized API and worker workloads on a managed container service with multi-zone database availability where supported. Use infrastructure as code and private database networking. Kubernetes is an optional later operational choice, not a pilot prerequisite.

Deploy at least two public API instances for availability, independently scale workers, and reserve resources for intake and deadline processing so feed traffic cannot starve operational work.

### CI/CD and migration sequence

1. Validate formatting, compilation, dependency locks, OpenAPI, event schemas, and generated clients.
2. Run unit tests for transitions and policies, then integration tests against the selected PostgreSQL/PostGIS version.
3. Scan dependencies, containers, secrets, and infrastructure configuration.
4. Apply migrations to an empty database and a representative previous-version snapshot.
5. Deploy backward-compatible database additions, then applications, then projection backfills.
6. Run canary checks with synthetic accounts and cases.
7. Expand deployment if error, authorization, and projection metrics remain healthy.
8. Remove deprecated columns or event fields only after consumers and rollback windows allow it.

Rollback cannot depend on a destructive down-migration. Use expand/contract changes and compatible application versions. Model configuration, ranking weights, category activation, and publication permissions have separate versioned rollout controls.

### Operational monitoring

Measure API availability, latency, errors, database saturation, cache health, upload failures, outbox age, retry volume, oldest unreviewed item, moderation capacity, delivery lag, overdue obligations, unauthorized access attempts, and projection-revocation latency.

Logs contain trace IDs, object references, outcomes, and sanitized error codes. They do not contain report bodies, raw location, tokens, identity fields, uploaded media, or unrestricted model prompts. Restrict metrics labels to bounded low-sensitivity values.

Alerts distinguish technical outages from operational failure. “No agency acknowledgement for this cohort” is a program issue even when all APIs are healthy. “Protected queue unstaffed” is a launch-scope failure, not simply another latency metric.

### Failure behavior

| Failure | User-visible result | Recovery |
|---|---|---|
| AI unavailable | Report saved; interpretation pending or manual flow | Retry or reviewer queue |
| Redis unavailable | Slower deterministic feed or explicit retry | Rebuild disposable cache |
| Search lag | Search results checked against current publication state | Replay projection events |
| Object processing fails | Text draft retained; media unavailable for publication | Reprocess or ask for another attachment |
| Agency endpoint unavailable | Delivered state not claimed; next retry shown to staff | Stable-key retries and agreed manual fallback |
| Publication worker delayed | Case progress retained internally; public update pending | Backlog alert; replay safe events |
| Authorization service uncertain | Sensitive operation denied temporarily | Restore identity/policy service |
| Database outage | No false success receipt | Retry with same idempotency key |
| Staff review capacity exceeded | Affected publication or intake category paused with guidance | Capacity escalation and scope reduction |

### Meaningful acceptance tests

These tests are required implementation work. They have not been executed merely by producing this document.

| Test | Expected result |
|---|---|
| AC-01: Submit one post concurrently with the same idempotency key | One resource; same result returned |
| AC-02: Reuse that key with a different body | Conflict; no second write |
| AC-03: Submit +1, +1, -1, 0 votes | One, one, one, zero active vote rows respectively |
| AC-04: Approve an obsolete revision after a newer edit | New text is not published under old approval |
| AC-05: Reply using a parent from another post | Foreign-key/service rejection |
| AC-06: Delete a parent comment | Thread remains navigable with a tombstone |
| AC-07: Block an account during a follow or vote attempt | Defined lock order prevents post-revocation interaction |
| AC-08: Revoke a post already in feed snapshots | No new authorized hydration; managed copies purged within target |
| AC-09: Revoke the source of a quote | Source body and preview disappear; quote text receives required review |
| AC-10: Attempt private-object enumeration through search/facets | No existence or snippet leakage |
| AC-11: Submit a sensitive allegation in a public draft | Withheld and routed to the reviewed safer process |
| AC-12: Query protected report IDs through social APIs | No body, existence signal, recommendation, or preview |
| AC-13: Use a moderator role to access raw evidence | Denied and audited |
| AC-14: Transfer a case between agencies | First-valid-report time unchanged |
| AC-15: Agency sends “completed” callback without verification | Completion claim only |
| AC-16: Two agencies dispute one case | Separate obligations and adjudication clocks; no resubmission |
| AC-17: Route a boundary point with uncertain coordinates | Multiple candidates or review; no invented certainty |
| AC-18: Activate new boundaries after a case is accepted | Historical decision reconstructable; reassignment explicit |
| AC-19: Deliver duplicate or out-of-order events | No duplicate delivery effect or stale projection resurrection |
| AC-20: Restart app after successful upload but lost submission response | One report after retry |
| AC-21: Downgrade user or officer permissions mid-session | Subsequent scoped operations fail |
| AC-22: Put a low-vote urgent case beside a popular routine case | Urgent case retains precedence in Unresolved |
| AC-23: Disable personalization | Explicit locality/follow behavior remains; behavioral features stop |
| AC-24: Use screen reader, keyboard, text zoom, and local script | Core reporting and participation tasks complete |
| AC-25: Restore backup and replay authorized events | Recovery objectives measured; deletion ledger reapplied |
| AC-26: Withdraw a publication while notification waits | Notification does not reveal withdrawn text |
| AC-27: Attempt self-approval of identity disclosure | Denied; separate authorized approval required |
| AC-28: Complete data deletion with a documented legal hold | Non-held data removed; held scope and access remain explicit |
| AC-29: OCR succeeds while issue detection fails | Completed text retained; targeted retry or manual reporting without reupload |
| AC-30: OCR returns incorrect or mixed-script text | Resident can correct/reject selected spans before applying to the report |
| AC-31: A delayed analysis targets replaced/deleted media | No findings applied to a different active revision; stale result cancelled/discarded |
| AC-32: Cropped/resized/rotated evidence gets a region overlay | Coordinate transforms map regions to the correct submitted image |
| AC-33: OCR reads instructions embedded in an image | No permission, remote-fetch, case-status, or agency-action side effect |
| AC-34: Image quality or model/provider availability is insufficient | Explicit warning/failure; preserved draft and manual submission option |
| AC-35: Return from thread or search with new feed items waiting | Chosen filters and stable reading anchor restored; explicit refresh affordance |
| AC-36: Use light/dark themes and both feed densities | Core tasks remain accessible at large text/zoom with reachable labeled controls |
| AC-37: Transcription is unavailable, empty, or unclear | No fabricated report content; manual text and correction remain usable |

### Data lifecycle and recovery

Before enabling a data class, approve its purpose, owner, retention period, deletion trigger, legal-hold conditions, backup treatment, and export rules. Distinguish public posts, drafts, raw media, derivatives, operational evidence, interaction signals, authentication records, and audit records.

Keep a deletion ledger outside replaceable projections. Restoring a backup must reapply deletion and revocation tasks before reopening access. A legal hold preserves only the approved scope and does not restore public visibility.

Perform restoration exercises using representative volumes. Verify encrypted evidence, key recovery, case event ordering, current grants, idempotency records, and outstanding deadlines. Backups that have never been restored are not sufficient evidence of recoverability.

## Pilot legal and governance readiness

### Counsel briefing: decisions required before protected intake

This subsection is a briefing for qualified Indian counsel and the safeguarding partner. It asks for decisions; it does not answer the legal questions or declare JanSetu compliant.

| Area | Questions counsel must answer | Required decision artifact |
|---|---|---|
| POCSO | When does a submitted narrative trigger reporting duties for JanSetu, its staff, contractors, or partners? What recipient, timing, documentation, and emergency pathway apply? How should child identity, unsafe guardians, and unsolicited sensitive media be handled? | Signed child-safety intake and reporting protocol with current source law and local procedure |
| BNSS section 173 | What can JanSetu receive, prepare, transmit, or track? Which acts must the informant or police perform? What constitutes an authorized electronic submission, signature, receipt, registration, and transfer? | Partner-specific submission and status terminology approved by counsel |
| DPDP | Which provisions and rules are in force on the planned launch date? Who is the fiduciary or processor for each flow? What basis, notices, age assurance, child-data treatment, retention, rights, and breach process apply? | Data-flow-specific legal basis and implementation schedule |
| CVC PIDPI | Which allegations and officials fall within the route? Can JanSetu assist or submit, through which channels, and with what identity requirements? What destroys or limits the intended protection? Which state-level alternatives apply? | Approved route matrix and disclosure wording |
| Public social content | What intermediary duties, grievance channels, notices, evidence preservation, AI-content labels, and response timelines apply to this product and scale? | Moderation/legal-request policy and clock configuration |
| Lawful access | Who may demand what records, by which procedure? When can a request be narrowed or challenged? When is notice prohibited or unsafe? What emergency exceptions and review requirements apply? | Disclosure and preservation playbook |
| Cross-agency sharing | Which party may access each evidence field, for what purpose, and under which agreement? What happens when the receiving authority is implicated? | Executed sharing and independent-referral agreements |
| Public accountability | Which status statements and aggregate outcomes may be published? What review, correction, defamation, and re-identification issues require controls? | Publication policy and approved status language |

The DPDP Rules publication includes staged commencement; a launch plan cannot assume every provision became operative at once [S12–S13](README.md#sources). The social-platform legal review must consider the current official intermediary-rules text, including its amendments [S14](README.md#sources). MHA and CVC materials are starting points for counsel, not permission to represent JanSetu as an official filing channel [S15–S16](README.md#sources).

The current consolidated POCSO Act, Rules, and applicable local procedures must be supplied and confirmed by counsel. The official statute download was not successfully retrieved during this drafting pass.

### Required operational agreements

Before the public-service pilot, sign the companion adjudication clause with completed scope, named officers, delegated decision authority, clock definitions, calendars, interim-safety responsibilities, appeal route, and permitted publication statements. Resolve its late-dispute timing ambiguity explicitly.

Also execute data-processing and sharing terms, integration and outage procedures, moderation authority and appeal arrangements, evidence retention and export terms, and a sponsor-funded continuity plan for staff turnover or pilot termination.

A product team cannot compensate for an unsigned responsibility agreement by making stronger claims in the interface.

### Category-level go/no-go

| Gate | Evidence required | Owner |
|---|---|---|
| Public community launch | Working moderation and appeals; tested publication boundaries | Trust-and-safety lead |
| Operational civic intake | Reviewed jurisdiction release and signed agency process | Program owner |
| Public receipt publication | Approved labels, redaction, correction, and revocation tests | Publication owner |
| Protected intake | Counsel-approved category protocol, staffed safe referral, tested vault and grants | Legal and safeguarding leads |
| Personalized behavior signals | Approved data purpose, opt-out, age policy, and deletion tests | Privacy/product owner |
| Native app release | Device, permission, accessibility, and notification testing | Mobile lead |

A failed protected-category gate leaves that intake disabled while approved public-service work can proceed. It does not justify silently accepting real protected evidence into an unstaffed demo.

## Delivery plan and implementation backlog

### Three demonstrations to build first

| Demonstration | Minimum end-to-end behavior | Insight proved |
|---|---|---|
| Community-to-case | Resident posts, neighbor replies, author submits service report, both follow one public receipt | Social attention can become coordinated action |
| Responsibility dispute | Two synthetic agencies accept different tasks and dispute ownership; case age persists | Jurisdiction ambiguity becomes managed work |
| Safe publication | Ordinary case creates a sanitized receipt; synthetic protected report stays absent from feed/search/profile | Public accountability does not require exposing reporters |

A 72-hour hackathon can demonstrate these using synthetic data, seeded rules, and explicitly simulated agency events. It cannot establish legal readiness, nationwide routing quality, production security, or actual government adoption.

### Estimated implementation sequence

Assume two backend engineers, two web/mobile engineers, one AI/data engineer, one QA engineer, and shared product/design, infrastructure, legal, and trust-and-safety capacity. This is a planning estimate to refine after the first design and integration spikes.

| Stage | Indicative timing | Deliverables | Exit evidence |
|---|---|---|---|
| Discovery and agreements | Weeks 1–3 | Pilot scope, research, legal questions, data inventory, signed responsibilities | Owners and launch category decisions |
| Foundation and social core | Weeks 3–7 | Auth, profiles, communities, posts, review, comments, votes, follows, block/mute | Core social contract and authorization tests |
| Civic workflow | Weeks 6–11 | Intake, geography versions, case linkage, obligations, dispute clocks, receipts | Synthetic cross-agency acceptance scenario |
| Feeds and mobile parity | Weeks 9–14 | Ranked/chronological feeds, search, native flows, notifications, offline retry | Ranking, accessibility, and device tests |
| Operational hardening | Weeks 13–17 | Integration recovery, security review, moderation drills, restore, load tests | Pilot readiness gates |
| Controlled pilot | Weeks 18 onward | Limited communities and categories, measured weekly review | Demonstrated ownership and safe outcomes |

Parallel stages require named dependencies and sufficient staffing. Native release review, legal agreements, and agency integration timing are external dependencies. Do not promise a fixed launch date before they are understood.

### Backlog packages with acceptance boundaries

| Package | Core work | Depends on | Done when |
|---|---|---|---|
| B-01 Identity | OIDC, profile binding, MFA for staff, role revocation | Identity provider decision | Cross-role access tests pass |
| B-02 Communities | Directory, rules, membership, stewards, bans | B-01 | Moderator scope and appeal tested |
| B-03 Publishing | Drafts, revisions, uploads, review, safe rendering | B-01, media quarantine | Obsolete approval cannot publish |
| B-04 Conversations | Comments, thread pages, votes, reposts, bookmarks | B-02, B-03 | Concurrent actions and deletion tested |
| B-05 Case intake | Voice/text, location, duplicates, receipt | Approved pilot taxonomy | Retry creates one report |
| B-06 Responsibility | Geography releases, rules, obligations, disputes | Agreements and source data | Ownership dispute preserves continuity |
| B-07 Publication | Receipt projections, correction, withdrawal | B-05, B-06 | No raw identity/evidence fields appear |
| B-08 Discovery | Feeds, search, explanations, preferences | B-03, B-04, B-07 | Urgency and visibility invariants pass |
| B-09 Trust operations | Reports, decisions, appeals, legal queue | B-01, B-03 | Independent review and audit work |
| B-10 Native client | Navigation, media, low-bandwidth, push | Stable API contracts | Critical tasks pass on pilot devices |
| B-11 Operations | Delivery retries, monitoring, backup, incident response | All enabled services | Recovery objectives measured |
| B-12 Protected lane | Separate vault, safe contact, specialist workflow | All protected-category gates | Independent security and safeguarding review |
| B-13 Media understanding | Versioned OCR/vision jobs, preprocessing transforms, correction UI, task retries, evaluation | B-03, B-05, approved taxonomy and model/provider | AC-29–34 and labeled per-task release thresholds pass |

All user-facing packages include the [UI specification's](UI_IMPLEMENTATION.md) relevant UX-01–37 scenarios and theme/density/state review. B-13 is required for enabled OCR/image-recognition features; the B-05 manual text/photo intake remains usable during analysis failures.

### Requirement traceability

| Business objective | Product requirements | UI | Backend/data | Acceptance evidence |
|---|---|---|---|---|
| Lower coordination effort | FR-16–20 | Report wizard; case timeline | Reports, observations, deliveries | AC-14–16, AC-20 |
| Useful community participation | FR-03–10 | Community and thread | Membership, revisions, comments, votes | AC-01–06 |
| Reach without distortion | FR-11–14; feed rules | Repost controls; Unresolved | Follow graph, ranking, receipt projection | AC-07–10, AC-22 |
| Reporter protection | FR-02, FR-19; protected boundary | Private help; safe publication preview | Vault, grants, separate credentials | AC-11–13, AC-27 |
| Continuous responsibility | FR-20; adjudication supplement | Authority queue and dispute view | Obligations, clocks, route decisions | AC-14–18 |
| User control | FR-13–15; feed preferences | Settings and Activity | Blocks, mutes, deletion tasks | AC-07–09, AC-23, AC-26 |
| Accessible service | Voice intake and UI requirements | Web/native forms and threads | Transcript review, resumable intake | AC-20, AC-24, AC-37 |
| Understandable photo reporting | OCR/vision task contracts | Analysis review and editable suggestions | Media-bound jobs, transforms, evaluations | AC-29–34; UX-18–26, UX-29 |
| Consistent social experience | Theme, density, and social presentation | Feed, thread, composer, search | Session navigation state and authoritative social actions | AC-35–36; UX-01–17 |
| Sustainable operation | BR-04; reliability requirements | Queue and capacity indicators | Metrics, recovery, integration adapters | AC-19, AC-25, AC-28 |

## Architecture decisions and handoff

### Decisions made in this version

| Decision | Consequence |
|---|---|
| Go backend selected by the user | `net/http` APIs and workers, `pgx`/`sqlc` persistence, Goose migrations, explicit transaction and authorization boundaries |
| Public posts, reports, cases, obligations, and receipts are distinct | Social popularity cannot mutate operational truth |
| One voting model, no redundant like counter | Clearer semantics and simpler interaction consistency |
| No open direct messaging in the pilot | Fewer private harassment and moderation surfaces |
| Feed eligibility precedes ranking | Safety cannot be traded for relevance |
| Urgency precedes engagement in Unresolved | Neglected problems can receive attention without virality |
| Public case authorship is not a reporter profile | Accountability records do not create automatic identity links |
| Versioned geography and source-backed responsibility | Routing remains explainable after boundary changes |
| Outbox before streaming infrastructure | Pilot reliability has manageable operational complexity |
| Web and native share contracts, not all UI code | Accessibility and device behavior remain appropriate |
| Protected reporting is separately gated | Broad product ambition does not imply unsupported live intake |
| Institutional exports are required | Dependence comes from useful continuity, not trapped records |

### Implementation handoff checklist

The development team should produce the following repository artifacts from this specification:

- OpenAPI contracts with role, validation, version, and idempotency behavior for every enabled endpoint.
- Complete Goose SQL migrations, grants, triggers, and seed data matching the reference schema and additional table contracts.
- UI component library, screen prototypes, local-language copy, and web/native accessibility test cases.
- Light/dark/system themes, feed densities, card anatomy, and screen/state coverage matching the social experience plan.
- Evaluated OCR/vision adapters, model assets or approved provider configuration, media-coordinate transforms, correction UI, and per-task failure handling.
- Versioned routing rules and source manifests for the selected pilot geography.
- Event schemas, worker retry policies, projection rebuild commands, and deletion/revocation procedures.
- Threat-model review, scoped authorization tests, restore evidence, and operational runbooks.
- Signed pilot agreements, approved publication policy, staffed moderation roster, and protected-category decisions.

This document defines the implementation. Production readiness requires working software, tested integrations, staffed processes, and completed legal decisions.

## Extended acceptance contracts

AC-01–37 above retain their identifiers. The following cover the expanded specification. All are planned software acceptance tests, distinct from documentation validation. Protected/P1/P2 tests become mandatory before their feature is enabled; manual fallback and feature-disable tests apply to the pilot.

| Test | Setup and expected observable result |
|---|---|
| AC-38 | Published comment edit is pending/rejected/approved: public body remains prior approval until exact current revision approval; removal prevents revival |
| AC-39 | Suspend the actor/revoke agency grant during queued mutation: after revocation commits, all new reads/writes/downloads deny and cache clears |
| AC-40 | An outbox lease expires, another worker claims it, old worker returns: old token cannot acknowledge/change the new claim; duplicate consumer effect suppressed |
| AC-41 | Commit version 3 projection before delayed version 2: authoritative recomputation/checkpoint prevents regression; delta gaps cannot be silently skipped |
| AC-42 | Different viewer/filter/expired feed cursor: reject reuse; expired returns refresh; removed items skipped with scan position advancing |
| AC-43 | Member joins restricted community: pending member cannot publish; contributor approval allows; stale rules/banned membership blocks new writes |
| AC-44 | Community request impersonates an agency or duplicate area: stays in review/rejected with reason; no immediate public community; old merge links resolve |
| AC-45 | Question author selects another post's comment or a removed response: deny; valid same-question response is labelled Helpful, never verified fact |
| AC-46 | Self-vote, generated case-update vote, raw voter-list request: deny; no vote identities exposed; discussion votes cannot alter case urgency |
| AC-47 | Notification queued before consent/block/quiet-hour/source change: send-time scope recheck suppresses or postpones; no removed text/thumbnail |
| AC-48 | Upload mismatch, malicious format, oversize/decode bomb, expired signed URL: quarantine/reject without model execution; resumed valid parts reused |
| AC-49 | Voice READY while OCR PLANNED for same language: UI advertises separate capabilities; manual Unicode report works; no unsupported OCR result invented |
| AC-50 | Script/IME/RTL fixture in each enabled locale: composition intact, names isolated, text shapes correctly, core tasks complete at large text |
| AC-51 | Staff export built, then grant revoked: status/download deny, artifact revoked/deleted on policy; only originally allowlisted scoped fields written |
| AC-52 | Deadline policy absent/changed/late dispute: unknown clock shown; signed version and event anchor preserved; case age and prior breaches unchanged |
| AC-53 | First agency accepts part of work: required remainder remains an obligation with coordinator/clock; case cannot resolve from one partial completion |
| AC-54 | Verification evidence belongs to another case or incomplete required obligations: deny verification/resolution; valid independent decision succeeds |
| AC-55 | Same report clientSubmissionId retries after HTTP receipt expiry: matching content returns original report; altered normalized body returns 409 |
| AC-56 | Publication fields contain alias/raw coordinates/contact/media-original key: rejected/withheld; only reviewed safe projection enters receipt/search |
| AC-57 | Partner callback duplicate/altered payload/unknown status or timeout: inbox dedupe/hash conflict/review; delivery ambiguity does not create acceptance |
| AC-58 | Open canonical public/private deep link before/after login/block/removal: correct eligible destination; unavailable stays generic; no private preview |
| AC-59 | Account closure/data request with legal hold: discovery removed as approved, non-held data deleted, held scope restricted and clearly recorded |
| AC-60 | Deploy expanded schema with old/new clients and rollback app: supported contract window works; runtime role cannot migrate; irreversible data steps gated |
| AC-61 | Data outage/provider outage/load envelope: bounded latency/queues/errors, preserved drafts, measured recovery/projection/purge objectives |
| AC-62 | Logs/traces/analytics exercise sensitive fixtures: no tokens/report bodies/evidence/aliases; authorized aggregate telemetry only |
| AC-63 | Search scoped facets, aliases, transliteration and removed hits: relevance evaluated for enabled languages; no inaccessible content or count leaks |
| AC-64 | Information request and response: only owned report alias can answer; original statement unchanged; expired staff grant denies further evidence access |
| AC-65 | Native task completes offline: unsent state persists; retry reuses upload/command key; work completion cannot declare verified restoration |
| AC-66 | Quoted source revocation / P1 private community: source preview removed, private membership counts/snippets protected, explicit author review where needed |
| AC-67 | P1 playbook edited after review: review date binds to exact steps revision; old review cannot imply new steps are checked |
| AC-68 | P2 DM/live/predictive requests while disabled: no enabled route/advertised capability; required consent, abuse, capacity/data evidence before activation |

## Functional requirement implementation map

API names below omit the `/v1` prefix for readability. Storage names are canonical schemas from backend/system design. Every FR has implementation ownership and acceptance evidence; a mapping is not a passing test result.

| FR | UI / delivery package | API or interface | Storage / event | Acceptance |
|---|---|---|---|---|
| FR-01 | Login / B-01 | OIDC BFF/native PKCE, GET me/logout | identity binding/session/grants | AC-21,39,58 |
| FR-02 | Profile / B-01 | profiles/{handle}, me/profile | social.profile + allowlisted DTO | AC-10,13,56 |
| FR-03 | Community / B-02 | communities/{id}, directory | social.community, geo.admin_unit | AC-17,43,44 |
| FR-04 | Join/follow/member review / B-02 | membership, follow, member-decisions | community_member/follow/decision | AC-21,43 |
| FR-05 | Community request / B-02 | community-requests + decisions | social.community_request | AC-44 |
| FR-06 | Thread / B-04 | comments list/create/edit/delete | comment/revision, CommentReviewRequested | AC-05,06,38 |
| FR-07 | Thread sort / B-04 | comments sort NEW/OLDEST/TOP | comments/votes/stats | AC-24,35,46 |
| FR-08 | Vote controls / B-04 | post/comment vote PUT | vote rows, VoteChanged | AC-03,07,46 |
| FR-09 | Receipt/generated update / B-07 | receipts, forbidden CASE_UPDATE vote | case_receipt, policy | AC-22,46 |
| FR-10 | Viewer vote / B-04 | vote DTO; no voter API | stats, restricted vote rows | AC-03,46 |
| FR-11 | Repost / B-04 | posts/{id}/repost | social.repost, RepostChanged | AC-08,09,35 |
| FR-12 | Quote / P1 | quote post source contract | post.source_post_id | AC-09,66 |
| FR-13 | Block/mute / B-08 | me/blocks, me/mutes | profile_block/mute, BlockChanged | AC-07,21,39 |
| FR-14 | Institutional receipt / B-07 | case-receipts/{id} | sanitized receipt/event | AC-13,22,56 |
| FR-15 | Delete/tombstone / B-03/04 | post/comment DELETE | deny state, deletion_task, ContentRevoked | AC-06,08,28 |
| FR-16 | Report wizard / B-05 | service-reports | report/evidence, ReportReceived | AC-20,55,56 |
| FR-17 | Create case from post / B-05 | prefill draft then service-reports | separate report; no implicit linkage | AC-11,20,56 |
| FR-18 | Similar cases / B-05/06 | duplicate suggestions, observations | observation/route review | AC-17,18,54 |
| FR-19 | Safe preview / B-07 | publications, public receipt | vault aliases/publication decision | AC-11–13,56 |
| FR-20 | Progress labels / B-06/11 | my-reports, partner inbox | agency_delivery/obligation/case_event | AC-14–16,57 |
| FR-21 | Feed modes / B-08 | feed + filters | feed_snapshot/stats/receipts | AC-22,23,35,42 |
| FR-22 | Search / B-08 | search + scoped facets | safe projection; PostPublished/Revoked | AC-10,63 |
| FR-23 | Activity / B-08 | activity/read | social.notification | AC-26,47 |
| FR-24 | Notification settings / B-08/10 | preferences/activity | preferences/notification delivery | AC-26,47 |
| FR-25 | Upload/viewer / B-03/13 | media upload/complete/status | media_asset/upload/derivative | AC-32,48,56 |
| FR-26 | Offline draft / B-05/10 | same creation/submission key | local draft + server receipt | AC-20,55,65 |
| FR-27 | OCR editor / B-13 | analyses request/get | analysis_task OCR/correction draft | AC-29,30,32,49 |
| FR-28 | Photo candidates / B-13 | analysis ISSUE_DETECTION | task result/taxonomy/version | AC-29,34 |
| FR-29 | Voice transcript / B-05/13 | analysis VOICE_TRANSCRIPTION | task + original audio reference | AC-37,49 |
| FR-30 | Partial/cancel/retry / B-13 | analyses retry/delete | pinned hash/version + task lease | AC-31–34,40 |
| FR-31 | My cases / B-05 | my-reports list/detail/responses | vault alias + information_request | AC-20,39,64 |
| FR-32 | Route proposal / B-06 | authority cases/routes | geo rules/release + route_decision | AC-17,18 |
| FR-33 | Agency actions / B-06 | accept/partial-accept/disputes/updates | obligation, case_event | AC-14–16,53,64 |
| FR-34 | Adjudication / B-06 | disputes/{id}/decisions | dispute/task/deadline | AC-16,52,53 |
| FR-35 | Clocks / B-06/11 | case clocks, deadline worker | clock_policy/deadline/event | AC-14,52 |
| FR-36 | Verification / B-06/07 | claims/verification-decisions | verification_decision/evidence | AC-15,54 |
| FR-37 | Receipt publication / B-07 | publications + receipts | decision/binding/safe events | AC-08,26,56 |
| FR-38 | Add/challenge observation / B-05/06 | receipt observations | report + case_observation | AC-16,54,55 |
| FR-39 | Moderation/appeal / B-09 | content-reports, moderation/appeals | typed target/immutable decisions | AC-04,11,13,38 |
| FR-40 | Integration status / B-11 | integrations/{partner}/events | inbox/delivery/outbox | AC-19,40,57 |
| FR-41 | Agency queue/export / B-06/11 | authority queues/exports | grants/export_job/audit | AC-21,39,51 |
| FR-42 | Preferences / B-08 | me/preferences, blocks/mutes | feed_preference/profile_block/mute | AC-23,36,47 |
| FR-43 | Shared navigation / B-10 | canonical ID reads | client navigation + current policy | AC-24,35,58 |
| FR-44 | Data/account settings / B-01/11 | own exports/account-closure | deletion_task/hold ledger/audit | AC-25,28,59 |
| FR-45 | Language selector / B-05/13 | capabilities | language_capability/versioned packs | AC-24,37,49,50,63 |
| FR-46 | Quote authoring / P1 | gated source-reference post | post/revision/source policy | AC-09,66 |
| FR-47 | Playbook authoring / P1 | gated structured revision contract | playbook_revision | AC-67 |
| FR-48 | Private communities / P1 | gated invitation/member contract | community/member + scoped projections | AC-10,66 |
| FR-49 | Language/locality expansion / P1 | capability manifests/search/routes | new packs/releases/evaluation | AC-18,49,50,63 |
| FR-50 | DM / P2 | disabled until separate contract | planned separate conversation module | AC-68 |
| FR-51 | Live media / P2 | disabled until separate contract | planned bounded media/session module | AC-68 |
| FR-52 | Prediction / P2 | disabled until separate contract | planned asset/evaluation provenance | AC-68 |
| FR-53 | Private help / B-12 conditional | separate protected service/session | separate protected store/vault/grants | AC-11–13,27,62 |

`duplicate suggestions` is the scoped `POST /v1/service-report-suggestions` contract added in backend; no private-case title is returned. Later-release interfaces in this map are planning boundaries, not pilot endpoints.

## Business and nonfunctional coverage

| Business requirement | Functional requirements / NFRs | Evidence |
|---|---|---|
| BR-01 | FR-16–20,26,31; NFR-08 | AC-20,55 + reporter-effort cohort |
| BR-02 | FR-32–36; NFR-08 | AC-14,16,52,53 + ownership-time cohort |
| BR-03 | FR-47 P1 | AC-67 + useful playbook interviews |
| BR-04 | FR-33,34,41; NFR-01,09 | signed staffed roster, AC-51,61 |
| BR-05 | FR-17,19,20,37,38 | AC-15,56 + community-to-case demonstration |
| BR-06 | FR-19,24,39,53; NFR-11,14 | AC-11–13,26,27,56,62 |
| BR-07 | FR-21,32; NFR-05 | AC-22,42 + urgency stratified report |
| BR-08 | FR-27–30,42,45,49; NFR-07,12 | AC-24,29–37,49,50,63 |
| BR-09 | FR-25,27–30; NFR-10,12 | AC-29–34,48 + measured correction burden |
| BR-10 | FR-03–15,21–24,47 | AC-03–10,35,36 + usefulness research |
| BR-11 | FR-41,44; NFR-11 | AC-51,59 + export format review |
| BR-12 | FR-34,35,39,40; NFR-01,10 | roster/absence drill, AC-52,57,61 |
| BR-13 | FR-35,37,40,44; NFR-08,09,13 | AC-19,25,28,40,41,60 |

NFR coverage: NFR-01–06 -> AC-08,35,42,61 and percentile/purge measurements; NFR-07 -> AC-24,36,50; NFR-08 -> AC-01–07,14–21,38–41,55; NFR-09 -> AC-25,61; NFR-10 -> AC-34,48,61; NFR-11 -> AC-10–13,21,27,39,51,56; NFR-12 -> AC-29–34,37,49,50,63; NFR-13 -> AC-60; NFR-14 -> AC-62. Each release evidence record includes actual environment, fixture/version, actor roles, observations, date, reviewer and defect links.

## Deployment inputs and release gates

| Required input | Development fixture | Production evidence / owner |
|---|---|---|
| Pilot geography/taxonomy | synthetic boundary with ambiguous point | approved sourced release; jurisdiction lead |
| Agency roster/mandate | two synthetic agency accounts | signed scope, authority documents, contacts/absence cover; program lead |
| Clocks/calendars | labelled synthetic clock policy | signed anchors, duration, pause/holiday rules; adjudication owner |
| Identity/session | local OIDC test issuer | approved issuer/audience/MFA/revocation; security lead |
| Language capabilities | all catalog PLANNED; synthetic en/hi fixtures | per-capability reviewed packs/evaluations; language lead |
| OCR/vision/voice | stub explicitly returns unavailable | licensed model/provider + versioned evaluation; ML lead |
| Media/retention | private local S3-compatible bucket | store class/purge/hold/backup policy; privacy/media owners |
| Staff capacity | synthetic assigned queues | funded roster, response procedure and appeals independence; trust lead |
| Agency integration | signed synthetic callbacks + lost response | partner mapping, credentials/rate limit/reconciliation; integration lead |
| Cloud/secrets | local development-only configuration | pinned infrastructure, private networking, secret rotation; infrastructure lead |
| Legal/publication | synthetic non-sensitive text | approved enabled categories/status labels/terms; legal and publication owners |

Gate 0: contract review and synthetic vertical slices. Gate 1: social pilot may write only after moderation/appeal and revocation pass. Gate 2: operational pilot may accept real public-service reports only with signed jurisdiction/agency/staff/data controls. Gate 3: enable each language/model capability independently when evaluated. Gate 4: widen locality/community only with capacity and outcome evidence. Protected intake has an independent legal/safeguarding/security gate; P1/P2 require their own contract and test extensions.

Definition of done for software packages: running UI/backend on supported devices, all mapped enabled-feature tests pass, generated contracts match deployed API, migrations/authorization enforced, accessibility/manual alternatives verified, metrics/alerts/runbooks staffed, no critical unresolved defect, rollback/recovery rehearsed. Documentation completion alone satisfies none of these launch gates.


## UX scenario catalog

UX-01–37 preserve the original experience requirements. This is their canonical catalog; applicable acceptance tests are listed above. Source-level reference limitations remain in the [reference research](../REFERENCE_RESEARCH.md).

| Scenario | Trigger | Required result |
|---|---|---|
| UX-01 | Signed-out visitor opens a public post | Can read; write action offers sign-in and restores context |
| UX-02 | No location permission or GPS | Manual locality/landmark selection remains usable |
| UX-03 | New resident has no follows | Useful community choices and eligible local content; no fabricated activity |
| UX-04 | Dense feed on a small screen | Readable text, reachable actions, no horizontal page overflow |
| UX-05 | Dark theme, large text, reduced motion | Complete core tasks with readable labels and visible focus |
| UX-06 | Return from a thread/search result | Restore query/filter and a stable visible feed anchor |
| UX-07 | New posts arrive while reading | Explicit new-updates affordance; current item stays in place |
| UX-08 | Vote/bookmark/follow fails | Roll back pending state with a clear retry; authoritative state remains correct |
| UX-09 | Double-tap submit or lost success response | One committed post/report for the same submission key |
| UX-10 | Upload succeeds but submission fails | Reuse uploaded media and preserve draft on retry |
| UX-11 | Connection lost during ordinary report | Clearly labeled unsent local draft; recover on reconnect |
| UX-12 | Two devices edit the same draft | Explain conflict and preserve local text; no silent overwrite |
| UX-13 | Parent comment is deleted | Replies remain navigable under a tombstone |
| UX-14 | Deep or very long thread | Bounded indentation, child pagination, stable reply context |
| UX-15 | Account is blocked during an interaction | Subsequent interaction rejected; feed state reconciles |
| UX-16 | Referenced post is removed | Repost/quote/source preview loses removed content on authorized refresh |
| UX-17 | Public edit awaits review | Author sees pending edit; readers see the approved revision |
| UX-18 | OCR misreads or mixes scripts | User can correct selected text before applying/submitting |
| UX-19 | Blurred/night/glare/rotated image | Quality warning or evaluated preprocessing; manual reporting remains available |
| UX-20 | Image has several defects or no known defect | Multi-candidate or no-result state; no forced category |
| UX-21 | OCR text contains instructions to the model | No permission change, remote fetch, agency action, or case-status mutation |
| UX-22 | Analysis finishes for replaced media | Findings remain tied to original revision and cannot overwrite replacement |
| UX-23 | OCR succeeds but vision times out | Completed text available; targeted retry/manual description |
| UX-24 | Evidence contains faces, plates, or contact details | Public preview uses reviewed redaction; private original is scope-restricted |
| UX-25 | Similar report is a separate asset or recurrence | Resident can reject suggestion; no automatic semantic merge |
| UX-26 | Image suggests a category but location is uncertain | Ask for location confirmation; do not invent jurisdiction |
| UX-27 | Two agencies dispute ownership | Preserve case age, separate obligations, next review time, no resubmission |
| UX-28 | Agency claims completion | Show completion claim/verification pending until authorized verification |
| UX-29 | Before/after images differ in angle or asset | Flag correspondence/coverage uncertainty; no automatic restoration |
| UX-30 | Old closure is challenged with new observation | Record observation and review task with preserved history |
| UX-31 | Staff permissions expire during a session | Subsequent scoped access fails and sensitive view is cleared |
| UX-32 | Notification waits while source is withdrawn | No withdrawn body or thumbnail delivered |
| UX-33 | Protected matter appears in a public draft | Withhold as policy requires and show reviewed safer-route option |
| UX-34 | Private or removed object searched directly | No unauthorized title, snippet, count, or existence disclosure |
| UX-35 | Public API unavailable or agency delivery delayed | Distinguish unsent, platform received, delivered, acknowledged, accepted |
| UX-36 | Popular routine discussion beside urgent case | Civic urgency retains its defined precedence in Unresolved |
| UX-37 | Speech recognition unavailable or audio unclear | Show unavailable/uncertain transcript, preserve consented input as policy permits, allow correction/manual text; never insert a predefined report |

## Documentation validation

Run the standard-library checker from the repository root:

```sh
python3 scripts/validate_specs.py
python3 scripts/validate_specs.py --extract-dir /tmp/jansetu-spec-validation
```

It checks repository-local links/heading anchors, complete JSON examples, requirement/scenario/test identifier coverage, and declared light/dark text/control color pairs. It extracts ordered reference SQL, separate vault SQL, Mermaid diagram text and complete Go packages for independent checks. It does not run acceptance tests against an application.

For SQL validation, create a disposable PostgreSQL 18 database with compatible PostGIS and citext/btree_gist, run `schema.sql` with `psql -v ON_ERROR_STOP=1`, and run `vault.sql` in a separate empty database. Check expected tables/FKs and drop only the disposable environment. Never run this reference DDL against an existing production database. Implementation migrations must add the explicitly required grants, RLS, triggers and lifecycle enforcement.

Parse every extracted diagram through Mermaid before publishing. Compile/format the complete Go request package in an isolated module. The earlier VoteService excerpt is an interface example with application-specific types; it is not independently deployable. Record actual tool versions and results in the validation evidence below.

### Validation evidence for this documentation release

Checks executed on 3 October 2026 for this documentation release:

| Check | Result and scope |
|---|---|
| Local document checker | Passed local links/anchors, GFM table column counts, eight JSON examples, BR/FR/NFR/UX/AC coverage and 30 declared theme contrast pairs |
| Mermaid 11.17.2 | All eight diagrams parsed successfully; syntax check, not a rendered visual review |
| PostgreSQL 18.6 + PostGIS 3.6.4, Linux arm64 | Ordered reference DDL executed in one transaction: 78 application tables and 129 foreign keys; five vault tables executed in a separate empty database |
| Go 1.26.0, Linux arm64 | Complete DecodeVote package compiled and passed go vet; gofmt produced no changes; application-specific VoteService excerpt remains a fragment |
| Git whitespace check | Passed `git diff --check` |

The SQL check establishes reference syntax/order/constraints, not production authorization or workflow correctness. Grants/RLS/triggers and application AC-01–68 remain implementation work. No live agency action, model-quality evaluation, application load test or production readiness is claimed.
