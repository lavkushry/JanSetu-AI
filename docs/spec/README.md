# JanSetu AI — Specification Suite

Version: 3.0. Date: 3 October 2026. Status: canonical documentation for implementation.

JanSetu combines useful local social participation with accountable service reporting and case coordination. The first pilot is a city/district; the full product is delivered in phases. Go is the selected backend. Hosting is provider-neutral. Support for Indian languages is tracked independently across UI, search, voice, and OCR.

## Reading order and ownership

| Order | Document | Canonical responsibility |
|---|---|---|
| 1 | [Domain glossary](../../CONTEXT.md) | Meaning of project terms |
| 2 | [BRD](BRD.md) | Business outcomes, stakeholders, economics, operating scope |
| 3 | [PRD](PRD.md) | Product behavior, requirements, permissions, release priorities |
| 4 | [System design](SYSTEM_DESIGN.md) | Module ownership, deployment, trust/data topology, capacity |
| 5 | [UI implementation](UI_IMPLEMENTATION.md) | Visual and interaction contracts, routes, frontend behavior |
| 6 | [Backend implementation](BACKEND_IMPLEMENTATION.md) | Storage, API fields, errors, transactions, jobs, AI contracts |
| 7 | [Traceability and delivery](TRACEABILITY_AND_DELIVERY.md) | Requirements-to-tests mapping, milestones, release evidence |

Product semantics come from PRD; wire and database shapes come from backend implementation; visual presentation comes from UI implementation. A technical optimization cannot change product behavior. Resolve contradictions by updating the responsible document and its dependent contracts in the same review, rather than silently choosing an interpretation.

## Decisions and configurable inputs

| Decision | Value |
|---|---|
| Product | Reddit-style communities/conversations plus X-style updates and timelines connected to civic cases |
| First pilot | One city/district, two or three agencies, approximately ten locality communities |
| Delivery | Full vision with P0/P1/P2 phases and independent protected-intake readiness |
| Backend | Go, net/http, pgx/sqlc, Goose, PostgreSQL/PostGIS, Redis, durable outbox workers |
| Clients | Next.js/React/TypeScript web and React Native/Expo mobile; shared tokens/contracts |
| Media/AI | Private S3-compatible storage; separate Python/FastAPI task adapters |
| Language target | Extensible Indian-language catalog with per-capability release evidence |
| Infrastructure | Provider-neutral containers, private data services, infrastructure as code |

Pilot locality, agency rosters, signed clocks/calendars, launch language packs, identity provider, cloud account, production secrets, and evaluated model artifacts are deployment inputs. Defaults and synthetic examples in this suite support development; they do not imply a real agency has accepted obligations or a model has passed evaluation.

## Related evidence and earlier documents

- [Reference repository research](../REFERENCE_RESEARCH.md): exact inspected commits and source-level findings.
- [Adjudication companion](../../JanSetu_AI_Blueprint_v1_Adjudication_and_Enforcement.md): original dispute model. Canonical clock interpretation is in the new backend/PRD documents.
- [Earlier consolidated blueprint](../../JanSetu_AI_Detailed_Blueprint.md): preserved historical design snapshot, superseded by this suite.
- [Earlier experience plan](../PRODUCT_EXPERIENCE_PLAN.md): preserved design input, superseded by UI implementation and the traceability document.

## Sources

First-party technical sources support capability descriptions, not JanSetu performance or accuracy claims. Repository evidence is pinned in the reference report. Product rules, priorities, scores, limits, and targets are JanSetu design decisions. Recheck version-specific dependencies before implementation.

| Ref | Primary source | Use in this document |
|---|---|---|
| S1 | [Reddit — Reddiquette](https://support.reddithelp.com/hc/en-us/articles/205926439-Reddiquette) | Public discussion and voting conventions |
| S2 | [X — Repost FAQs](https://help.x.com/en/using-x/repost-faqs) | Repost and quote interaction reference |
| S3 | [W3C — WCAG 2.2](https://www.w3.org/TR/WCAG22/) | Accessibility target |
| S4 | [Go — net/http](https://pkg.go.dev/net/http) | Go HTTP server and handler foundation |
| S5 | [Next.js — App Router documentation](https://nextjs.org/docs/app) | Website routing foundation |
| S6 | [Expo — Router introduction](https://docs.expo.dev/router/introduction/) | Native routing foundation |
| S7 | [Government of India — Local Government Directory](https://lgdirectory.gov.in/) | Administrative identifiers and source inventory |
| S8 | [PostGIS — ST_Covers](https://postgis.net/docs/ST_Covers.html) | Boundary-inclusive spatial predicate |
| S9 | [PostgreSQL — Row security policies](https://www.postgresql.org/docs/current/ddl-rowsecurity.html) | RLS behavior and bypass constraints |
| S10 | [OWASP — Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) | Authorization review reference |
| S11 | [IETF — RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) | OAuth security baseline |
| S12 | [MeitY — Digital Personal Data Protection Act, 2023](https://www.meity.gov.in/static/uploads/2024/06/2bf1f0e9f04e6fb4f8fef35e82c42aa5.pdf) | Counsel's data-protection review input |
| S13 | [MeitY — Digital Personal Data Protection Rules, 2025](https://www.meity.gov.in/static/uploads/2025/11/53450e6e5dc0bfa85ebd78686cadad39.pdf) | Staged commencement and counsel questions |
| S14 | [MeitY — Intermediary Rules consolidated text with 2026 amendments](https://www.meity.gov.in/static/uploads/2026/02/550681ab908f8afb135b0ad42816a1c9.pdf) | Social-platform legal review input |
| S15 | [MHA — Parliamentary response on BNSS and e-Zero FIR, 28 July 2026](https://www.mha.gov.in/MHA1/Par2017/pdfs/par2026-pdfs/LS28072026/135.pdf) | Counsel's official-channel and terminology review |
| S16 | [CVC — Complaint Handling Policy](https://cvc.gov.in/uploads/pdfs/pdf-1772375911000-530170809.pdf) | Jurisdiction and PIDPI review starting point; document indicates a 2019 revision, so counsel must verify current procedure |
| S17 | [pgx v5 documentation](https://pkg.go.dev/github.com/jackc/pgx/v5) | PostgreSQL driver and explicit transaction helpers |
| S18 | [sqlc — Using transactions](https://docs.sqlc.dev/en/v1.31.1/howto/transactions.html) | Generated queries bound to the command transaction |
| S19 | [Goose](https://github.com/pressly/goose) | SQL migration tooling |
| S20 | [go-oidc](https://github.com/coreos/go-oidc) | OIDC login integration; application authorization remains explicit |
| S21 | [Next.js — Server and Client Components](https://nextjs.org/docs/app/getting-started/server-and-client-components) | Web rendering and interactivity boundaries |
| S22 | [Expo — Navigation](https://docs.expo.dev/router/basics/navigation/) | Native navigation primitives |
| S23 | [TanStack Query — Optimistic Updates](https://tanstack.com/query/latest/docs/framework/react/guides/optimistic-updates) | Selected server-state/mutation library |
| S24 | [Department of Official Language — Eighth Schedule inventory](https://rajbhasha.gov.in/en/languages-included-eighth-schedule-indian-constitution) | Initial language inventory; capability readiness requires separate evidence |
