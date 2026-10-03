# JanSetu AI — Business Requirements Document

Version: 3.0. Date: 3 October 2026. Status: canonical design for implementation.

Audience: Business owners, institutional partners, program managers, and product leads.

This document owns: Business purpose, stakeholders, economics, scope, adoption, and outcome definitions. Read the [suite guide](README.md) and [domain glossary](../../CONTEXT.md) first. Requirements describe intended behavior; application code, agency integrations, and model evaluation remain implementation work. P0 is the controlled pilot, P1 is the next validated release, and P2 is later expansion.


## Contents

- [Product decision and scope](#product-decision-and-scope)
- [Business problem and value](#business-problem-and-value)
- [Business scope and delivery commitments](#business-scope-and-delivery-commitments)

## Product decision and scope

Build JanSetu as a civic social network connected to an accountable case-management system. Residents participate through familiar communities, short posts, threaded discussions, follows, votes, reposts, and useful local feeds. A service problem can become a persistent case whose owner, obligations, evidence, and progress remain visible across departmental handoffs.

The social product brings people together. The operational product preserves responsibility. Neither a popular post nor a moderator's decision establishes that an allegation is true, an agency has accepted responsibility, or a repair has happened.

This version replaces the earlier master blueprint's general social-feed and implementation sections with explicit product behavior. The existing **JanSetu AI Blueprint v1.0 — Adjudication and Enforcement Design** remains the companion specification for disputed handoffs. Its pilot agreement must be adapted and signed; this document does not confer enforcement powers.

The [social experience, OCR, and image recognition plan](UI_IMPLEMENTATION.md) expands the user's Reddit + X direction into visual, screen, and scenario requirements. The [reference research](../REFERENCE_RESEARCH.md) traces the original CampusFix idea and four supplied AI projects to their source code. OCR means reading text from images; it does not select a cloud provider. These documents add implementation requirements, not evidence of completed software.

### What “like Reddit and X” means here

| Familiar interaction | JanSetu implementation | Civic constraint |
|---|---|---|
| Reddit communities | Locality and topic communities with rules, stewards, and discussions | Community boundaries do not determine statutory jurisdiction |
| Nested comments | Replies, sorting, moderation, and selected helpful responses | Comments cannot change case status |
| Voting | Upvote/downvote the usefulness of posts and comments | Votes do not verify reports or reduce an operational priority |
| X-style following | Follow residents, organizations, communities, and cases | A protected report never appears in the follower graph |
| Short updates | Quick observations and agency progress messages | An official badge verifies account affiliation, not every claim |
| Reposts and quote posts | Share an eligible public post or add context | Original visibility and removal decisions remain enforceable |
| Personalized feed | Explicit interests, locality, follows, and optional limited interaction signals | No targeting from protected reports or inferred sensitive traits |
| Trending issues | “Needs attention” using severity, elapsed time, and verified service impact | Virality and accusations do not determine urgency |
| Public profile | Chosen name, handle, biography, and public contributions | Real identity and reporter identity remain separate |
| Search | Public discussions, communities, playbooks, and published case receipts | Protected reports are absent, including from autocomplete |

These are interaction patterns, not claims about either platform's internal ranking algorithms. References S1–S2 explain the source products' public interaction conventions.

### Five objects that must remain distinct

| Object | Meaning | Owner of truth |
|---|---|---|
| Post | A public or community-scoped statement | Its author, subject to moderation |
| Report | A submitted observation and associated evidence | Intake record with source attribution |
| Case | A persistent operational problem receiving one or more reports | Authorized case workflow |
| Obligation | A specific action, accepted or disputed by a responsible organization | Agency and agreed adjudication process |
| Public receipt | A reviewed, sanitized projection of selected case facts | Publication service with versioned approval |

A post may discuss several cases. Several posts and reports may relate to one case. A report need not create a public post. Deleting a post does not delete a separately retained operational record. The interface explains that distinction before submission.

### Release assumptions

The first operational pilot covers one city or district, two or three participating service organizations, approximately ten locality communities, and a language rollout selected from the multilingual readiness catalog. Public participation is initially for adults; anonymous reading remains available. Age assurance and child-access rules require counsel and safeguarding review before launch.

Child abuse, violence against women, coercion, extortion, police misconduct, and corruption are included in the long-term product scope through a protected reporting service. They are not public community categories for naming alleged perpetrators. The pilot enables real protected intake only after the separate legal, staffing, referral, and security gates in [readiness requirements](TRACEABILITY_AND_DELIVERY.md#pilot-legal-and-governance-readiness) pass. Until then, the UI presents verified support options and clearly labels the intake demonstration as synthetic.

## Business problem and value

### The failures JanSetu addresses

Residents currently separate the work of reporting, finding the department, gaining attention, gathering neighbors, and proving that a response was inadequate. Agencies receive overlapping complaints without a reliable shared history. Public platforms reward visibility, but a popular thread rarely becomes a durable operational record.

The original social signals translate into concrete business requirements:

| Signal | Underlying failure | Business requirement |
|---|---|---|
| Viral commuter reporting | Accountability depends on exceptional personal effort | BR-01: An ordinary resident can create a durable public-service record |
| Departmental deflection | Each transfer loses continuity and time | BR-02: Responsibility disputes preserve case age and next action |
| Footpath repair playbook | Practical administrative knowledge stays inside individual stories | BR-03: Successful processes become reusable, versioned playbooks |
| Civic app abandonment | Participation outlives institutional attention | BR-04: Every participating category has a funded operational owner |
| Social-platform workarounds | Citizen attention is disconnected from agency workflow | BR-05: Public discussion links to auditable operational progress |
| Retaliation | Public visibility can reveal vulnerable reporters | BR-06: Publication and identity disclosure are separate decisions |
| Personalized unresolved feed | Popularity can bury severe, neglected problems | BR-07: Need and evidence determine attention before engagement |

### Stakeholders and incentives

| Stakeholder | Value received | Commitment required |
|---|---|---|
| Resident | Less coordination work; useful local information; progress tracking | Submit observations honestly and participate safely |
| Community steward | Tools to maintain useful discussion | Apply published rules and accept independent appeal |
| Agency coordinator | Fewer duplicate contacts; clear work queue | Accept, partially accept, or dispute obligations with reasons |
| Field worker | Actionable task and evidence requirements | Record work without claiming independent verification |
| District or municipal sponsor | Cross-department visibility and continuity | Maintain named adjudicators and escalation capacity |
| Safety partner | Controlled referrals with relevant information | Safe-contact procedures, staffed coverage, and accountability |
| JanSetu operator | Sustainable service revenue and reusable infrastructure | Operate moderation, security, reliability, and support |
| Independent reviewer | Reconstructable process records | Review retaliation, publication, and procedural complaints |

An agency's payment buys workflow and support. It does not buy a better feed rank, the suppression of criticism, or permission to close a case without the required evidence.

### Business objectives and proposed pilot measures

Targets below are proposed acceptance targets, not observed results or promises of government response. Establish a four-week baseline before using them in a contract.

| Metric | Exact definition | Proposed pilot target |
|---|---|---|
| Accepted ownership | Eligible cases with at least one accepted next-action obligation within the agreed window / eligible cases due in that window | At least 80% by pilot month three |
| Ownership time | Median and p90 elapsed time from first valid report to first accepted responsibility | Median improves at least 30% against baseline |
| Reporter effort | Number of additional contacts required from a reporter after initial submission | Median at most one |
| Verified restoration | Cases verified restored / cases whose applicable restoration target fell within the cohort period | Report by category; never mix with raw closure rate |
| Unsupported closure | Closure attempts rejected or reopened for inadequate evidence / all closure attempts | Downward trend after the baseline period |
| Community usefulness | Sampled sessions in which a resident found information, contributed evidence, or completed a chosen action | At least 70% positive interview responses |
| Publication safety | Confirmed exposure of protected material through public projections | Zero; any incident triggers containment |
| Retention after outcome | Eligible residents returning voluntarily within 30 days of a meaningful case update | Learn baseline; do not impose a screen-time target |

Report denominators, exclusions, severity, locality, and partner participation. A district with poor digital access must not appear to have fewer problems merely because it produces fewer posts.

### Commercial and institutional model

Offer an annual organization subscription for case coordination, integrations, support, and service analytics. Add explicitly priced onboarding, data maintenance, and specialist operations where needed. NGO or philanthropic sponsorship can fund assisted access and independent oversight.

Do not sell reporter identity, protected case data, behavioral targeting, or preferential treatment of cases. Public reading, ordinary reporting, and core participation should remain free. Provide contractually specified exports, including case history and evidence manifests, to avoid dependence through withheld records.

Use this contribution-margin model before pricing:

`margin = recurring revenue − hosting − media delivery − AI processing − moderation − case coordination − support`

Model public-social costs separately from agency workflow costs. Moderation and human coordination may exceed model inference costs. Budget them explicitly. Do not count volunteer labor as permanently free capacity.

### Launch operating model

Start communities only where stewards and agency contacts exist. Import authoritative service guides and a small number of consented, real cases; label synthetic examples as demonstrations. Invite local associations, accessibility groups, and field workers before paid acquisition.

One named program owner maintains agency agreements and service scope. One trust-and-safety lead owns publication and appeals. An engineering owner owns incident response and access controls. The launch decision requires all three, plus legal approval for any enabled protected category.

## Business scope and delivery commitments

| Phase | Business offer | Eligible audience | Evidence before expansion |
|---|---|---|---|
| P0 controlled pilot | Free public reading/participation and service reporting; staffed case coordination for contracted categories | Adults in one city/district; two or three participating agencies | Useful communities, accepted next actions, working review/recovery, sustainable operating cost |
| P1 validated expansion | More localities/languages, richer search, quote posts, citizen-authored playbooks, controlled private communities | Organizations with named stewards and working response arrangements | Quality and capacity hold by locality/language; new features pass their own acceptance conditions |
| P2 broader platform | Wider geography, approved live communication, predictive maintenance, deeper integrations | Regions/categories with sufficient data, staffing, and partner readiness | Explicit business case, measured demand, independent operating and model evidence |
| Protected reporting | Specialist intake and safe referral | Approved categories and staffed specialist pathways | Separate authorization, confidentiality/contact process, access controls, and operating agreement |

A neighborhood conversation does not need a case link. Residents may ask questions, exchange service guidance, and organize lawful community maintenance under community rules. Reporting a service problem does not force someone to publish a social post. Agency subscriptions purchase operational capability and support; ordinary reporting does not require a subscription.

### Stakeholder jobs and success definitions

| Stakeholder | Job to accomplish | Success | Failure to avoid |
|---|---|---|---|
| New resident | Find useful discussion and understand where to report | Chooses a locality/community and completes a relevant task without assistance | Empty launch community, mandatory GPS, unexplained permissions |
| Reporter | Explain an observation once and track the next action | Receives a durable platform receipt and understandable progress | Repeated resubmission during transfers; false agency acknowledgement |
| Neighbor | Contribute independent evidence or useful discussion | Contribution retains attribution and reaches the appropriate case/community | Votes treated as evidence; unrelated reports silently merged |
| Coordinator | Keep every eligible case moving | Clear next action, owner, due time, and recorded handoff | Unowned work hidden by transfer or closure |
| Agency supervisor | Allocate work and identify missed obligations | Scoped queue and reliable acceptance/dispute/completion history | Popularity ranking overriding operational urgency |
| Community steward | Maintain a useful discussion space | Scoped tools, documented decisions, workable appeals | Moderation authority mistaken for case decision authority |
| Program sponsor | Assess whether the pilot improves coordination | Outcome cohorts, denominators, category/language breakdowns | Claiming improvement from raw closure counts or unmeasured AI accuracy |

### Business requirement extensions

Existing BR-01–07 remain the outcome requirements in the problem table above. Add these delivery requirements:

| ID | Requirement | Owner | Acceptance evidence |
|---|---|---|---|
| BR-08 | Participation supports Indian languages through tested capability releases | Product and language operations | Published language/capability matrix, localized critical tasks, manual alternative where inference is unavailable |
| BR-09 | Media understanding reduces reporting effort without changing the reporter's statement silently | AI/product | Correction burden measured; original statement and accepted suggestions remain distinguishable |
| BR-10 | Social use remains valuable before and after a case outcome | Community/product | Residents complete reading, discussion, learning, and follow-up tasks in research sessions |
| BR-11 | Organizations can retrieve their scoped operational records in a documented format | Program/backend | Authorized export includes history and evidence manifest; access/deletion rules applied |
| BR-12 | Every enabled workflow has a named operating owner and capacity plan | Program/trust operations | Roster, working queues, absence cover, incident and handover procedures |
| BR-13 | Releases preserve service continuity and reconstructable decisions | Engineering | Compatible migrations, tested restore, retained clocks and correction history |

### Outcome measurement contract

Use case cohorts defined by first-valid-report date, participating category, and service scope. Store definitions and exclusion reasons with the report. Late-arriving evidence updates the cohort transparently; it does not silently change a published denominator. Count unique operational cases separately from reports, posts, and observations.

- Accepted ownership: at least one accepted next-action obligation within the configured acknowledgement window. Partial acceptance counts only for the accepted action; show remaining unowned actions separately.
- Ownership time: elapsed time to the first accepted obligation. Report cases still awaiting ownership as censored/unresolved rather than dropping them from the dataset.
- Verified restoration: configured reviewer decision supported by the required evidence. An agency completion claim belongs in a separate measure.
- Reporter effort: additional contacts and required clarification after submission. Distinguish optional participation from required coordination work.
- Community usefulness: completion of a chosen task and interview evidence. Do not substitute time spent, outrage, or total notifications.
- Model usefulness: extraction errors, corrections, abstentions, review burden, latency and cost by task/language. Model-reported scores alone do not establish accuracy.

Record a four-week baseline where practical and review weekly during the controlled pilot. The 80% ownership, 30% median improvement, and 70% positive research targets above are proposed targets, not contractual commitments until the program owner establishes feasibility and partner terms.

### Commercial and operating assumptions

| Cost driver | Budget input | Control |
|---|---|---|
| API/database | Request volume, query cost, retained rows, replicas/backups | Measured capacity envelope and query budgets |
| Media | Upload volume, original/derivative retention, downloads | Compression, upload limits, explicit retention, delivery monitoring |
| AI | Tasks/image or audio, tokens, retries, model/compute pricing | Per-task budget, bounded retries, manual alternative, evaluation-based selection |
| Moderation | Submitted content, review rate, appeal rate, language coverage | Staffing and queue capacity; scope pause when service cannot be supported |
| Case coordination | Eligible cases, disputes, evidence exchanges | Named owners, templates/playbooks, integration and handover agreements |
| Language operations | Translation, local-script QA, model evaluation, reviewer availability | Per-capability release checklist and maintained locale catalog |
| Support/security | Active organizations, incidents, recovery exercises | Runbooks, support contracts, reserved operating capacity |

Do not invent subscription prices or model/cloud costs in this specification. The first pricing exercise uses current supplier quotations, measured pilot usage, and staffing costs. Compare sponsored access and annual organization subscription options using contribution margin and funded continuity, not an assumed advertising business.

### Launch and adoption sequence

1. Select locality/categories and recruit agency contacts, community stewards, accessibility participants, and language reviewers.
2. Obtain source-backed routing data and configure organization permissions, clocks, and calendars.
3. Seed useful service guidance and reviewed demonstrations. Synthetic data remains labeled and separate from live operational statistics.
4. Invite a small resident cohort and field teams; measure task completion, review burden, and response capacity.
5. Expand only the categories, communities, and language capabilities with working ownership and acceptance evidence.

### Business risks and decisions

| Risk or dependency | Consequence | Owner and response |
|---|---|---|
| Agency participation is informal or expires | The platform can record receipt but cannot promise accepted ownership | Program owner tracks signed scope, expiry, named substitutes, and truthful status language |
| Community launches without stewardship | Low usefulness and unmanaged reports | Community lead establishes rules, roster, and appeal capacity first |
| Broad language claims exceed tested capability | Residents misunderstand unavailable voice/OCR | Language lead publishes feature-specific readiness and usable alternatives |
| AI labels unsupported categories confidently | Incorrect routing or unnecessary correction work | AI lead evaluates, abstains, and routes uncertain proposals to review |
| Model/provider dependency or outage | Analysis latency and cost changes | Engineering uses versioned adapters and manual reporting |
| Staffing cost exceeds institutional revenue | Service continuity becomes unsustainable | Sponsor and commercial lead fund capacity and revise scope/pricing |
| Protected intake is enabled without a workable specialist process | Unsafe follow-up and missed responsibilities | Specialist/program owners keep the category disabled until its own readiness decision |

Open deployment inputs are documented in the [suite guide](README.md#decisions-and-configurable-inputs). Their owners must provide evidence before live enablement; they do not prevent implementation with synthetic fixtures and explicit demo settings.
