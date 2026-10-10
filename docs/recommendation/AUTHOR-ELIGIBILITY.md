# Active authors in candidate retrieval

Candidate retrieval now checks that a post's author belongs to the active profile set, using an `IN` predicate wrapped in `IS TRUE`. The wrapper keeps membership inside the eligibility predicate instead of allowing PostgreSQL to flatten it into an inventory/profile join. Authorless civic updates remain excluded from social candidates; the separate labeled civic section retains its existing allocation and ordering.

This addresses a measured planner problem. An isolated PostgreSQL 18.6 `EXPLAIN (ANALYZE, BUFFERS)` on 4,096 generated posts, 1,024 authors and 12 viewers estimated 19 eligible rows but observed 3,855. The old author join compared those posts against 1,042 active profiles, removing 4,017,219 nonmatching join pairs. The membership form built a hashed active-profile subplan once and avoided that cross product. A sequential diagnostic observed SQL execution of 711.574 ms before and 148.023 ms after, including EXPLAIN instrumentation and planner-selected JIT work. This is a local query-plan observation, not a complete-feed capacity result.

Both queries use the same restricted application role and statement snapshot. The profile read policy and all publication, community, language, review, repost-source, block/mute, consent/history and feature predicates remain in force. Source limits, chronological tie ordering, the 2,000-candidate ceiling and the published-body hash budget are unchanged. No migration, permission grant, ranker policy or rollout setting changes.

## Correctness

The restricted-role author test compares every ordered candidate and feature against the frozen pre-optimization query in one statement. It checks active, suspended, deactivated and authorless posts; hidden posts and rejected revisions; private, frozen and restricted communities; blocks in both directions; explicit person/community follows and active membership; reposts of active and suspended sources; language/community filters; authenticated, foreign-session and anonymous scopes; and personalization on/off. Explicit graph relations retain their existing public read semantics. Reactivating authors makes their posts and eligible reposts available in the next statement.

The existing 3,500-post disjoint-source parity test continues to compare all features and candidate order, with and without language filters, and verifies the 2,000 hash evaluation ceiling. The final-hydration race test now suspends an author after metadata selection and before body hydration. Both posts from that author disappear, and neither receives an exposure.

## Operating limits

Active-profile membership is statement-local, with no cross-request cache. PostgreSQL chooses whether to hash that set; sufficiently large sets or different memory/statistics settings can produce another plan. The active profile scan, matching-post inventory scan and eligible-body temporary storage remain scaling prerequisites. Representative graph sizes, memory/spill diagnostics and arrival-rate complete-feed load tests are still required by the [capacity gate](BENCHMARKS.md#full-api-release-workload). Synthetic parity and latency observations do not validate recommendation quality or million-user capacity. The deployed pilot is unchanged.
