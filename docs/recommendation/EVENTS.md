# Event and privacy contracts

## Public endpoints

| Endpoint                                   | Contract                                                                                                                                                              |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/feed?sort=recommended`            | Existing feed DTO plus optional explanation, exposure ID, civic section and serving mode; original sorts remain supported                                             |
| `GET /v1/me/recommendation-preferences`    | Authenticated viewer's explicit interests, languages, coarse locality, consent, version and history generation                                                        |
| `GET /v1/me/recommendation-feedback-summary` | Owner-only current-generation counts of More, Less, Helpful and Not helpful events from the last 30 days; zero counts without consent |
| `PUT /v1/me/recommendation-preferences`    | Full explicit input; `If-Match` version; default consent off; no-op does not bump version                                                                             |
| `POST /v1/me/recommendation-history/reset` | `If-Match`; advance generation; preserve chosen preferences; remove snapshots/exposures/events                                                                        |
| `POST /v1/me/recommendation-events`        | Current consent, UUID event/exposure identity, served revision and expiry for new events; identical stored-event retries remain acknowledged; unknown fields rejected |

All mutations require the existing CSRF/origin/session checks. Preferences and event rows are RLS owner-scoped. Recommendation commands refresh current principal/profile state and session inside the transaction. Operations, publication, media, projection-worker and vault roles cannot read personal recommendation tables. The projection worker can execute only a fixed retention cleanup function.

Event kinds: READ, SKIP, MORE, LESS, SATISFIED, DISSATISFIED. READ uses foreground active milliseconds bounded by exposure age plus one second for clock/transport tolerance, capped at ten minutes, and normalized by `max(3000, UnicodeLength(body)*300)` milliseconds with a maximum of 1. Reading is supporting evidence; it has no behavioral ranking weight in this baseline. The UI submits explicit More/Less and optional Helpful/Not helpful feedback from the explanation panel for consented exposures. Usefulness feedback is separate from topic preference and does not hide a post. The first usefulness attempt is retained per mounted exposure, including after an ambiguous failure; the opposite answer stays disabled while the original can be retried with the same event ID. The controls reset when the exposure changes; it does not silently collect reading time or claim to detect background attention. Watch/completion/replay events are rejected until video delivery exists.

Server exposures are minted only for consented authenticated viewers and only inserted for returned published revisions. Exposure IDs are frozen across cursor retries. A snapshot is not proof an item was visually viewed; this first ledger records served exposure, not viewport impression. Clients must keep the same event ID/body for retries. Repeated event ID with different fields conflicts; multiple IDs for the same exposure/kind also conflict. Duplicate submissions do not accumulate duration. New events reject expired, foreign, stale-generation, withdrawn, blocked/muted or revision-changed content. Identical stored-event retries are acknowledged before exposure expiry/visibility checks, while still requiring the current owner, active session, consent and history generation.

## Retention

Snapshot tokens expire after five minutes; a worker janitor runs each minute to delete expired snapshots. Feedback events are retained for 30 days from their own creation, plus the janitor interval. Exposures older than 30 days are removed once no retained events reference them; feedback submitted near the end of the five-minute exposure window can keep its exposure for up to five additional minutes. New exposure submissions expire with the snapshot; identical retries of stored events remain acknowledged within the current owner/history generation while the stored event is retained. Preference changes/reset delete old exposures/events and snapshots synchronously. Account deactivation clears behavioral consent/history and advances generation in a database trigger. Foreign keys remove personal rows on actual profile deletion; logical deactivation is covered by the trigger. Snapshot removal on deactivation is explicit because anonymous snapshot ownership permits the nil viewer ID.

The optional stage-3 publisher now exports allowlisted envelopes into Kafka and Redis projections. [Stream delivery and retention](STREAMS.md) documents live authority checks, revocation/deletion fences, usable feature expiry and the separate broker retention window. No analytics/training dataset export exists yet. Backups and future analytics retention require their own documented deletion process.

## Stream envelope and future extensions

Use a separate recommendation stream with this allowlisted envelope, not the mixed existing operational outbox:

- `schemaVersion`, globally unique `eventId`, `eventType`, `occurredAt`, producer version.
- Content key: opaque public post ID, published revision, author/community public IDs, entity version, eligibility/revocation action.
- Interaction key: pseudonymous viewer key, current consent/history generation, opaque exposure ID, served revision, normalized action/format and active duration.
- Exposure policy: model/policy versions, candidate source, exploration flag, conditional selection probability, experiment assignment/probability.
- Consumer state: consumer name, event ID and last applied entity/generation version. Replay applies no duplicate deltas and cannot resurrect prior consent generations.

Publish content understanding only from eligible approved social revisions. Exclude reports, private evidence/media, identity-vault data, raw OCR, internal moderator report grounds and operational case stores. Only sanitized, published civic receipt projections can appear in serving, and urgency remains independent of behavior. The current stream implements controls, normalized interactions and public content references. Retrieval-source, exploration and experiment probabilities require their own serving instrumentation before future model/dataset consumers can use them.

## Private feedback summary

`GET /v1/me/recommendation-feedback-summary` returns only the authenticated viewer's More, Less, Helpful and Not helpful counts and current history generation. It counts current-generation events from the last 30 days under owner RLS and the same transaction lock used for consent changes and history reset. Consent off returns zero counts. No post, exposure or other-viewer identifiers are returned.

Recommendation settings display the summary and reload it after new feedback or a preference/history generation change. These are counts of explicit feedback events, not unique posts, reading duration or a satisfaction score. A history reset clears them while retaining chosen preferences; changing preferences or withdrawing consent also clears the old generation.

## One usefulness answer per exposure

New SATISFIED or DISSATISFIED events conflict with HTTP 409 and `USEFULNESS_ALREADY_RECORDED` if either usefulness kind is already recorded for the same viewer, generation and exposure. The recommendation transaction serializes concurrent sessions for that viewer, so opposite answers racing from different tabs cannot both be accepted. The losing UI disables both usefulness choices, displays an already-recorded status and refreshes the private summary without claiming its attempted answer was accepted. More/Less remain independent topic controls; a different exposure can receive its own usefulness answer. Other event-ID/body or exposure-action conflicts retain `EVENT_CONFLICT`.

An identical event-ID/body retry still succeeds, including after exposure expiry, subject to current owner, session, consent and generation checks. Clients must retain the first attempted answer and event ID across ambiguous failures. This rule does not rewrite historical events or alter ranking weights.
