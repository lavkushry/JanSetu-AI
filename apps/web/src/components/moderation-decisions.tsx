'use client';
import Link from 'next/link';
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { ShieldCheck } from 'lucide-react';
import { api, dateLabel, type Schema } from '@/lib/api';
import { Badge, Empty, ErrorState, Loading, useSession } from './ui';

export function MyModerationDecisions() {
  const { me } = useSession();
  const qc = useQueryClient();
  const q = useInfiniteQuery({
    queryKey: ['moderation-decisions', me?.profile.id],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['AuthorModerationDecisionPage']>(
        `me/moderation-decisions${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!me,
    refetchInterval: 30000,
  });
  const items = q.data?.pages.flatMap((p) => p.items) || [];
  const refresh = () => void qc.resetQueries({ queryKey: ['moderation-decisions'] });
  return (
    <section
      className="account-panel"
      id="moderation-decisions"
      aria-labelledby="moderation-decisions-heading"
    >
      <h2 id="moderation-decisions-heading">
        <ShieldCheck size={19} /> Moderation decisions
      </h2>
      <p className="muted">
        Private decisions about your posts and replies. A rejected initial submission can be edited
        and resubmitted from its thread. Published content that was removed stays unavailable.
      </p>
      <button type="button" className="text-button" onClick={refresh} disabled={q.isFetching}>
        Refresh moderation decisions
      </button>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={refresh} />
      ) : !items.length ? (
        <Empty title="No moderation decisions">
          Decisions will appear here after a moderator reviews your content.
        </Empty>
      ) : (
        <div className="content-report-list">
          {items.map((d) => (
            <article
              key={d.id}
              className="content-report-receipt"
              data-testid={`moderation-decision-${d.id}`}
            >
              <div className="receipt-eyebrow">
                <span>
                  {d.target.type} · REVISION {d.target.revision}
                </span>
                <Badge
                  state={
                    d.action === 'ALLOW'
                      ? 'APPROVED'
                      : d.action === 'REMOVE'
                        ? 'REMOVED'
                        : 'RESTRICTED'
                  }
                />
              </div>
              <h3>
                {d.action === 'ALLOW'
                  ? 'Approved for publication'
                  : d.action === 'REMOVE'
                    ? 'Content removed'
                    : 'Revision restricted'}
              </h3>
              <p>{d.reason}</p>
              <small>
                {dateLabel(d.decidedAt)} · Policy {d.ruleVersion}
              </small>
              <Link
                className="text-button"
                href={`/posts/${d.target.postId}${d.target.type === 'COMMENT' ? `#comment-${d.target.id}` : ''}`}
              >
                Open current thread
              </Link>
            </article>
          ))}
          {q.hasNextPage && (
            <button
              className="secondary small"
              disabled={q.isFetchingNextPage}
              onClick={() => void q.fetchNextPage()}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Load more moderation decisions'}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
