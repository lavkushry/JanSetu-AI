# Runnable core, accounts, vault isolation and private image analysis — implementation status

This milestone connects the first usable community and civic-service workflows to a real Go API and persistent PostgreSQL database. The [canonical specification suite](spec/README.md) remains the full product target. The milestone is a local demonstration with fictional identities and mandates, not a production pilot or completion of every P0 requirement.

## Implemented behavior

| Area             | Working behavior                                                                                                                                                                                   | Current boundary                                                                                                                                                                                                                                                                                                                                                             |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Social feed      | Latest/top social posts; followed communities, people, and service receipts; unresolved/resolved service views; bookmarks; typed search                                                            | City view shows the seeded city. No GPS discovery or production ranking model.                                                                                                                                                                                                                                                                                               |
| Feed pagination  | Signed, viewer/query-bound snapshots; five-minute expiry; current visibility checked during hydration                                                                                              | Up to 200 posts and 200 public receipts per snapshot. Signing key is process-local; an API restart invalidates old cursors.                                                                                                                                                                                                                                                  |
| Communities      | Browse, rules, rules revision check, join/leave, follow/unfollow, scoped conversations                                                                                                             | Public/restricted communities only; creation, private communities, moderator management, and appeals remain pending.                                                                                                                                                                                                                                                         |
| Posts            | Short updates, discussions, questions, owned revisions, approved public revision, deletion notice                                                                                                  | Media, quote posts, polls, links, and resubmission of a restricted initial post remain pending.                                                                                                                                                                                                                                                                              |
| Comments         | Parent validation, ordered replies, pending revisions, edit/review, deletion with descendants retained                                                                                             | Twenty-comment chronological pages without a total cap; depth 20. Separate branch loading, virtualization and full accessibility evaluation remain pending.                                                                                                                                                                                                                  |
| Social actions   | Desired-state votes, bookmarks, reposts, follows, blocks, copied share links; public author profiles, people search and owner-only block management                                                | Reposts maintain a relation/count; a separate repost activity feed, follower directories/counts and public comment/activity histories remain pending.                                                                                                                                                                                                                        |
| Activity         | Private inbox for approved replies and followed reviewed public progress; filters, unread count, persistent read/unread controls, owner RLS and duplicate-safe worker delivery                     | IN_APP only. Account consent and person/community mutes are implemented; outbound channels, mentions, account-security alerts, quiet-hour scheduling and production delivery remain pending.                                                                                                                                                                                 |
| Review           | Separate publication/report queues, exact revision checks, private owner receipts, reasoned dismissal/removal, immutable decisions and obsolete-review rejection                                   | Human synthetic moderation only. Anonymous/extended-target reporting, community moderation, author notices, appeals/restoration and production moderation policy remain pending.                                                                                                                                                                                             |
| Private reports  | Original statement, language label, category, landmark, publication preference, platform receipt, owner-only progress                                                                              | Fictional public-service intake only. No protected reporting, contact details, or real agency submission. Up to four private photos, experimental English OCR and opt-in object candidates are available; public photo publication is disabled.                                                                                                                              |
| Report retries   | Principal-scoped command keys plus lifetime client-submission-ID deduplication and content conflict checks                                                                                         | Failed vault-to-app submission can leave an unused vault alias; retry reuses it. Automated alias cleanup remains pending.                                                                                                                                                                                                                                                    |
| Staff workflow   | Coordinator assessment, urgency reason, agency proposal, acceptance, work, completion claim, independent verification                                                                              | One restoration obligation created per case. Multi-agency dependency coordination, disputes, referrals, SLA clocks, and reopening after verified closure remain pending.                                                                                                                                                                                                     |
| Public progress  | Independently identified receipt, reviewed title/summary/broad area, fixed safe timeline wording, agency/task summaries, follow progress                                                           | Operational updates require a fresh explicit publication review. A report requesting private progress cannot be published. Withdrawal/redaction lifecycle remains pending.                                                                                                                                                                                                   |
| UI               | Desktop sidebars, mobile bottom navigation, 320px reflow, light/dark themes, compact density, loading/error/empty states, native modal focus containment, Escape dismissal                         | English interface. Unicode text and language labels are accepted; this does not imply evaluated multilingual search, translation, voice, or OCR. Full accessibility and assistive-technology audit remains pending.                                                                                                                                                          |
| Drafts           | Explicitly opted-in device storage for post/report drafts, restore/discard, stable restored report ID                                                                                              | No server draft synchronization. Use fictional details on a trusted device.                                                                                                                                                                                                                                                                                                  |
| Authentication   | OIDC authorization code + PKCE, state/browser/nonce verification, exact issuer/subject binding, resident-only provisioning, hashed session cookies, binding/session revocation, CSRF/origin checks | Local Keycloak fixtures; demo shortcuts explicitly gated. Secure cookies follow HTTPS configuration. Provider deprovisioning/backchannel logout, MFA/passkey policy, and controlled staff provisioning remain pending. Startup still requires `local`/`test`.                                                                                                                |
| Account controls | Chosen public name/handle/bio, versioned updates, own-session list, single/other-session revocation, 30-minute request inactivity and 12-hour absolute expiry, account cache clearing              | Provider identity is never copied into public profiles. Account linking, export/closure, and native authentication remain pending.                                                                                                                                                                                                                                           |
| Persistence      | Canonical application/vault foundation migrations, enforcement triggers, transactional commands, generated SQL, outbox, post-stat projection                                                       | Canonical tables outside these workflows are scaffolding, not implemented features. Account-security events have transactional append-only audit. Restricted roles, operational RLS, a separate vault service, encrypted locators and purpose audit are implemented locally. Managed keys, external audit retention and full production deployment isolation remain pending. |

Private photos use resumable 2 MiB parts, committed-part progress, original-file verification after reload, and duplicate protection when an allocation response is lost. Existing single-part API sessions remain supported. See the [resumable upload guide](RESUMABLE_PHOTO_UPLOADS.md) for recovery and migration boundaries, the [recognition guide](PRIVATE_IMAGE_RECOGNITION.md) for object candidates, and the [private media/OCR guide](PRIVATE_MEDIA_OCR.md) for processing and review.

Public author profiles now show chosen details and approved published posts with keyset pagination. People search, follow/unfollow and owner-only block management are implemented, including removal of blocks on inactive accounts. Either-direction blocks hide profiles/content, and public timelines exclude pending edits even for their author. See the [public profile guide](PUBLIC_PROFILES.md) for contracts and current boundaries.

Conversations now load twenty visible comments per chronological page with no total-row cap. Reply context, parent links, loaded-branch collapse controls and refresh recovery are available. Deleted placeholders exclude body, author and candidate; new replies recheck parent author/block visibility. See the [conversation guide](PAGINATED_CONVERSATIONS.md) for pagination semantics and remaining boundaries.

In-app Activity now uses reviewed sources, fixed safe messages and current visibility checks for every page/count. Follow-time checks prevent delayed case events from backfilling a new subscription. See the [Activity guide](IN_APP_ACTIVITY.md) for delivery semantics, contracts and remaining boundaries.

Activity preferences now support server-persisted IN_APP consent and private person/community mutes, including expiry and owner-only management. Feed snapshots and reply alerts recheck these choices while explicit public threads/bookmarks stay readable. See the [preferences and mutes guide](ACTIVITY_PREFERENCES_AND_MUTES.md) for contracts and current boundaries.

Published posts and comments now support private content reports, retry-safe receipts, owner history and a separate moderator queue. Reasoned removal rechecks the published revision, preserves descendant replies and prevents pending edits from reviving hidden content. See the [content reporting guide](CONTENT_REPORTING.md) for privacy, decision and upgrade contracts.

## Run locally

Docker and Compose are sufficient for the complete application:

```bash
docker compose up --build -d
docker compose ps
```

Local Keycloak runs at `http://localhost:8180`. The web app is at `http://localhost:3100`; API readiness is at `http://127.0.0.1:8081/health/ready`. The database is bound to loopback port 5438. Compose preserves data in the `jansetu-data` volume and seeds fixtures without overwriting ordinary user-created records.

For development, install Go 1.26, Node 22, Docker, and Python 3:

```bash
npm ci
make db
make identity
make seed
```

Then run `make vault`, `make api`, `make worker`, and `make web` in separate terminals. Backend defaults match [.env.example](../.env.example). Export environment variables before starting Go processes; the Go binaries do not automatically load `.env` files. Next.js reads its app-local environment file or exported variables. Compose explicitly supplies the internal service URLs.

The API server has graceful shutdown, bounded headers/body/request duration, liveness/readiness probes, safe error responses, and request IDs. Local HTTP requests use the configured `JANSETU_WEB_ORIGIN`, which defaults to `http://localhost:3100`. An alternative browser origin must be configured consistently for both API and web services.

See the [account security guide](ACCOUNT_SECURITY.md) for fixture credentials, `/account` controls, auth configuration, and security boundaries. Open the avatar menu and sign in using the corresponding username; all fixture passwords are `jansetu-demo`.

## Walk through the application

1. Choose Ananya. Create a discussion in Indiranagar. Only the author can see its pending revision.
2. Choose Kiran and open Staff workspace → Content review. Record a reason and approve the exact revision.
3. Choose Rohan. Vote, bookmark, repost, comment, or reply. Comments also await review. Refresh to confirm persistence.
4. Choose Ananya and report a fictional service issue. Review the private submission and select whether a sanitized public progress card is allowed. The acknowledgement confirms platform receipt only.
5. Choose Kiran → Service intake. Assess urgency and propose a City Works restoration task. The case preserves the original receipt time.
6. Choose City Works → Service cases. Accept the task, start work, and record a completion claim.
7. Choose Neha. Record a fictional independent inspection result. Only verified required work resolves the case. Insufficient evidence keeps verification pending; a failed inspection returns work to progress.
8. Choose Kiran. Write and review a public title, summary, and broad area, then publish progress. Private statements, principal IDs, report aliases, and internal case IDs are not copied into the public DTO or generated timeline.

The local fixture demonstrates independent decisions; it does not provide actual inspection evidence. Production verification requires the evidence and oversight gates in the specification.

## Source map

| Location                                                         | Purpose                                                                          |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| [Web app](../apps/web/src/components/jansetu.tsx)                | Shell, navigation, sessions, feeds, search, themes                               |
| [Account UI](../apps/web/src/components/accounts.tsx)            | Provider sign-in, profile settings, session control                              |
| [OIDC provider](../services/backend/internal/authn/provider.go)  | Discovery, PKCE exchange, ID-token verification                                  |
| [Accounts](../services/backend/internal/app/accounts.go)         | Browser flow, provisioning, sessions, audit, profile editing                     |
| [Social UI](../apps/web/src/components/social.tsx)               | Cards, composition, threads, communities                                         |
| [Reporting UI](../apps/web/src/components/reports.tsx)           | Private wizard, receipt, own progress                                            |
| [Staff UI](../apps/web/src/components/studio.tsx)                | Review, intake, agency work, verification, public preview                        |
| [BFF](../apps/web/src/app/api/[...path]/route.ts)                | Server-only upstream address, cookies, mutation headers, body/origin bounds      |
| [Go application](../services/backend/internal/app/http.go)       | HTTP routes, request/session policy checks, DTOs                                 |
| [Transactions](../services/backend/internal/app/transactions.go) | Fresh authority checks, command receipt and outbox transaction                   |
| [Vault isolation](DATABASE_VAULT_ISOLATION.md)                   | Restricted runtime roles, self-only vault protocol, encryption and purpose audit |
| [Reports](../services/backend/internal/app/reports.go)           | Separate vault aliases, intake, owner progress                                   |
| [Cases](../services/backend/internal/app/cases.go)               | Operational state machine and reviewed public projection                         |
| [Worker](../services/backend/internal/app/worker.go)             | Lease fencing, deduplication, locked aggregate projection                        |
| [SQL queries](../services/backend/internal/store/queries.sql)    | Generated `pgx` query inputs and results                                         |
| [OpenAPI](../contracts/openapi/core.yaml)                        | Core REST contract and generated web DTOs                                        |
| [Migrations](../db/migrations/00001_foundation.sql)              | Versioned application foundation; vault migrations live separately               |

Local mutations and projections take a shared advisory lock before aggregate row locks. This intentionally serializes pilot work to keep the first implementation deterministic and prevents the profile/post lock inversion caused by notification foreign keys. It is a throughput limit, and must become ordered aggregate locking before load testing or a multi-city rollout. Workers also lock the relevant post before counting current votes/comments/reposts, preventing an older count snapshot from overwriting a newer one.

The API uses separate account/social/operations/publication pools and calls an internal self-scope vault service. It has no vault database credential or encryption key; the worker also has its own restricted role. See the [database/vault guide](DATABASE_VAULT_ISOLATION.md) for row policies, encrypted ownership, purpose audit, volume-preserving upgrades and remaining production boundaries. Database credentials and keys in the example are synthetic local fixtures. No production hosting, credentials, evaluated models, or integrations are configured. A separate local media worker runs the experimental English Tesseract adapter.

## Verification

```bash
make check
make test-integration
make generate
npx playwright install chromium
npm run test:e2e
```

`make check` validates the specification documents, Go vet/unit tests, TypeScript, and the production web build. Integration tests create randomly named temporary application/vault databases, apply migrations and fixtures, then remove those test databases. They do not reset the running application volume. Integration tests use `JANSETU_MIGRATION_DATABASE_URL` (the synthetic local administrator by default) to create temporary databases and bootstrap runtime roles. They exercise workflows with the restricted runtime credentials.

Go integration checks cover concurrent create retries, conflicting keys/content, unapproved revision privacy, obsolete review decisions, stale versions, desired-state votes, durable bookmarks, blocking, cross-post replies, parent tombstones, report ownership, lifetime report deduplication, original case age, authority boundaries, independent verification, private-public projection isolation, grant revocation, stale worker leases, corrected receipt areas, and the OIDC/account-security adversarial checks detailed in the [account guide](ACCOUNT_SECURITY.md). Go race detection is enabled in `make test-integration`.

Browser checks use real Keycloak sign-in and API/database workflows for social publication and interactions, private report → triage → agency claim → independent verification → reviewed publication, cross-origin rejection, another resident's private-report denial, search, membership, theme persistence, modal dismissal, mobile overflow, first-login provisioning, profile persistence, session revocation from another browser, and logout, plus real private-photo OCR/corrections, draft restoration, rejected-image removal and manual fallback for unavailable OCR, plus real object candidates/regions, partial language states and unchanged manual category/description. Screenshots, traces, and the HTML report are local artifacts under ignored `test-results` / `playwright-report` directories.

Upload browser checks also cover lost allocation/part/completion acknowledgements, same-file recovery after reload, skipped committed parts and finishing a fully committed upload without a local file. The [CI workflow](../.github/workflows/core.yml) repeats these checks and verifies generated SQL/types have no drift. Passing these focused checks does not constitute all 68 canonical acceptance contracts, a load test, a full accessibility audit, or a security review.

## Next implementation milestones

1. Complete production privacy/identity gates: managed envelope encryption and rotation/recovery, independent vault hosting/administration, independent API service identities, external immutable audit retention, provider revocation/MFA and controlled staff provisioning. Restricted database roles/RLS, the separate self-scope vault service, encrypted locators, purpose audit and focused adversarial tests are implemented locally.
2. Extend the implemented local resumable private-photo/OCR flow with cloud object storage, scanning/redaction, governed original-evidence retention, evaluated civic-hazard recognition, per-language OCR evaluations and calibrated results. See [the media guide](PRIVATE_MEDIA_OCR.md) for precise current boundaries.
3. Complete multi-agency obligations, mandates/geography, responsibility disputes, escalation/SLA clocks, outbound notifications and quiet-hour scheduling, and production agency integrations.
4. Finish social post kinds, branch-specific comment loading, profile follower/activity views, community administration, extended content reporting and appeals/restoration, multilingual UI, and Expo mobile clients. Public profiles, block/mute settings and private post/comment reports are implemented; full search/ranking capacity and accessibility gates remain pending.
5. Replace local serialization and bounded search/ranking, then run capacity, accessibility, recovery, and production pilot acceptance gates.
