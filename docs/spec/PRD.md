# JanSetu AI — Product Requirements Document

Version: 3.0. Date: 3 October 2026. Status: canonical design for implementation.

Audience: Product, design, engineering, QA, moderation, and operational teams.

This document owns: Observable product behavior, release priorities, actor permissions, and functional requirements. Read the [suite guide](README.md) and [domain glossary](../../CONTEXT.md) first. Requirements describe intended behavior; application code, agency integrations, and model evaluation remain implementation work. P0 is the controlled pilot, P1 is the next validated release, and P2 is later expansion.


## Contents

- [Release boundaries](#release-boundaries)
- [Social features and precise behavior](#social-features-and-precise-behavior)
- [Civic workflow and protected boundaries](#civic-workflow-and-protected-boundaries)
- [Feed, search, and notifications](#feed-search-and-notifications)
- [Extended functional requirements](#extended-functional-requirements)
- [Canonical state and visibility rules](#canonical-state-and-visibility-rules)
- [Multilingual product contract](#multilingual-product-contract)
- [Nonfunctional requirements](#nonfunctional-requirements)

## Release boundaries

### Priority definitions

P0 means required for the controlled public-service pilot. P1 means the next validated release after pilot evidence. P2 means growth or intelligence work with its own business case.

| Capability | Priority | Release decision |
|---|---:|---|
| Responsive website and Android/iOS app | P0 | Shared contracts and visual tokens; separate web/native presentation |
| Chosen-name profile, follows, mute, block | P0 | No contact-book upload |
| Public and restricted-posting communities | P0 | Platform approves community creation |
| Short posts, discussions, questions, text comments | P0 | One primary community per post |
| Votes, bookmarks, reposts | P0 | No paid boosts or public voter lists |
| Public-service intake and case follow | P0 | One supported pilot jurisdiction |
| Unresolved, Following, For You, and Resolved views | P0 | Explainable ranking and finite sessions |
| Authority workspace and worker task view | P0 | Contract-backed responsibilities only |
| Moderation, appeals, publication redaction | P0 | Must ship with social writing |
| Protected intake using real reports | Conditional P0 | Separate readiness decision; synthetic demonstration otherwise |
| Quote posts, playbooks, richer multilingual search | P1 | Enable after abuse and indexing tests |
| Invite-only private communities | P1 | No use as a substitute for protected reporting |
| Live audio, livestreaming, direct messages | P2 | Separate safety and cost justification |
| Predictive infrastructure maintenance | P2 | Requires validated longitudinal asset records |
| Political ads, suspect databases, predictive policing | Excluded | Outside product purpose |

The reference schema anticipates selected P1 features, but the API feature gates disable them until release requirements pass.

### Actor permissions

Permissions combine role, organization, community, case assignment, purpose, and publication state. A global role alone is insufficient.

| Actor | Social content | Operational case | Identity or protected evidence |
|---|---|---|---|
| Visitor | Read eligible public content | Read published receipt | None |
| Resident | Publish, reply, vote, follow, manage own drafts | Submit observations; view own authorized receipt | Own authorized material through a separate service |
| Community moderator | Moderate their community and its threads | Read public receipt | None |
| Platform moderator | Review public-content abuse and appeals | Read public receipt | No identity-vault access |
| Agency officer | Publish affiliated updates within mandate | Assigned obligations and approved evidence subset | Only explicit, purpose-bound grant if permitted |
| Field worker | Ordinary social access separately | Assigned task and minimum necessary evidence | No general reporter lookup |
| Case coordinator | Ordinary social access separately | Route, coordinate, request adjudication | Limited operational access |
| Protected specialist | No automatic social powers | Granted protected matters | Time-bound, audited access |
| Legal reviewer | No general content ownership | Approved disclosure or preservation workflow | Only the authorized scope |
| Infrastructure administrator | Operate systems | No routine content inspection | No routine decryption authority |

Organization affiliation is verified separately from social reputation. An officer leaving an agency loses organization-scoped access without deleting the agency's historical actions.

## Social features and precise behavior

### Accounts, profiles, and communities

FR-01: Sign in using an OIDC provider. Web sessions use secure, HttpOnly cookies through a backend-for-frontend. Native apps use authorization code with PKCE and platform secure storage. Staff require MFA. No password database is built inside the social module.

FR-02: Public profiles expose a random public ID, chosen handle, display name, biography, optional avatar, and public contributions. Phone, email, login identifier, exact home location, private follows, and protected reports never appear in profile DTOs.

FR-03: A community has a slug, title, description, language, geographic or topic scope, rules revision, posting policy, moderators, and appeal route. A geographic community links to an administrative unit; it does not establish legal service ownership.

FR-04: Membership is distinct from following. Joining permits participation under the community rules; following controls feed subscriptions. Public communities allow eligible members to post. Restricted communities require an approved contributor role. Bans include reason, duration, decision maker, and appeal.

FR-05: Ordinary users request new communities. Staff review duplicate scope, name impersonation, steward capacity, and abuse risks before approval. Merging communities preserves old links and post origins.

### Post types and limits

These are application defaults and must be configuration-controlled.

| Type | Required fields | Initial limits | Special behavior |
|---|---|---|---|
| Short update | Body; optional community | 1,000 Unicode characters | Suitable for observations and quick updates |
| Discussion | Title, body, community | Title 180; body 8,000 characters | Threaded replies |
| Question | Title, body, community | Same as discussion | Author can select “Helpful response”; never “verified fact” |
| Playbook | Title, steps, scope, revision | 8,000 characters plus structured steps | Last-reviewed date and success conditions |
| Quote post | Body and source post ID | 1,000 characters | Live reference; no copied source body |
| Case update | Published receipt reference and approved summary | 1,000 characters | Created by publication workflow, not ordinary user input |

Permit up to four images in a pilot post, within [upload limits](BACKEND_IMPLEMENTATION.md#shared-types-limits-and-errors). Audio is for voice intake. Short video is a later release requiring moderation/transcoding tests. Alternative text is supported. Raw HTML, scripts, embedded trackers, executable attachments, and arbitrary iframe embeds are disallowed.

A draft contains the author's current revision. Publishing passes that revision through checks. Editing a published post creates a new pending revision; the previous approved revision remains visible until the new revision is approved. A removal decision overrides both. The author sees “Edit awaiting review” rather than a false indication that everyone sees the new text.

### Comments and voting

FR-06: Comments use an adjacency tree with an immutable parent. The API returns pages of top-level comments and separately paginated replies. Maximum nesting depth is 20; visual indentation stops at two levels on mobile and three on desktop, with a thread view for deeper replies.

FR-07: Comment sorts are Newest, Oldest, and Helpful. Helpful uses a bounded vote signal with age and anti-abuse controls. Official status changes appear in the case timeline, not pinned because of comment popularity.

FR-08: Votes represent “useful contribution” and “not useful here.” Each account has one current vote per eligible post or comment: +1, -1, or no vote. A new value replaces the prior value. Self-votes are rejected. Removing a vote is idempotent.

FR-09: Case receipts and generated case-update posts have no vote score. Users can follow a case or submit an independent observation. A discussion about a case may receive votes, but the operational case priority ignores them. The vote policy rejects generated case-update targets.

FR-10: Do not publish voter identities. Aggregate scores may lag briefly; the current user's vote must reflect the committed value. Raw vote history is unavailable to community moderators. Abuse review uses restricted metadata with a retention policy.

### Sharing, following, blocking, and deletion

FR-11: A repost is a reference to an original post. One active repost per user and source is allowed. Undo removes the reference. The feed groups repeated reposts of the same original in a session.

FR-12: A quote post requires an eligible public source. Private-community content cannot be quoted outside that community. Removed sources render a neutral unavailable card. The quoting author's own text is evaluated separately; JanSetu cannot retract screenshots already copied outside the service.

FR-13: A block prevents new follows, replies, mentions, votes, and reposts between the two accounts, hides their content from each other's personalized views, and revokes the existing follow relationship in both directions. Public posts can still be seen while logged out; the UI states this limit. Mute only affects the muting user's feed.

FR-14: Case receipts remain accessible independently of interpersonal blocks. If a blocked official issued a relevant agency update, the receipt shows the institutional status without promoting the official's personal post.

FR-15: Deleting an author's post removes it from discovery and replaces thread context with a tombstone. Other people's comments do not automatically disappear unless their visibility or safety requires it. Retained moderation or operational evidence follows a separate documented policy; deletion is never represented as guaranteed erasure from third-party devices.

### Reputation and contribution

Avoid a universal civic “trust score.” Display specific, explainable attributes: account affiliation, community role, playbook review date, and counts of helpful contributions where safe. A popular account gets no extra right to identify a reporter, decide truth, or bypass review.

Do not reward the number of abuse reports filed, suffering witnessed, or allegations amplified. Recognition can acknowledge a reviewed playbook, accessible translation, or completed community maintenance activity.

## Civic workflow and protected boundaries

### Turning discussion into action

FR-16: “Report a service issue” is separate from “Create a post.” The former captures observation time, approximate or exact operational location, category, source media, and safe contact preference. The latter creates a social statement.

FR-17: When a post appears to describe an actionable problem, the author may select “Create a case from this.” The UI requests any missing operational facts and presents a separate publication choice. No background process silently registers an allegation or republishes evidence.

FR-18: Duplicate suggestions show public-eligible cases nearby with a clear “same issue / different issue / unsure” decision. Joining an existing case creates a new observation, retaining source and timestamp. A human or approved deterministic rule confirms operational merges; an embedding match alone never merges cases.

FR-19: Anonymous public case authorship is the default. A resident can separately choose to post publicly about their experience. The public receipt does not expose a reporter profile, internal report ID, identity-vault reference, or raw media location.

FR-20: Acknowledgements are distinct: **received by JanSetu**, **delivered to agency**, **acknowledged by agency**, and **obligation accepted**. Each requires its own recorded event. A successful HTTP request to JanSetu proves only platform receipt.

### Responsibility, closure, and disagreement

The operational record preserves the original case age. A transfer changes obligations and coordination; it never requires reporter resubmission. Partial acceptance is supported: a roads team can barricade a hazard while a utility investigates a pipe.

The existing adjudication supplement governs disputed handoffs. Implementation stores separate clocks for case age, agency acknowledgement, dispute evidence, adjudication, and accepted service obligations. Before pilot signature, the parties must resolve whether each deadline uses elapsed or working time and how deadlines begin for a dispute raised after the original case's first days. Software must not guess.

“Work completed” is an agency claim. “Verification pending” follows receipt of required evidence. “Verified restored” requires the configured reviewer and evidence rules. A resident can challenge closure; the system records the challenge without automatically accusing an official of dishonesty.

### Protected reports

Protected reports have no social post, searchable title, case-follow recommendation, public map pin, share preview, hashtag, or public existence indicator. A private community is not an adequate protection boundary.

A resident mentioning abuse or intimidation in a public draft receives a discreet safer-route option. The draft is withheld when publication risk is identified. The system does not silently forward it to an allegedly implicated authority. A trained reviewer handles ambiguous cases under an approved procedure.

Real protected intake uses an independently authorized service, separate credentials, encrypted evidence storage, and purpose-bound access. It has no feed-analytics integration. Product notices must explain the limits of confidentiality, mandatory processes where applicable, and safe contact choices approved by counsel. This blueprint leaves legal questions open in [readiness requirements](TRACEABILITY_AND_DELIVERY.md#pilot-legal-and-governance-readiness).

## Feed, search, and notifications

### Feed modes

| Mode | Candidate set | Ordering | User control |
|---|---|---|---|
| Unresolved | Public receipts in selected localities and service categories | Validated urgency tier, then need score | Locality, category, oldest, needs ownership, verification pending |
| Following | Eligible posts and updates from followed profiles, communities, and cases | Reverse chronological | Mute, unfollow, show reposts |
| For You | Explicit interests, joined communities, locality, and optional public interaction signals | Explainable relevance and diversity | Why shown, reset, personalization off |
| Nearby | Eligible public issues and discussions within chosen coarse area | Local relevance or recent | Manual area selection; location permission optional |
| Resolved | Published, verified restoration updates | Recent verification with recurrence context | Service category and locality |

Default new users to a locality-based, non-behavioral feed. If no locality is selected, show a community chooser and useful public playbooks. Never require continuous GPS permission.

### “Top unresolved problems” algorithm

First exclude protected cases, unpublished receipts, expired safety alerts, and cases outside the chosen service scope. Apply content visibility checks before ranking.

Order public cases lexicographically by:

1. operational urgency tier, validated by configured rules or an authorized reviewer;
2. the need score within that tier;
3. oldest first-report time, then stable receipt ID.

Proposed pilot score, with every component normalized to 0–1:

`need = 0.30 × serviceImpact + 0.25 × overdue + 0.20 × unowned + 0.15 × age + 0.10 × evidenceFreshness`

Service impact measures documented loss of access, affected assets, and duration. Overdue is calculated from the applicable service rule; no agreed deadline means “deadline unavailable,” not zero failure. Unowned indicates missing accepted responsibility. Age saturates by category so very old cases do not permanently dominate. Evidence freshness is bounded so a valid old report is not dismissed for lacking new photos.

For missing components, use a published category-specific fallback and attach an explanation flag. Do not reweight silently. No votes, reposts, author followers, outrage score, payment, caste, religion, inferred vulnerability, or protected-report data enter this score.

Within the same urgency tier, optional ordering can reflect the user's chosen area and categories. A lower-severity case never overtakes a higher-severity one merely because it matches personal interests.

### For You ranking and session behavior

Use rule-based ranking first:

`relevance = 0.35 × explicitInterest + 0.25 × locality + 0.20 × relationship + 0.10 × freshness + 0.10 × boundedUsefulness`

The weights are hypotheses for pilot evaluation. Count no more than two posts by one author in a 20-item page, except where doing so would hide a directly requested case update. Collapse duplicate case cards. Reserve an initial experiment allocation of at least 30% of eligible slots for unresolved local cases when enough candidates exist; show a labeled section rather than pretending this is purely personal relevance.

Use 20-item pages, an explicit “Load more,” no graphic autoplay, and a “You are caught up” state. Offer a weekly digest. Measure usefulness, constructive contribution, and informed follow-up instead of maximizing time spent.

Store at most the limited public interaction events needed for an approved personalization experiment. Disable behavioral personalization by default for unknown-age users and for any child account supported later. Never infer political, religious, caste, health, sexual, or abuse-victim attributes.

### Search

Search published posts, public profiles, communities, playbooks, and safe receipt summaries. Search results respect blocks, publication revocation, community membership, locale, and removed accounts. Apply the same checks to suggestions, counts, trending terms, and export endpoints.

The pilot uses PostgreSQL search projections plus explicit category, language, locality, and status filters. Language-specific tokenization quality must be evaluated; default English stemming is insufficient for Indian-language search. Begin with exact and trigram fallback where appropriate, and add an evaluated search engine when relevance requires it.

### Notifications

| Event | Default delivery | Constraints |
|---|---|---|
| Reply or mention | In-app; optional push | Block and mute checks at enqueue and send time |
| Followed case makes progress | In-app; opt-in push | Meaningful status changes, not every internal event |
| Deadline missed | Digest for residents; operational alert for owner | Separate public explanation from internal escalation |
| Community announcement | In-app digest | Moderator quota and unsubscribe |
| Login or security change | Security channel | No case details |
| Protected follow-up | Only approved safe-contact method | No category, location, or allegation in push preview |

The server records channel consent and quiet hours. Case follow does not automatically consent to email or push. Notification text is rendered at send time from current authorized projections, not copied permanently from a possibly revoked post.

## Extended functional requirements

FR-01–20 above retain their identifiers. The following requirements complete the pilot and define later delivery. Their acceptance evidence is mapped in [traceability](TRACEABILITY_AND_DELIVERY.md).

| ID | Phase | Capability and observable behavior |
|---|---|---|
| FR-21 | P0 | Home exposes Following, For You, Unresolved, Nearby, and Resolved with mode-specific filters and clear empty states |
| FR-22 | P0 | Search filters eligible posts, profiles, communities and public receipts by applicable type, locality, category, language and status; playbooks join search in P1 |
| FR-23 | P0 | In-app activity separates social events, followed-case progress and account security; mark-read changes presentation only |
| FR-24 | P0 | Notification delivery honors channel consent, quiet hours, blocks, removal and permission changes at send time |
| FR-25 | P0 | Upload sessions verify size/type/hash, quarantine originals, and attach only approved derivatives to public content |
| FR-26 | P0 | Ordinary drafts survive supported offline interruptions with an explicit Not sent state and an owner-controlled removal action |
| FR-27 | P0 | OCR exposes regions, text and uncertain spans; residents correct or reject suggestions before applying them |
| FR-28 | P0 | Image recognition offers supported category/region candidates and useful no-result or partial-result states |
| FR-29 | P0 | Voice input provides an editable transcript or clear failure; unavailable recognition never inserts invented text |
| FR-30 | P0 | Analysis jobs bind to immutable media revisions and independent tasks; stale/deleted/revoked inputs cannot update the current draft |
| FR-31 | P0 | My reports lists the resident's authorized submissions, pending information requests, and distinct delivery/acceptance progress |
| FR-32 | P0 | Coordinators create proposed obligations from source-backed routing records; ambiguity produces a review task |
| FR-33 | P0 | Agencies accept, partially accept, dispute, request clarification, and record work updates within their assignment scope |
| FR-34 | P0 | Disputes preserve case age, independent obligations and interim safety work; adjudication records evidence, owner and next deadline |
| FR-35 | P0 | Deadline processing uses recorded start events and versioned calendars; missed clocks generate scoped alerts without closing work |
| FR-36 | P0 | Completion claims remain distinct from verification; outcomes require the configured evidence/reviewer decision |
| FR-37 | P0 | Public receipt publication, correction and withdrawal are independently reviewed and versioned |
| FR-38 | P0 | Residents add supporting/challenging observations; same/different/unsure duplicate decisions never silently merge reports |
| FR-39 | P0 | Moderators act only on their content/community scope, recording reasons and an independent appeal route |
| FR-40 | P0 | Agency callbacks are authenticated and replay-safe; uncertain external delivery is reconciled before resend where supported |
| FR-41 | P0 | Organization staff receive scoped exports and queues; affiliation revocation removes future access without erasing history |
| FR-42 | P0 | Theme, density, language, locality, blocked/muted items and personalization controls have inspectable, reversible state |
| FR-43 | P0 | Web and native use canonical links and shared API contracts; link opening rechecks current permission |
| FR-44 | P0 | Settings support data request/account closure under documented retention/hold rules; ordinary discovery is removed when applicable |
| FR-45 | P0 | A language catalog advertises separate UI/search/voice/OCR capability readiness; unsupported inference retains manual text reporting |
| FR-46 | P1 | Quote posts reference eligible public sources; source removal suppresses quoted previews and triggers appropriate review |
| FR-47 | P1 | Residents draft playbooks with steps, applicability and review date; ordinary playbook authoring is feature-gated |
| FR-48 | P1 | Private communities use membership-aware discovery, counts and content access; they cannot substitute for protected intake |
| FR-49 | P1 | Expanded multilingual search and locality rollout pass their own relevance, authorization and operating-capacity checks |
| FR-50 | P2 | Direct messaging requires consent, block/report controls, retention policy and staffed abuse handling before enablement |
| FR-51 | P2 | Live rooms/video require accessibility, media moderation, consent and capacity requirements before enablement |
| FR-52 | P2 | Predictive maintenance remains a suggestion backed by evaluated longitudinal asset data; it cannot fabricate events or obligations |
| FR-53 | Conditional | Protected intake uses a separate specialist contract and remains absent from social feeds, search, profiles, analytics and public existence signals |

### Representative journeys

| Journey | Entry and steps | Completion |
|---|---|---|
| Read and join | Public home → select locality/language → community → rules → sign-in when joining | Membership and follow are displayed independently |
| Discuss | Community → question/discussion composer → review → thread → reply/helpful response | Approved content visible; current viewer actions accurate |
| Photo report | Create → service report → upload → correct OCR/category → location/time → similar cases → sharing review → submit | One platform receipt with honest delivery state |
| Follow progress | Public receipt → follow → activity → timeline → evidence | Meaningful authorized update; no raw reporter linkage |
| Act on work | Assigned queue → inspect obligation → accept/partial/dispute → update/evidence → completion claim | Attributed action with preserved case age and next deadline |
| Challenge closure | Public receipt/My reports → new observation → review task | Challenge recorded; previous decision retained until re-reviewed |
| Appeal moderation | Decision notice → grounds → assigned independent reviewer → outcome | Versioned decision and understandable explanation |

## Canonical state and visibility rules

| Object | States | Interpretation |
|---|---|---|
| Profile | ACTIVE, SUSPENDED, DEACTIVATED | Participation permission depends on current state |
| Community | ACTIVE, FROZEN, ARCHIVED | Frozen blocks new participation; archived remains readable where eligible |
| Post | DRAFT, PENDING, PUBLISHED, HIDDEN, DELETED | PENDING applies before first publication; existing approved content stays PUBLISHED while an edit is reviewed |
| Post/comment revision | PENDING, APPROVED, REJECTED | Approval binds to exact content/media/policy version |
| Comment | PENDING, PUBLISHED, HIDDEN, DELETED | Pending edit does not replace an approved public body or hide an otherwise eligible thread |
| Case | OPEN, ACTIVE, VERIFICATION_PENDING, RESOLVED, REOPENED, REFERRED, WITHDRAWN | A case reflects required obligations collectively, not a single agency claim |
| Obligation | PROPOSED, DELIVERED, ACKNOWLEDGED, ACCEPTED, DISPUTED, IN_PROGRESS, COMPLETION_CLAIMED, VERIFIED, CANCELLED | Delivery/acknowledgement/acceptance are independent facts; partial acceptance splits scope |
| Public receipt | PUBLISHED, WITHDRAWN | Safe public state uses approved case labels; source case IDs never appear |
| Analysis task | QUEUED, RUNNING, SUCCEEDED, PARTIAL, FAILED, CANCELLED | Task states are separate from publication and operational case states |

Ordering urgency uses four tiers: 0 ROUTINE, 1 HIGH, 2 URGENT, 3 CRITICAL; larger tiers precede smaller tiers in Unresolved. An authorized rule/reviewer establishes the tier. Missing or disputed urgency enters review; neither vote count nor model confidence alone assigns it.

Published post edits retain the approved revision until the new candidate is approved. Published comment edits follow the same behavior. Hidden/deleted status overrides every revision, cache, quote/repost preview and delayed approval. A blocked official's institutional case receipt remains available independently of their personal social content.

### Case closure and disagreement

Only the case workflow changes operational state. A case becomes RESOLVED when every required restoration obligation is VERIFIED and no outstanding blocking verification/dispute remains. Approved cancelled obligations must have a reason and replacement/referral disposition. An agency may record partial completion without resolving the whole case. Administrative withdrawal and external referral have their own public labels and never count as verified restoration.

Reopening requires an authorized review decision. A resident's new observation creates that review request; AI or a social vote cannot reopen automatically. Recurrence after restoration is evaluated as a new incident or linked follow-up using category/asset/time evidence, preserving both histories.

### Clocks and calendars

Case age always starts at the first valid report. Assignment response starts at recorded delivery of the proposed obligation; dispute evidence/adjudication clocks start at the recorded dispute; accepted service clocks start at acceptance. Previously applicable case-level progress targets remain recorded. A later handoff does not restart case age or erase earlier breaches. Existing deadlines change only through an attributed decision with reason and calendar/version reference.

The old companion's T+4/24/48/72-hour and day-based timeline is a synthetic pilot schedule for illustration, not a universal service promise. The deployment must load the participating category's signed policy; a missing policy produces deadline unavailable and a coordinator configuration task. Safety/emergency windows are a separately approved policy.

## Multilingual product contract

Target Indian languages through an extensible BCP 47 catalog. The initial catalog includes English and the scheduled languages as coverage targets; additional languages, scripts, dialect labels and transliteration modes can be added without changing core report types. The [government language list](https://rajbhasha.gov.in/en/languages-included-eighth-schedule-indian-constitution) supplies the initial language inventory, not a claim about model or interface readiness.

Each locale/capability has PLANNED, EVALUATING, READY or PAUSED status, reviewer, evidence reference and version. The capability set is UI, SEARCH, VOICE_TRANSCRIPTION, OCR and CONTENT_TRANSLATION. Public availability labels describe these capabilities separately. Translation does not replace the original statement or official event wording.

Residents can submit supported Unicode text even when voice/OCR for their language is unavailable. UI falls back to a reviewed installed locale and explains the unavailable capability. Initial synthetic fixtures use English/Hindi examples solely for repeatable testing; live language enablement is chosen from the pilot's population and verified packs.

User-supplied text preserves its original bytes apart from documented transport normalization; derived search normalization is stored separately. Mixed-script text, RTL scripts, long translated labels, names, numerals and locale date formatting receive explicit tests. Locale is an explicit preference, never inferred as ethnicity or a sensitive profile trait.

## Nonfunctional requirements

| ID | Requirement | Acceptance boundary |
|---|---|---|
| NFR-01 | Public read API availability target 99.9% monthly | Eligible requests at edge; report downtime and exclusions |
| NFR-02 | Feed processing p95 under 500 ms | Declared server workload; media and client network measured separately |
| NFR-03 | Social commit p95 under 700 ms | Authoritative database commit acknowledgement |
| NFR-04 | First usable feed p75 under 2.5 seconds | Named pilot device, dataset and mobile-network profile |
| NFR-05 | Ordinary projection p95 under 10 seconds | Commit to eligible projection availability |
| NFR-06 | Revocation denies new origin reads synchronously; edge purge target 60 seconds | Origin plus managed copies; previous downloads outside recall scope |
| NFR-07 | Core tasks satisfy WCAG 2.2 AA and tested native accessibility | Keyboard, screen reader, text zoom, contrast, focus, motion and local scripts |
| NFR-08 | Writes preserve authorization, idempotency and consistent locking | Concurrent/retry/revocation scenarios; no duplicate committed effects |
| NFR-09 | Recovery targets RTO 60 minutes, RPO 15 minutes | Timed restore with current grants, clocks and deletion ledger |
| NFR-10 | Quotas and bounded resource use apply to media, queries and AI | Oversized/malformed input, runaway job and provider outage checks |
| NFR-11 | Operational/identity/protected data remains purpose-scoped | Cross-role/resource/export/search/media authorization checks |
| NFR-12 | Language and model capabilities require versioned evidence | Per-language/task evaluation and successful manual alternatives |
| NFR-13 | Releases preserve schema/client compatibility | Migration upgrade/rollback-window and generated-client contract checks |
| NFR-14 | Logs and analytics avoid raw statements, evidence and secrets | Redaction/telemetry fixtures and scoped access review |

These are initial acceptance targets. [System design](SYSTEM_DESIGN.md) owns workload and operational assumptions; [delivery](TRACEABILITY_AND_DELIVERY.md) owns evidence and go/no-go reporting.
