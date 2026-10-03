# JanSetu-AI

JanSetu AI combines local communities and social discussion with accountable civic reporting, case ownership, evidence, and progress tracking. The planned interface draws on Reddit communities and conversations and X-style updates and timelines, with OCR and image recognition to assist reporting.

This repository currently contains product and implementation specifications. Application code, trained models, integrations, and production validation are still to be built.

The selected stack is Next.js/React/TypeScript for web, React Native/Expo for mobile, **Go** for APIs and workers, PostgreSQL/PostGIS with `pgx`/`sqlc` and Goose migrations, Redis, private S3-compatible storage, and a separate Python/FastAPI AI service. Exact tool versions and deployment providers will be pinned during implementation.

Start with the [specification suite](docs/spec/README.md) and [domain glossary](CONTEXT.md).

| Document | What it defines |
|---|---|
| [BRD](docs/spec/BRD.md) | Business outcomes, stakeholders, operating model, costs and pilot measures |
| [PRD](docs/spec/PRD.md) | Product scope, 53 functional requirements, states, permissions and 14 nonfunctional requirements |
| [System design](docs/spec/SYSTEM_DESIGN.md) | Go module boundaries, deployment, trust topology, geography, capacity and recovery |
| [UI implementation](docs/spec/UI_IMPLEMENTATION.md) | Reddit/X-inspired screens, themes, responsive layouts, client state, accessibility and language catalog |
| [Backend implementation](docs/spec/BACKEND_IMPLEMENTATION.md) | Reference SQL, REST contracts, transactions, outbox workers, media, OCR/vision and integration behavior |
| [Traceability and delivery](docs/spec/TRACEABILITY_AND_DELIVERY.md) | Requirement mapping, 37 UX scenarios, 68 acceptance contracts, phased backlog and release gates |

The first pilot is a city/district. The full product ships in phases. Indian-language support has independent UI, search, voice, OCR and translation readiness; these documents do not claim evaluated models or live agency integrations.

- [Earlier consolidated blueprint](JanSetu_AI_Detailed_Blueprint.md): preserved design snapshot, superseded by the specification suite.
- [Earlier experience plan](docs/PRODUCT_EXPERIENCE_PLAN.md): preserved input, superseded by the UI and traceability documents.
- [Reference repository research](docs/REFERENCE_RESEARCH.md): source-level findings from CampusFix, RoadLens, the OCR notebooks, BlackVision, and ArmMind.
- [Adjudication and enforcement design](JanSetu_AI_Blueprint_v1_Adjudication_and_Enforcement.md): responsibility disputes, continuity, and escalation.

OCR means reading text from images. The cloud provider and exact OCR/vision engines remain open choices to resolve using pilot requirements and evaluation.

Run `python3 scripts/validate_specs.py` to check local Markdown links/anchors, JSON examples, requirement coverage and selected cross-document invariants. See [documentation validation](docs/spec/TRACEABILITY_AND_DELIVERY.md#documentation-validation) for the additional SQL and Mermaid checks. These checks validate the specifications, not application readiness.
