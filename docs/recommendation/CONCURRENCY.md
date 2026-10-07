# First aggregate concurrency migration

Vote/repost count projections now serialize on their post row instead of the pilot-wide mutation lock. A blocked post does not hold up count projection for a different post. The durable outbox lease, processed-event insertion, post count rebuild and acknowledgement still commit together.

## Reviewed write set

The explicit allowlist is `PostVoteChanged` and `PostRepostChanged`, payload version 1, POST aggregate, positive aggregate version and nonnil aggregate ID. The worker rechecks that the leased row still matches that allowlist before taking the path. A changed envelope cannot enter notification delivery without its ordering lock.

Each allowed projection locks its claimed outbox row, inserts its event-dedup row, locks the post, rebuilds that post's statistics, and acknowledges its lease. The count statement reads votes, comments and reposts but writes only `social.post_stats`; its foreign key points to the already locked post. It neither inserts notifications nor locks recipient profiles, community membership or civic receipts. The post lock remains held before the count snapshot, preserving consistency with commands and other workers for the same aggregate.

Other projections acquire the existing pilot ordering lock before row locks. Reply/review/civic notifications may acquire recipient-profile locks after a post lock, whereas commands acquire their actor-profile lock before the post. Removing the common boundary for those paths without changing both lock orders creates a cycle. API command serialization and civic graph invariants remain migration work.

## Verification

`TestIndependentPostStatsProjectWithoutPilotSerialization` holds the pilot lock and pauses a real vote-stat insertion while the worker holds the first post. A second worker projects and acknowledges a different post's repost count during that pause. Releasing the gate lets the first worker finish with the correct vote count. The existing reply-projection/command deadlock regression also passes, together with worker replay and compatibility tests.

Run `JANSETU_INTEGRATION=1 go test -race ./internal/app -run 'TestIndependentPostStats|TestReplyProjectionAndCommandDoNotDeadlock|TestWorker' -count=1` from `services/backend` against the isolated integration harness. This proves independent aggregate progress and retained count correctness; it is not a production throughput claim. The full civic/API lock migration still requires aggregate-by-aggregate audits and representative load evidence.
