# Runnable core — implementation status

This milestone connects the first usable community and civic-service workflows to a real Go API and persistent PostgreSQL database. The [canonical specification suite](spec/README.md) remains the full product target. The milestone is a local demonstration with fictional identities and mandates, not a production pilot or completion of every P0 requirement.

## Implemented behavior

| Area | Working behavior | Current boundary |
|---|---|---|
| Social feed | Latest/top social posts; followed communities, people, and service receipts; unresolved/resolved service views; bookmarks; typed search | City view shows the seeded city. No GPS discovery or production ranking model. |
| Feed pagination | Signed, viewer/query-bound snapshots; five-minute expiry; current visibility checked during hydration | Up to 200 posts and 200 public receipts per snapshot. Signing key is process-local; an API restart invalidates old cursors. |
| Communities | Browse, rules, rules revision check, join/leave, follow/unfollow, scoped conversations | Public/restricted communities only; creation, private communities, moderator management, and appeals remain pending. |
| Posts | Short updates, discussions, questions, owned revisions, approved public revision, deletion notice | Media, quote posts, polls, links, and resubmission of a restricted initial post remain pending. |
| Comments | Parent validation, ordered replies, pending revisions, edit/review, deletion with descendants retained | At most 200 visible comments per thread and depth 20. Branch pagination and accessibility of very large threads remain pending. |
| Social actions | Desired-state votes, bookmarks, reposts, follows, blocks, copied share links | Reposts maintain a relation/count; a separate repost activity feed and block-management screen remain pending. |
| Review | Staff queue, reasoned allow/restrict decisions, exact revision check, obsolete-review rejection | Human synthetic moderation only; user-initiated abuse reports, community moderation, appeals, and production moderation policy remain pending. |
| Private reports | Original statement, language label, category, landmark, publication preference, platform receipt, owner-only progress | Fictional public-service intake only. No protected reporting, contact details, media, or real agency submission. |
| Report retries | Principal-scoped command keys plus lifetime client-submission-ID deduplication and content conflict checks | Failed vault-to-app submission can leave an unused vault alias; retry reuses it. Automated alias cleanup remains pending. |
| Staff workflow | Coordinator assessment, urgency reason, agency proposal, acceptance, work, completion claim, independent verification | One restoration obligation created per case. Multi-agency dependency coordination, disputes, referrals, SLA clocks, and reopening after verified closure remain pending. |
| Public progress | Independently identified receipt, reviewed title/summary/broad area, fixed safe timeline wording, agency/task summaries, follow progress | Operational updates require a fresh explicit publication review. A report requesting private progress cannot be published. Withdrawal/redaction lifecycle remains pending. |
| UI | Desktop sidebars, mobile bottom navigation, 320px reflow, light/dark themes, compact density, loading/error/empty states, native modal focus containment, Escape dismissal | English interface. Unicode text and language labels are accepted; this does not imply evaluated multilingual search, translation, voice, or OCR. Full accessibility and assistive-technology audit remains pending. |
| Drafts | Explicitly opted-in device storage for post/report drafts, restore/discard, stable restored report ID | No server draft synchronization. Use fictional details on a trusted device. |
| Authentication | Random hashed HttpOnly session cookies, revocation, CSRF/origin checks, account-scoped cache clearing | Allowlisted synthetic accounts only. Startup rejects environment values other than `local`/`test`. OIDC, account provisioning, secure production cookies, and session administration remain pending. |
| Persistence | Canonical application/vault foundation migrations, enforcement triggers, transactional commands, generated SQL, outbox, post-stat projection | Canonical tables outside these workflows are scaffolding, not implemented features. Database least-privilege roles, RLS, encryption, auditing, and deployment isolation remain pending. |

## Run locally

Docker and Compose are sufficient for the complete application:

```bash
docker compose up --build -d
docker compose ps
```

The web app is at `http://localhost:3100`; API readiness is at `http://127.0.0.1:8081/health/ready`. The database is bound to loopback port 5438. Compose preserves data in the `jansetu-data` volume and seeds fixtures without overwriting ordinary user-created records.

For development, install Go 1.26, Node 22, Docker, and Python 3:

```bash
npm ci
make db
make seed
```

Then run `make api`, `make worker`, and `make web` in separate terminals. Backend defaults match [.env.example](../.env.example). Export environment variables before starting Go processes; the Go binaries do not automatically load `.env` files. Next.js reads its app-local environment file or exported variables. Compose explicitly supplies the internal service URLs.

The API server has graceful shutdown, bounded headers/body/request duration, liveness/readiness probes, safe error responses, and request IDs. Local HTTP requests use the configured `JANSETU_WEB_ORIGIN`, which defaults to `http://localhost:3100`. An alternative browser origin must be configured consistently for both API and web services.

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

| Location | Purpose |
|---|---|
| [Web app](../apps/web/src/components/jansetu.tsx) | Shell, navigation, sessions, feeds, search, themes |
| [Social UI](../apps/web/src/components/social.tsx) | Cards, composition, threads, communities |
| [Reporting UI](../apps/web/src/components/reports.tsx) | Private wizard, receipt, own progress |
| [Staff UI](../apps/web/src/components/studio.tsx) | Review, intake, agency work, verification, public preview |
| [BFF](../apps/web/src/app/api/[...path]/route.ts) | Server-only upstream address, cookies, mutation headers, body/origin bounds |
| [Go application](../services/backend/internal/app/http.go) | HTTP routes, synthetic sessions, policy checks, DTOs |
| [Transactions](../services/backend/internal/app/transactions.go) | Fresh authority checks, command receipt and outbox transaction |
| [Reports](../services/backend/internal/app/reports.go) | Separate vault aliases, intake, owner progress |
| [Cases](../services/backend/internal/app/cases.go) | Operational state machine and reviewed public projection |
| [Worker](../services/backend/internal/app/worker.go) | Lease fencing, deduplication, locked aggregate projection |
| [SQL queries](../services/backend/internal/store/queries.sql) | Generated `pgx` query inputs and results |
| [OpenAPI](../contracts/openapi/core.yaml) | Core REST contract and generated web DTOs |
| [Migrations](../db/migrations/00001_foundation.sql) | Versioned application foundation; vault migrations live separately |

Local mutations take a shared advisory lock before aggregate row locks. This intentionally serializes pilot commands to keep the first implementation deterministic. It is a throughput limit, and must become ordered aggregate locking before load testing or a multi-city rollout. Workers lock the relevant post before counting current votes/comments/reposts, preventing an older count snapshot from overwriting a newer one.

The API owns both database pools in this local milestone. Separate database names alone do not enforce a production vault security boundary. Database credentials in the example are synthetic local credentials. No production hosting, credentials, models, or integrations are configured.

## Verification

```bash
make check
make test-integration
make generate
npx playwright install chromium
npm run test:e2e
```

`make check` validates the specification documents, Go vet/unit tests, TypeScript, and the production web build. Integration tests create randomly named temporary application/vault databases, apply migrations and fixtures, then remove those test databases. They do not reset the running application volume. Integration tests require a local database role capable of creating test databases.

Go integration checks cover concurrent create retries, conflicting keys/content, unapproved revision privacy, obsolete review decisions, stale versions, desired-state votes, durable bookmarks, blocking, cross-post replies, parent tombstones, report ownership, lifetime report deduplication, original case age, authority boundaries, independent verification, private-public projection isolation, grant revocation, and stale worker leases. Go race detection is enabled in `make test-integration`.

Browser checks use real API/database workflows for social publication and interactions, private report → triage → agency claim → independent verification → reviewed publication, cross-origin rejection, another resident's private-report denial, search, membership, theme persistence, modal dismissal, and mobile overflow. Screenshots, traces, and the HTML report are local artifacts under ignored `test-results` / `playwright-report` directories.

The [CI workflow](../.github/workflows/core.yml) repeats these checks and verifies generated SQL/types have no drift. Passing these focused checks does not constitute all 68 canonical acceptance contracts, a load test, a full accessibility audit, or a security review.

## Next implementation milestones

1. Replace synthetic sessions with OIDC and scoped provisioning; introduce least-privilege database roles/RLS, vault encryption and purpose-limited audit; complete permission and privacy adversarial tests.
2. Add private object storage, constrained upload sessions, scanning/redaction, real evidence provenance, OCR/vision adapters, per-language evaluations, and reviewable correction flows.
3. Complete multi-agency obligations, mandates/geography, responsibility disputes, escalation/SLA clocks, notification delivery, and production agency integrations.
4. Finish social post kinds, branches/pagination at scale, public profiles, community administration, abuse reports/appeals, block settings, multilingual UI, and Expo mobile clients.
5. Replace local serialization and bounded search/ranking, then run capacity, accessibility, recovery, and production pilot acceptance gates.
