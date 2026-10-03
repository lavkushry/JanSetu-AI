# JanSetu AI Blueprint v1.0 — Adjudication and Enforcement Design

## Focused gap

**Disputed or delayed administrative handoffs that leave a public problem without an accountable owner.**

## Purpose

JanSetu already represents a public problem as a persistent case with proposed routes, obligations, evidence, and status history. This document defines the pilot mechanism that makes that representation operationally enforceable. It does not redesign intake, AI triage, protected reporting, feed ranking, or the rest of the platform.

The governing rule is:

> A disputed handoff may change who performs an obligation. It may not suspend the obligation, reset case age, or return coordination work to the reporter.

JanSetu’s adjudication is an operational process. It does not decide criminal guilt, legal liability, procurement fault, or disciplinary guilt.

---

## 1. What counts as a dispute

A dispute exists when an authority rejects a proposed assignment, claims that another authority owns the whole problem, accepts only part of an obligation, challenges the location or asset, closes without required evidence, or fails to acknowledge the proposed obligation within the agreed period.

A disagreement about method is not necessarily a responsibility dispute. For example, the roads unit may accept responsibility for barricading an open drain while the water utility investigates the underlying pipe failure. JanSetu should create separate obligations instead of forcing a binary decision.

| Class | Example | Urgency | Decision owner |
|---|---|---:|---|
| Boundary | Location near two wards | Normal | Local coordinating officer |
| Asset owner | Road surface versus utility pipe | High | Service adjudicator |
| Project dependency | Active contractor or planned work | High | Project coordination cell |
| Evidence | Authority says proof is insufficient | Normal | Operations supervisor |
| Safety | No agency accepts immediate hazard | Critical | Safety sponsor |
| Service standard | Closure lacks required proof | High | Supervising authority |
| Cross-level | Local and district bodies disagree | High | Next-level sponsor |

The dispute class determines the clock and evidence packet. The reporter never selects the responsible authority.

## 2. Roles and decision rights

**Reporter:** supplies observations, corrections, and safe follow-up information. The reporter cannot be required to identify the department or contact several authorities to keep the case alive.

**Receiving authority:** must acknowledge, accept a defined subset, or dispute with a reason and supporting record. Silence is not acceptance, but it is a missed pilot commitment.

**Case coordinator:** owns continuity. The coordinator keeps the case moving, creates interim safety tasks, gathers evidence, and sends the dispute to the correct adjudicator. This role has no power to determine legal liability.

**Service adjudicator:** a named officer for each participating authority or service family. The adjudicator assigns operational ownership, creates interim action, and records the evidence basis. The decision binds the pilot workflow unless senior review is invoked.

**Escalation sponsor:** the next administrative level or designated executive who can compel a response, authorize joint action, or accept a documented exception.

**Independent reviewer:** reviews process integrity, retaliation concerns, inaccurate public receipts, or repeated unsupported closure. The reviewer can require correction or re-review but cannot make a criminal or disciplinary finding.

---

## 3. Dispute workflow

### State 0 — Proposed route

JanSetu creates one or more proposed obligations containing the owner, outcome, rule source, evidence, acknowledgement deadline, immediate protection task, and escalation route. The case clock starts at the first valid report and never restarts after transfer.

### State 1 — Acknowledged or partially accepted

The authority identifies the obligation it accepts. Partial acceptance is preferred to blanket rejection. Typical obligations include temporary protection, utility inspection, contractor confirmation, surface restoration, and cross-department coordination.

### State 2 — Disputed

A dispute requires a structured reason and supporting reference, such as a boundary record, asset register, work order, delegated responsibility order, or specific evidence deficiency. “Not our department” alone is an incomplete dispute. When immediate safety is involved, the authority must accept an interim protection task while the ownership question is examined.

### State 3 — Evidence exchange

The coordinator sends a compact packet containing the original observation, location confidence, relevant boundary or asset records, work-order references, previous accepted decisions, and the exact decision required. Authorities may correct records, add documents, identify another actor, or accept a limited obligation. All changes are time-stamped and attributed.

### State 4 — Coordinator recommendation

The coordinator records accepted obligations, unowned obligations, evidence for each possible owner, immediate risk, the requested adjudication decision, and the latest safe interim action. AI may summarize or flag missing evidence; the coordinator signs the recommendation.

### State 5 — Adjudication

The adjudicator chooses one outcome:

1. assign the obligation to Authority A;
2. assign separate obligations to Authorities A and B;
3. assign an interim owner while a higher-level decision is pending;
4. return the case for a specified missing fact with a new deadline; or
5. record that the matter is outside the pilot and create a safe external referral.

The decision must state the evidence basis, owner, effective time, next deadline, and review route. “Closed” is not an adjudication outcome.

### State 6 — Enforcement and monitoring

JanSetu creates the accepted obligation and starts the responsible authority’s service clock. The original dispute remains in the history. Failure to act moves the obligation to escalation; it does not return the case to routing.

### State 7 — Senior review or process appeal

An authority may request one senior review for a material factual or procedural error. A reporter may request process review when the case was closed without evidence, a public receipt is wrong, or retaliation is alleged. Review does not suspend immediate safety duties or accepted obligations.

---

## 4. Escalation timeline

The pilot agreement should adopt one standard clock. The following schedule is recommended for ordinary public-service cases:

| Time from first valid report | Required event | If missed |
|---|---|---|
| T+0 | Case and interim-risk assessment created | Coordinator task opened |
| T+4 hours | Proposed owner accepts, partially accepts, or disputes | Supervisor alert |
| T+24 hours | Dispute reason and supporting record submitted | Service adjudicator notified |
| T+48 hours | Evidence exchange completed | Incomplete-packet notice |
| T+72 hours | Adjudication decision recorded | Escalation sponsor alerted |
| T+5 days | Accepted owner begins action | SLA breach and senior review |
| T+7 days | Progress evidence or justified plan | Public status may show at-risk state |
| T+14 days | Senior review of unresolved obligation | Sponsor records action or referral |

For an active threat, violence in progress, child-safety concern, exposed electrical hazard, or other immediate danger, the ordinary clock does not apply. The receiving authority must meet the locally agreed emergency window, and the coordinator must provide the appropriate emergency or specialist referral. Long-term ownership can be adjudicated afterward.

Silence is recorded as **unacknowledged**, not acceptance and not closure. Repeated silence escalates the obligation and enters the authority’s weekly service review. JanSetu must not publish a refusal claim unless the audit record supports it and publication policy permits it.

---

## 5. Enforcement controls

JanSetu has no statutory enforcement power. The pilot therefore uses operational, governance, and carefully limited public controls.

### Operational controls

- Preserve case age through every transfer.
- Keep a supervisor exception queue for missed deadlines.
- Require an interim owner for immediate safety obligations.
- Block final closure while required evidence is missing.
- Show disputed and overdue obligations in the authority console.
- Include open disputes in the weekly pilot governance meeting.
- Generate a structured escalation package for the next administrative level.

### Public accountability controls

For a public-eligible case, the receipt may state **responsibility dispute under review**, **next update overdue**, or **verification pending**. It must not publish personal blame, motives, criminal allegations, or protected reporter information.

### Governance controls

Repeated missed obligations may trigger a remedial plan, sponsor escalation, existing audit or ombuds referral, contractor review, service-level remedy, or suspension of additional categories from the pilot. JanSetu records and triggers the agreed review; it does not impose fines, suspend officials, or direct police action.

---

## 6. Required pilot agreement clause

Counsel should adapt this clause to the authority’s delegation, procurement, data-processing, public-record, and grievance rules:

> **Disputed Responsibility and Continuity.** The Participating Authority designates the officers listed in Schedule [X] as its operational contacts and service adjudicators for JanSetu cases within the Pilot Scope. On receiving a JanSetu case or proposed obligation, the Authority shall, within the response period in Schedule [Y], (a) acknowledge and accept the obligation, (b) accept a defined part and identify any dependency, or (c) dispute responsibility by submitting a reason code and supporting record through the agreed JanSetu channel. A statement that the matter belongs to another department, without a reason code or supporting record, is an incomplete dispute.
>
> A dispute, transfer, or officer change shall not reset case age, suspend an immediate safety obligation, or require the reporting person to resubmit the matter. Where an immediate safety risk exists, the Authority shall identify or perform an interim protection action within the emergency period in Schedule [Y], without prejudice to its position on long-term responsibility. JanSetu shall maintain the case, evidence, dispute history, and accepted obligations during the process.
>
> The designated adjudicator shall issue an operational decision within the adjudication period in Schedule [Y]. The decision shall identify each outstanding obligation’s owner, evidence basis, next due date, and senior-review route. It is binding for the Pilot workflow unless a senior review is requested within [number] business days for a material factual or procedural error. A senior review shall not suspend immediate safety actions or accepted obligations.
>
> The Authority authorizes JanSetu to generate internal exception reports and, for cases marked public-eligible under the agreed publication policy, sanitized status statements describing the current obligation state, elapsed time, and next expected update. Such statements shall not disclose protected reporter information, assert criminal or disciplinary guilt, or replace a statutory investigation or legal process. The Authority retains responsibility for statutory decisions, emergency response, investigation, discipline, and service delivery.
>
> Repeated failure to acknowledge, adjudicate, or progress obligations shall be reviewed in the Pilot governance meeting and may result in a remedial action plan, escalation to the Pilot Sponsor, referral under the Authority’s existing oversight process, or suspension of additional case categories from the Pilot. Nothing in this clause creates a new statutory duty, transfers legal liability to JanSetu, or limits the Authority’s lawful powers and obligations.

---

## 7. Pilot acceptance tests

The adjudication capability is ready when:

1. Two authorities dispute a road case, but case age and reporter continuity remain unchanged.
2. Separate surface-safety and utility-investigation obligations can be accepted independently.
3. No free-text-only dispute can satisfy the pilot acknowledgement requirement.
4. Missed deadlines create the agreed supervisor and sponsor alerts.
5. An immediate hazard receives an interim owner during long-term adjudication.
6. Closure without required evidence is rejected or marked claim-only.
7. Senior review preserves immediate obligations and records its reason.
8. A public receipt describes the dispute neutrally without exposing a reporter or alleging misconduct.
9. An auditor can reconstruct every transfer, decision, deadline, and sensitive access.
10. The complete dispute history can be exported in a portable format.

The pilot should measure time to accepted responsibility, time to interim safety action, citizen coordination effort saved, dispute recurrence, unsupported closure rate, and verified restoration. The number of adjudications is not a success metric; the reduction of unresolved ownership gaps is.
