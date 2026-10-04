# JanSetu-AI

JanSetu AI combines local communities and social discussion with accountable civic reporting, case ownership, evidence, and progress tracking. The planned interface draws on Reddit communities and conversations and X-style updates and timelines, with OCR and image recognition to assist reporting.

The repository now includes a runnable first-release core: a responsive community web app, persistent Go API, OIDC sign-in and account security, restricted database roles and a separate vault service, PostgreSQL/PostGIS migrations, review queues, private service reports, and an agency-to-verifier workflow. This milestone runs with explicitly synthetic local accounts and agencies. It does not enable real public intake or claim full P0 completion.

Implemented stack: **Go 1.26**, Next.js 16.3.8, React 19.3, TypeScript, TanStack Query, PostgreSQL 18/PostGIS, `pgx` v5, `sqlc`, Goose, Keycloak/OIDC, and a durable Go projection worker. React Native/Expo, Redis, private object storage, and the separate Python/FastAPI AI service remain planned modules.

Run the local application with Docker Compose:

```bash
docker compose up --build -d
```

Open [JanSetu locally](http://localhost:3100), then open the avatar menu and continue to sign in through local Keycloak. Fixture usernames and the fictional password are shown in the menu. Ananya and Rohan are residents; Kiran reviews posts and triages reports; City Works accepts and performs tasks; Neha independently verifies completion. Kiran publishes reviewed public progress separately.

The database uses a named volume. `docker compose down` stops the application while preserving its data. See the [implementation runbook](docs/IMPLEMENTATION_STATUS.md) for development commands, checks, workflow details, and remaining work. The [database/vault guide](docs/DATABASE_VAULT_ISOLATION.md) explains restricted roles, encrypted ownership, upgrade steps, and remaining privacy gates. The [account security guide](docs/ACCOUNT_SECURITY.md) explains OIDC configuration, private provisioning, public profile settings, and session revocation.

Start with the [specification suite](docs/spec/README.md) and [domain glossary](CONTEXT.md).

| Document                                                            | What it defines                                                                                         |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| [BRD](docs/spec/BRD.md)                                             | Business outcomes, stakeholders, operating model, costs and pilot measures                              |
| [PRD](docs/spec/PRD.md)                                             | Product scope, 53 functional requirements, states, permissions and 14 nonfunctional requirements        |
| [System design](docs/spec/SYSTEM_DESIGN.md)                         | Go module boundaries, deployment, trust topology, geography, capacity and recovery                      |
| [UI implementation](docs/spec/UI_IMPLEMENTATION.md)                 | Reddit/X-inspired screens, themes, responsive layouts, client state, accessibility and language catalog |
| [Backend implementation](docs/spec/BACKEND_IMPLEMENTATION.md)       | Reference SQL, REST contracts, transactions, outbox workers, media, OCR/vision and integration behavior |
| [Traceability and delivery](docs/spec/TRACEABILITY_AND_DELIVERY.md) | Requirement mapping, 37 UX scenarios, 68 acceptance contracts, phased backlog and release gates         |

The first pilot is a city/district. The full product ships in phases. Indian-language support has independent UI, search, voice, OCR and translation readiness; these documents do not claim evaluated models or live agency integrations.

- [Earlier consolidated blueprint](JanSetu_AI_Detailed_Blueprint.md): preserved design snapshot, superseded by the specification suite.
- [Earlier experience plan](docs/PRODUCT_EXPERIENCE_PLAN.md): preserved input, superseded by the UI and traceability documents.
- [Reference repository research](docs/REFERENCE_RESEARCH.md): source-level findings from CampusFix, RoadLens, the OCR notebooks, BlackVision, and ArmMind.
- [Adjudication and enforcement design](JanSetu_AI_Blueprint_v1_Adjudication_and_Enforcement.md): responsibility disputes, continuity, and escalation.

OCR means reading text from images. The [private photo/OCR guide](docs/PRIVATE_MEDIA_OCR.md) describes local uploads and experimental English text review. The [resumable upload guide](docs/RESUMABLE_PHOTO_UPLOADS.md) covers progress, retry and recovery after reload. The [recognition guide](docs/PRIVATE_IMAGE_RECOGNITION.md) describes opt-in CPU object candidates and private overlays. Civic-hazard detection, cloud storage and evaluated multilingual OCR remain pending.

Run `python3 scripts/validate_specs.py` to check local Markdown links/anchors, JSON examples, requirement coverage and selected cross-document invariants. See [documentation validation](docs/spec/TRACEABILITY_AND_DELIVERY.md#documentation-validation) for the additional SQL and Mermaid checks. These checks validate the specifications, not application readiness.
