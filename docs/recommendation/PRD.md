# Recommendation product requirements

## Objective

Help residents consistently find conversations worth their time, discover useful communities and creators, and return satisfied. Initial inventory is JanSetu's eligible published social revisions and sanitized public civic receipts. Community discovery and video follow their own delivery milestones. Engagement supports usefulness; raw reading duration, follower totals and reports are not universal quality scores.

## Baseline behavior

The implemented social rule is the existing specification:

`relevance = 0.35 × explicitInterest + 0.25 × locality + 0.20 × relationship + 0.10 × freshness + 0.10 × boundedUsefulness`

All features are finite in [0,1]. Explicit interest matches selected community slugs or consented More feedback for the same community (same creator for standalone posts). Locality matches a selected geographic community slug/title. Relationship means followed person/community or active membership. Freshness is `1/(1+max(ageDays,0))`. Bounded usefulness is `clamp((upCount-downCount)/20,0,1)`, preserving the documented hypothesis with limited influence. Policy and model versions are internal and frozen in snapshots; all weights require pilot validation. Civic urgency has no social vote component.

Retrieve up to 500 followed/joined candidates, 500 explicit-interest/locality candidates and 1,000 recent candidates; merge and deduplicate into at most 2,000. Language tags are exact selected BCP-47 tags; blank means all languages, not a claim of language model readiness. Rank to 200. Deduplicate identical published body hashes and canonical published conversation roots, so quoted branches and their source share one slot. Media deduplication remains gated on social media publishing. The existing release does not yet publish social images or videos; private report media never supplies social features.

Each returned 20-item page has at most two posts by one author. Excess candidates are skipped, so low-diversity inventory can produce shorter pages. Home without a community filter reserves up to six initial slots for eligible unresolved public receipts in the selected coarse locality (all local pilot receipts if unset); fill remaining slots with social posts, then more receipts if social supply is short. Receipts carry `CIVIC_UPDATES` and urgency explanations. Civic cards preserve urgency-first, oldest-report-first snapshot order, independent of engagement. If there is insufficient civic inventory, use available social content. Community feeds have no civic allocation.

Following is chronological, including when the client supplies recommended/top. Latest and Top remain available through the original API. Nearby, Unresolved and Resolved retain their existing contracts; recommended for these modes delegates to their deterministic ordering. Following's combined civic/social interleave is inherited from the existing implementation; a unified chronological stream is a separate compatibility change.

## Controls

- Why this? returns an allowlisted code and a plain explanation of explicit interest, locality, relationship, recency or civic urgency. No private attribute or hidden reputation score is exposed.
- Authenticated users choose community interests, exact language tags and a coarse public locality label. The initial locality mapping accepts geographic community slugs/titles and exact public receipt area labels; canonical locality IDs are needed for multi-city serving.
- Behavioral personalization defaults off. Explicit interests/follows/locality remain usable without behavioral consent. Nonconsenting and anonymous feeds create no behavioral exposures or interaction history.
- More influences future recommendations; existing snapshot order remains stable. Less and Skip remove that post from subsequent recommended responses in the current generation, including already-created snapshots.
- Creator/community mute and block controls apply at final hydration. Latest/Top and saved items retain their documented explicit-access behavior.
- Reset advances the history generation and deletes personal exposure/events and snapshots, preserving chosen preferences. Any preference change advances generation and clears history. Withdrawal disables behavioral use immediately.

## Quality and promotion

Primary: randomly sampled session usefulness/satisfaction. Secondary: seven/28-day retention, useful saves/follows, creator discovery and repetition. Evaluate new users, languages, localities and creator sizes separately. Post SATISFIED/DISSATISFIED feedback is diagnostic, not the primary randomized session survey.

The Python evaluator uses viewer-cluster bootstrap confidence intervals so repeat sessions are not independent users. It checks a positive satisfaction confidence bound and predeclared two-percentage-point guardrails for negative feedback and retention. The initial minimum of 50 viewers per arm/cohort is only an evidence floor; experiment power analysis must determine sample sizes and these margins must be agreed before running a pilot. No unmeasured cohort is silently promoted. The evaluator alone cannot approve a model.

Learned retrieval/ranking requires temporal holdouts, serving/training feature parity, exposure-aware evaluation and a tested runtime/artifact pair. Predict satisfaction, useful saves/follows, constructive participation and negative feedback separately. Duration must be normalized by format/length and foreground activity, with duplicate events excluded. Short-term session and long-term preferences remain separate. Introduce 10% eligible social exploration only with logged conditional selection probabilities and measured repetition/creator-size guardrails. Never infer successful learning or superiority from synthetic fixtures.

## Correctness and capacity gates

Release blockers: unauthorized exposure, private feature contamination, stale consent generations, revision confusion and bypassed publication revocations/blocks. Test cold starts, reset, withdrawal, account deactivation, cursor replay/expiry, duplicate and manipulated events, outages and invalid model responses.

One million daily users × 20 requests/day × 20 peak multiplier yields ~4,630 peak requests/s; benchmark toward 10,000 complete-feed requests/s. Targets are recommendation p95 <150 ms and full API p95 <500 ms. Publish dataset, hardware, concurrency, error rate, cost per thousand feed requests and cost per million events. These are goals until measured under representative load.
