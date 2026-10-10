# Negative feedback before candidate limits

Less and Skip already removed posts at final hydration, but they still consumed candidate slots on fresh Recommended requests. If the newest 1,000 posts had been dismissed, recent retrieval could select only hidden posts and return an empty page despite older eligible inventory. The same problem affected the followed/joined and interest/locality sources.

## Fresh retrieval

The [candidate query](../../services/backend/internal/app/recommendation_feed.go) materializes distinct suppressed post IDs from the authenticated viewer's retained Less/Skip events in the current consent generation. The eligible relation excludes them before the 500 followed/joined, 500 interest/locality and 1,000 recent source limits. Selection, published-body hashing and ranking then operate on at most 2,000 remaining candidates. The snapshot still contains at most 200 ranked social references, and composition retains the 20-item page, two-post author cap and existing civic allocation.

Suppression follows the existing final-hydration contract:

- It requires behavioral personalization and the current generation. Anonymous, nonconsenting, foreign-owner and prior-generation events do not suppress the viewer's posts. Restricted-role owner RLS remains in effect on both ledger tables.
- Less/Skip hide the post across republished revisions. They do not mute its creator or community; sibling posts remain eligible. More retains its separate revision-aware, 30-day interest behavior.
- READ, SATISFIED and DISSATISFIED do not hide posts. Usefulness answers remain diagnostic feedback.
- An accepted retained dismissal continues to apply after its exposure submission window expires. There is no new retrieval-specific age limit. Existing event-age retention cleanup, preference changes, withdrawal and history reset continue to govern the ledger. Reset preserves chosen preferences and clears dismissals.

The same eligible pool feeds ranked, shadow and chronological fallback snapshots. Following, Latest and Top keep their existing behavior.

## Live snapshots and races

Early filtering improves fresh retrieval but cannot replace final checks. A viewer can submit Less in another session after candidate selection, or load a cursor created before that feedback. Final hydration still rereads current-generation Less/Skip events inside the consent/generation transaction and skips suppressed posts before issuing served exposures. This check also applies to Redis snapshot hits.

Existing snapshots keep frozen references and ordering. A cursor can advance through remaining eligible references, but it cannot retrieve new IDs or rerank. Refresh creates a new snapshot from the updated eligible pool. Limited eligible inventory, author diversity or concurrent visibility changes can still produce a shorter page.

## Regression proof

The [candidate-budget regression](../../services/backend/internal/app/recommendation_suppression_integration_test.go) creates 1,050 unique approved posts by 25 followed authors in a chosen-interest community. The newest 1,000 have retained Less/Skip history from prior sessions, covering all three source budgets. The 50 older posts reach ranking, fill the first 20-item page and are served exactly once across frozen cursor pages in ranked, shadow, disabled and ranker-outage scenarios. Hidden posts receive no new exposures. A real history reset restores the newest 1,000 to candidate selection. When a Rust test target is configured, the ranked/shadow cases use the production Go gRPC client and Rust service.

The [restricted-role retrieval test](../../services/backend/internal/app/recommendation_retrieval_integration_test.go) checks owner, consent and generation isolation, post-scoped dismissal after republication, unaffected sibling posts and usefulness answers, active/expired mutes and history reset. A separate two-session test commits Less after retrieval and before ranking returns, proving that final hydration still excludes the post and issues no additional exposure for it.

The [browser proof](../../tests/e2e/recommendations.spec.ts) records Less through the real UI, verifies removal of the dismissed card and a refilled 20-item response, and checks that the private account summary includes the dismissal. Existing usefulness conflict, retry, reset, withdrawal and Following scenarios remain in the same flow. Run `make recommendation-browser-proof` against its disposable fixture; [validation results](VALIDATION.md) record executed checks.

## Scope and remaining limits

This change adds one statement-local ledger relation and an eligibility predicate. It introduces no schema, public API, ranking-weight or rollout change. PostgreSQL remains authoritative; Redis feature observations remain shadow-only. The relation reads retained owner history and can add query memory/work for viewers with large ledgers. Eligible inventory still spans matching posts before source limits. This correctness proof does not measure latency improvement, representative capacity or recommendation quality. The default deployment remains shadow with zero rollout.
