# JanSetu AI — Social experience, OCR, and image recognition

> Historical design input. The [v3 specification suite](spec/README.md), especially [UI implementation](spec/UI_IMPLEMENTATION.md) and the [UX catalog](spec/TRACEABILITY_AND_DELIVERY.md#ux-scenario-catalog), supersedes this plan. Reference findings remain in the linked research report.

Date: 3 October 2026. Status: proposed implementation requirements; no application or model benchmarks have been built by this document.

The user wants an excellent social experience inspired by Reddit and X, connected to the original CampusFix idea and strengthened with OCR and image recognition. “OCI” was clarified to mean **OCR: reading text from images**. A cloud provider has not been selected.

Read this with the [detailed blueprint](../JanSetu_AI_Detailed_Blueprint.md) and [reference research](REFERENCE_RESEARCH.md). This document extends the screen and AI requirements. The blueprint's role permissions, publication rules, and release priorities continue to apply. Where a proposed feature is P1 or P2, its design may be reviewed early, but its functionality remains disabled until that release's requirements pass.

## 1. Product direction

Build a place residents want to read and contribute to every day: relevant local conversations, useful communities, quick updates, and satisfying discussions. Connect an actionable report to a lasting case with clear responsibility, evidence, and progress.

The target journey is:

1. A resident discovers a useful local discussion or describes an issue using text, voice, or a photo.
2. OCR and image analysis propose extracted facts and categories; the resident corrects them.
3. Neighbors contribute observations or join an eligible existing case.
4. A coordinator assigns defined obligations; agencies accept, dispute, and update them.
5. The public sees reviewed progress and the evidence basis for restoration.
6. The resident returns for relevant conversations and meaningful updates.

“Best” is a product ambition. Evaluate it through task completion, qualitative research, accessibility, device performance, and outcome quality. Enumerated scenarios provide coverage; they do not prove every possible scenario is solved.

## 2. What Reddit and X contribute

| Inspiration | JanSetu experience | Required implementation detail |
|---|---|---|
| Reddit communities | Locality and topic spaces, clear rules, membership, stewards | Joining and following have separate labels and state |
| Reddit conversations | Nested replies, collapse, helpful/newest/oldest sorting, community context | Paginated children, stable navigation, deleted-parent tombstones |
| Reddit contribution discovery | Usefulness voting, questions, reviewed playbooks | One vote system; operational case priority has separate rules |
| X timelines | Readable stream of short updates, For You and Following tabs, profiles | Preserve chosen feed, filters, and scroll position within the session |
| X sharing | Reposts and later quote posts with source context | Reference the live source, honor removal, avoid duplicate feed cards |
| X fast participation | Obvious composer, quick reply, follow, bookmark, activity | Clear pending states, optimistic social actions with rollback |
| CampusFix origin | Describe an issue and follow it through responsible teams | Civic and campus organization scopes use explicit routing records |
| JanSetu case management | Responsibility, deadlines, escalation, evidence, verification | Every status has an authorized source and visible history |

The interaction references are documented in [Reddit community settings](https://support.reddithelp.com/hc/en-us/articles/15484546290068-Community-settings), [X timeline behavior](https://help.x.com/en/using-x/x-timeline), and [X repost behavior](https://help.x.com/en/using-x/how-to-repost). These sources establish recognizable patterns. JanSetu's exact behaviors and visual design are our own proposals.

## 3. Visual direction and navigation

### Layout

- Desktop: a persistent left navigation rail, readable central stream, and contextual right column. Use the blueprint's approximate 224 / 680 / 300 px proportions where space permits. Content determines when the right column collapses.
- Tablet: compact navigation; move contextual content into a drawer or the page body. Preserve access to locality, language, and filters.
- Mobile: Home, Communities, Create, Activity, Profile. Put search in the header and My cases within Home/Profile. Apply safe-area insets and keep the composer action reachable above the keyboard.
- Home: Following and For You are social feeds. Unresolved, Nearby, and Resolved retain the blueprint's civic discovery semantics. Overflow tabs must remain discoverable without horizontal page scrolling.
- Communities: directory, community detail, rules, Posts/Cases/Playbooks, membership, and moderation states.
- Search: query, content type, locality, category, language, and status. Keep query and filters when opening and returning from a result.
- A public case is a dedicated detail view linked from posts, receipts, search, and followed-case activity.

### Visual system

- Use a restrained neutral canvas, strong typography, a blue primary action, and semantic status colors with text/icons. Start with the blueprint's tokens and verify combinations in the actual components.
- Provide light, dark, and system theme modes. Define semantic surface, text, muted, divider, focus, action, and status tokens for each theme before implementing components.
- Provide comfortable and compact feed density. Compact mode reduces whitespace and media previews while preserving text legibility and touch targets.
- Use inline dividers for the stream, stronger grouping for cases and evidence, and a single clear primary action per contextual panel.
- Maintain a consistent icon family and action order. Distinguish account affiliation, community role, content review state, and operational status.
- Body text must support the pilot's scripts at 16 px with scalable line height. Test long names, translated labels, mixed scripts, large system text, and 200% web zoom.
- Respect reduced motion. Status changes and newly arrived posts must not move the item someone is reading. Show “New updates available” and let the reader refresh.
- Onboarding supports reading before sign-in. Ask for locality/language explicitly; continuous location access is unnecessary.

### Card anatomy

| Surface | Information hierarchy | Actions |
|---|---|---|
| Short post | Author/affiliation, community and time, body, optional media, linked case | Usefulness vote, reply, repost, bookmark, share; contextual menu |
| Discussion/question | Community, title, author/time, bounded body preview, optional media | Same social actions; helpful response selection for eligible author |
| Case receipt | Title, coarse locality, status, case age, accepted owner or review state, next update | Follow case, add observation, open timeline |
| Official update | Agency affiliation, reviewed statement, linked receipt, source/time | Open case, follow; no case vote score |
| Media preview | Approved derivative, caption/alternative text, bounded dimensions | Open viewer, inspect caption; never fall back to the private original |
| Quote post, P1 | Quoting author's text, referenced eligible source | Social actions; neutral source-unavailable treatment after revocation |

Show the actions relevant to the viewer's permissions. Reserve metadata space while loading so content does not jump. Translate icon labels and tooltips. Media uses an intentional aspect ratio and an accessible viewer with keyboard dismissal, focus return, and image descriptions.

## 4. Screen contracts

| Screen | Essential behavior | Design review must include |
|---|---|---|
| First visit | Choose area/language or browse public communities; sign-in at write action | No GPS, no chosen area, empty area, signed-out reader |
| Home/feed | Remember session filters, maintain stable item order, explain recommendations | Both themes/densities, unseen updates, duplicate reposts, empty Following |
| Community | Show identity, rules, join/follow, posts/cases/playbooks | Public, restricted-posting, joined, banned, archived |
| Thread | Original post, collapsible replies, sorts, quick reply, deeper-thread route | Deleted parent, long thread, blocked account, new replies, draft conflict |
| Composer | Audience/community, draft, media progress, public preview, publication state | Pending review, failed image, long text, audience switch, stale revision |
| Search | Debounced query with cancellation/stale-result protection and filters | No match, mixed scripts, removed results, back navigation |
| Profile | Public contributions, affiliation, follow/mute/block | Own versus another profile, removed account, long biography |
| Activity | Social interactions, case progress, account security; separate read state | Repeated events, withdrawn text, notification permissions off |
| Service report | Text/voice/photo, correct analysis, confirm facts/location, similar cases, sharing, receipt | No media, OCR failure, offline draft, uncertain location, retry |
| My cases | Private authorized report progress, information requests, follow-up | No reports, acknowledgement lag, closure challenge |
| Public case | Summary, timeline, responsibilities, public evidence, linked discussion | Multiple agencies, dispute, overdue update, completion claim, verified outcome |
| Authority workspace | Assigned queue, due times, accept/partial/dispute, scoped evidence, work update | Permission change, stale assignment, missed deadline, agency outage |
| Field worker | Minimum necessary task, directions, evidence checklist, offline update | Lost network, upload retry, expired assignment |
| Moderator workspace | Scoped queues, reasoned decisions, appeals, revision history | Obsolete revision, conflict of interest, restoration after removal |
| Preferences | Theme, density, language, locality, personalization, blocked/muted items | Reset, large type, reduced motion, notification channels |

Direct messages, live rooms, livestreaming, and expanded private communities remain roadmap decisions with the blueprint's P1/P2 conditions. Visual similarity to a mature social platform does not imply those systems already exist.

## 5. OCR and image recognition

### Responsibilities

| Task | Input | Result the user or coordinator can inspect |
|---|---|---|
| OCR | Consented photo/document crop | Text regions with bounding boxes, script/language candidates, uncertain spans |
| Issue detection | Eligible image of an asset or service problem | Candidate category, region overlay, model score, limitations |
| Scene description | Eligible image plus observation | Suggested description grounded in visible regions |
| Location assistance | User landmark, permitted metadata, visible text | Candidate landmark/area with source; user confirmation |
| Duplicate suggestion | Eligible summary, category, geography/time, asset | Similar cases with reasons; independent same/different/unsure decision |
| Public redaction assistance | Proposed public derivative and text | Suggested sensitive regions and text removals for review |
| Before/after comparison | Authorized paired evidence | Alignment/coverage issues and differences; reviewer decision remains separate |

OCR, object detection, and visual language reasoning are separate tasks. Success at one does not establish the others. An image may show several issues, no supported issue, misleading text, or insufficient detail. Preserve those outcomes explicitly.

### Processing flow

1. Create an authorized upload session and upload to private quarantine.
2. Validate bytes, file signature, limits, and scan/decode results before model processing. Reject malformed or unsupported inputs without model calls.
3. Create an analysis job for the exact media revision and approved purpose. The worker resolves the storage reference; clients cannot make it fetch an arbitrary URL.
4. Assess orientation, blur, glare, visibility, and text size. Offer a useful retake/crop suggestion; retain the ability to continue with text.
5. Run requested OCR and/or vision adapters under bounded time, cost, and resource limits. Hosted inference follows the blueprint's data-processing approval requirements.
6. Validate allowed taxonomy IDs, bounding boxes, evidence references, schema version, and source media revision. Reject invented categories and references.
7. Show suggestions in an editable review panel. A person confirms the description, category, location, time, and public-sharing choice.
8. A successful submit commits a report using its idempotency key. Publication creates a separately reviewed sanitized derivative.

Model findings stay proposals. AI cannot mark a case verified restored, infer a responsible department without sourced jurisdiction rules, or treat OCR text as workflow instructions. Prompt injection in photographed signs or uploaded documents is untrusted content.

### Analysis contract

The private job result needs: job ID, media revision reference, purpose, task/schema versions, status, engine/model version, preprocessing record, text regions, issue candidates, quality warnings, timing, and reviewer/user corrections. Internal evidence identifiers and storage keys are never included in a public receipt.

Store statuses such as `queued`, `running`, `succeeded`, `partial`, `failed`, and `cancelled`, with structured reason codes. OCR can succeed while issue detection fails; the interface must explain that partial result. Make raw model scores distinct from evaluated/calibrated confidence and from human acceptance.

| Analysis UI state | User experience |
|---|---|
| Waiting/running | Image preview, task-specific progress, continue manually option |
| Text found | Optional region overlay, extracted text editor, uncertain spans, apply selected text |
| Multiple issue candidates | Show visible regions and ask which observations apply |
| No text or supported issue | Clear result and editable report form; no invented diagnosis |
| Low-quality image | Concrete retake/crop suggestion; preserve supplied description |
| Partial result | Show completed task and retry only the failed task |
| Timeout/provider failure | Preserve draft and upload; retry without reuploading or submit manually |
| Sensitive public preview | Hold publication, explain requested redaction/review, retain authorized private evidence |
| Media replaced/deleted | Cancel or discard stale results; do not apply findings to the replacement |

### Engine and hardware decisions

Keep a versioned Python service with task-specific adapters; FastAPI is the proposed HTTP framework. Benchmark an OCR engine, a detector for the pilot taxonomy, and any visual language model independently. Candidate engines may come from the reference research, but none is selected merely because a demonstration used it.

PaddleOCR is a candidate to evaluate, with the exact recognition model, language/script support, weights, and preprocessing recorded. Its [multilingual documentation](https://www.paddleocr.ai/main/en/version3.x/algorithm/PP-OCRv5/PP-OCRv5_multi_languages.html) distinguishes model language families; benchmark our chosen languages rather than assuming one model covers every script and handwriting style.

CPU processing or a contracted inference endpoint can support the first implementation. An AMD/ROCm path is optional after confirming the exact device, OS, runtime, framework, and model combination against [AMD's compatibility matrix](https://rocm.docs.amd.com/en/latest/compatibility/compatibility-matrix.html). GPU presence alone does not prove compatibility or justify deployment cost.

Before importing code or model assets, verify repository, dependency, model-weight, and dataset licenses separately. The reference report records what could be confirmed. Preserve attribution where required and implement missing production controls within JanSetu's own service boundary.

## 6. Scenario coverage and expected behavior

| ID | Scenario | Observable acceptance behavior |
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

Use these with the blueprint's AC-01–37. UI screenshots alone cannot establish authorization, idempotency, notification withdrawal, or race behavior; verify the complete user-visible flow against real backend behavior when implemented.

## 7. Quality evidence before release

| Area | Evidence needed |
|---|---|
| Visual quality | Reviewed desktop/tablet/mobile layouts, light/dark themes, both densities, long content and all relevant screen states |
| Ease of use | Representative residents and staff complete discover, post/reply, report/correct, track, and act tasks; record completion and failure reasons |
| Accessibility | WCAG 2.2 AA checks plus keyboard, screen reader, large text, contrast, local scripts, focus and reduced-motion verification |
| Client performance | First usable feed target p75 under 2.5 seconds on a documented pilot device/network; measure media and interaction responsiveness |
| API performance | Blueprint targets: feed p95 under 500 ms, social commit p95 under 700 ms; initial stress envelope 100 feed reads/s, 100 interaction writes/s, 10 concurrent uploads |
| OCR | Labeled samples by language/script, printed versus handwriting, size, glare, rotation; character/word error rates and correction burden |
| Detection | Per-category precision/recall, unsupported-category behavior, location/asset confusion, multi-issue images and out-of-distribution examples |
| Models and operations | Versioned evaluation set, chosen thresholds, abstention/review, inference latency/cost, retries, deletion/cancellation and independent rollback |
| Case outcomes | Correct owner/obligation history, deadline continuity, source-backed status and evidence review; distinguish agency claims from verification |

Numbers are acceptance targets from the existing blueprint, not measured results. Establish OCR/detection thresholds from the pilot's labeled data and reviewed error costs before selecting a model. No generic “99% accurate” claim is justified by the references alone.

## 8. Implementation order and outstanding choices

1. Review the visual system and representative Home, Community, Thread, Composer, and Case screens with realistic synthetic content and their failure states.
2. Implement web shell and shared tokens, API contracts, identity, community/post/thread actions, and media quarantine.
3. Deliver one complete photo/text report journey: upload → OCR/vision proposals → correction → receipt → assigned obligation → reviewed progress → verification.
4. Add discovery, activity, moderation, authority and field-worker workspaces; validate the scenario matrix through the connected system.
5. Implement native screens against the stable contracts and validate pilot devices, weak networks, media/push permissions and draft recovery.
6. Expand model categories, languages, quote posts, richer search, and other roadmap features using measured results.

This sequence refines the blueprint's delivery packages; it does not replace their dependencies or create a new launch-date promise. UI review and API work can overlap where staffing permits.

Outstanding choices: pilot geography and organizations, two initial languages, representative device/network, identity and cloud provider, OCR/detector/model licenses and benchmarks, compute hardware, and implementation staffing. The user selected Go for the backend. The stack is Next.js/React/TypeScript, Expo/React Native, Go with `net/http`, PostgreSQL/PostGIS with `pgx`/`sqlc` and Goose migrations, Redis, private S3-compatible storage, durable Go outbox workers, and the Python AI adapter. The [blueprint](../JanSetu_AI_Detailed_Blueprint.md) defines the explicit transaction, authorization, and module boundaries for that implementation.

Implementation is complete only when the connected flows, scenario behavior, model evaluation, and operating requirements have evidence. This document is the design and acceptance contract for that work.
