# Event and privacy contracts

## Public endpoints

| Endpoint | Contract |
| --- | --- |
| `GET /v1/feed?sort=recommended` | Existing feed DTO plus optional explanation, exposure ID, civic section and serving mode; original sorts remain supported |
| `GET /v1/me/recommendation-preferences` | Authenticated viewer's explicit interests, languages, coarse locality, consent, version and history generation |
| `PUT /v1/me/recommendation-preferences` | Full explicit input; `If-Match` version; default consent off; no-op does not bump version |
| `POST /v1/me/recommendation-history/reset` | `If-Match`; advance generation; preserve chosen preferences; remove snapshots/exposures/events |
| `POST /v1/me/recommendation-events` | Current consent, UUID event/exposure identity, served revision and expiry; unknown fields rejected |

All mutations require the existing CSRF/origin/session checks. Preferences and event rows are RLS owner-scoped. Recommendation commands refresh current principal/profile state and session inside the transaction. Operations, publication, media, projection-worker and vault roles cannot read personal recommendation tables. The projection worker can execute only a fixed retention cleanup function.

Event kinds: READ, SKIP, MORE, LESS, SATISFIED, DISSATISFIED. READ uses foreground active milliseconds bounded by exposure age plus one second for clock/transport tolerance, capped at ten minutes, and normalized by `max(3000, UnicodeLength(body)*300)` milliseconds with a maximum of 1. Reading is supporting evidence; it has no behavioral ranking weight in this baseline. The UI currently submits only explicit More/Less; it does not silently collect reading time or claim to detect background attention. Watch/completion/replay events are rejected until video delivery exists.

Server exposures are minted only for consented authenticated viewers and only inserted for returned published revisions. Exposure IDs are frozen across cursor retries. A snapshot is not proof an item was visually viewed; this first ledger records served exposure, not viewport impression. Clients must keep the same event ID/body for retries. Repeated event ID with different fields conflicts; multiple IDs for the same exposure/kind also conflict. Duplicate submissions do not accumulate duration. Event endpoints reject expired, foreign, stale-generation, withdrawn, blocked/muted or revision-changed content.

## Retention

Snapshot tokens expire after five minutes; a worker janitor runs each minute to delete expired snapshots. Recommendation exposures and their cascade-linked events are retained for at most 30 days plus the janitor interval. Exposure submission expires with the snapshot, even though diagnostic events remain longer. Preference changes/reset delete old exposures/events and snapshots synchronously. Account deactivation clears behavioral consent/history and advances generation in a database trigger. Foreign keys remove personal rows on actual profile deletion; logical deactivation is covered by the trigger. Snapshot removal on deactivation is explicit because anonymous snapshot ownership permits the nil viewer ID.

No asynchronous personal feature store or dataset exists yet, so there is no hidden downstream copy of current events. Distributed expansion must add versioned revocation/deletion tombstones and acknowledgement tracking before exporting behavior. Backups and any future analytics retention require their own documented deletion process.

## Future stream envelope

Use a separate recommendation stream with this allowlisted envelope, not the mixed existing operational outbox:

- `schemaVersion`, globally unique `eventId`, `eventType`, `occurredAt`, producer version.
- Content key: opaque public post ID, published revision, author/community public IDs, entity version, eligibility/revocation action.
- Interaction key: pseudonymous viewer key, current consent/history generation, opaque exposure ID, served revision, normalized action/format and active duration.
- Exposure policy: model/policy versions, candidate source, exploration flag, conditional selection probability, experiment assignment/probability.
- Consumer state: consumer name, event ID and last applied entity/generation version. Replay applies no duplicate deltas and cannot resurrect prior consent generations.

Publish content understanding only from eligible approved social revisions. Exclude reports, private evidence/media, identity-vault data, raw OCR, internal moderator report grounds and operational case stores. Only sanitized, published civic receipt projections can appear in serving, and urgency remains independent of behavior. No stream export is implemented by the baseline; Kafka and analytics consumers remain gated on this contract.
