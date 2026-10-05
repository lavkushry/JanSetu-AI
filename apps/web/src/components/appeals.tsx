'use client';
import Link from 'next/link';
import { useState } from 'react';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Scale } from 'lucide-react';
import { api, dateLabel, readable, type Schema } from '@/lib/api';
import { refreshSocialVisibility } from '@/lib/social-cache';
import { Badge, Empty, ErrorState, FormError, Loading, Modal, useSession } from './ui';

const count = (s: string) => [...s.trim()].length;
const states: Record<string, string> = {
  OPEN: 'Awaiting reviewer',
  REVIEWING: 'Independent review',
  UPHELD: 'Decision upheld',
  REVERSED: 'Decision overturned',
  WITHDRAWN: 'Appeal withdrawn',
};
const restoration: Record<Schema['AppealOutcome']['restorationReason'], string> = {
  NONE: 'The exact reviewed revision can be published after current rules are checked again.',
  TARGET_CHANGED: 'The source has a later revision. This appeal cannot publish that edit.',
  TARGET_UNAVAILABLE: 'The source or thread is unavailable, or another removal still applies.',
  POSTING_NOT_ALLOWED: 'The author no longer has permission to post in this community.',
  PARENT_UNAVAILABLE: 'The parent reply is unavailable to the author.',
};
export function AppealControl({ decision }: { decision: Schema['AuthorModerationDecision'] }) {
  const [open, setOpen] = useState(false);
  if (decision.action === 'ALLOW') return null;
  if (decision.appeal)
    return (
      <Link className="text-button" href={`/account/appeals/${decision.appeal.id}`}>
        {states[decision.appeal.state]} · View appeal
      </Link>
    );
  return (
    <>
      <button className="secondary small" onClick={() => setOpen(true)}>
        Appeal decision
      </button>
      {open && <AppealForm decision={decision} close={() => setOpen(false)} />}
    </>
  );
}
function AppealForm({
  decision,
  close,
}: {
  decision: Schema['AuthorModerationDecision'];
  close: () => void;
}) {
  const [grounds, setGrounds] = useState('');
  const [key, setKey] = useState(() => crypto.randomUUID());
  const qc = useQueryClient();
  const { notify } = useSession();
  const submit = useMutation({
    mutationFn: () =>
      api<Schema['AppealReceipt']>(`moderation/decisions/${decision.id}/appeals`, {
        method: 'POST',
        key,
        body: { grounds: grounds.trim() },
      }),
    onSuccess: async () => {
      notify('Your private appeal is received. An independent reviewer can assess it.');
      close();
      await refreshSocialVisibility(qc);
    },
  });
  return (
    <Modal title="Appeal this decision" onClose={() => !submit.isPending && close()}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit.mutate();
        }}
      >
        <p>
          {decision.target.type} · Revision {decision.target.revision}
        </p>
        <p>{decision.reason}</p>
        <p className="muted">
          Explain why the decision should change. Your appeal is shared with an independent platform
          moderator. Each decision has one appeal; submission does not change publication.
        </p>
        <label>
          Appeal grounds
          <textarea
            rows={5}
            required
            value={grounds}
            maxLength={1000}
            disabled={submit.isPending}
            onChange={(e) => {
              setGrounds(e.target.value);
              setKey(crypto.randomUUID());
            }}
          />
        </label>
        <small>{count(grounds)} / 1,000 characters · At least 5</small>
        <FormError error={submit.error} />
        <div className="form-actions">
          <button className="secondary" type="button" disabled={submit.isPending} onClick={close}>
            Cancel
          </button>
          <button
            className="primary"
            disabled={submit.isPending || count(grounds) < 5 || count(grounds) > 1000}
          >
            {submit.isPending ? 'Submitting…' : 'Submit appeal'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
export function MyAppeals() {
  const { me } = useSession();
  const qc = useQueryClient();
  const q = useInfiniteQuery({
    queryKey: ['appeals', me?.profile.id],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['AppealPage']>(
        `me/appeals${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (p) => p.nextCursor || undefined,
    enabled: !!me,
    refetchInterval: 30000,
  });
  const refresh = () => void qc.resetQueries({ queryKey: ['appeals'] });
  const items = q.data?.pages.flatMap((p) => p.items) || [];
  return (
    <section className="account-panel" id="appeals" aria-labelledby="appeals-heading">
      <h2 id="appeals-heading">
        <Scale size={19} /> My appeals
      </h2>
      <p className="muted">
        Private independent reviews of your restrictions and removals. An overturned decision
        restores content only when the exact revision still meets current rules.
      </p>
      <button className="text-button" onClick={refresh} disabled={q.isFetching}>
        Refresh appeals
      </button>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={refresh} />
      ) : !items.length ? (
        <Empty title="No appeals yet">
          Use Appeal decision in your moderation history to request independent review.
        </Empty>
      ) : (
        <div className="content-report-list">
          {items.map((v) => (
            <PrivateAppealCard key={v.id} appeal={v} link />
          ))}
          {q.hasNextPage && (
            <button
              className="secondary small"
              disabled={q.isFetchingNextPage}
              onClick={() => void q.fetchNextPage()}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Load more appeals'}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
export function AppealQueue() {
  const { me } = useSession();
  const qc = useQueryClient();
  const q = useInfiniteQuery({
    queryKey: ['appeal-review', me?.profile.id],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['AppealReviewPage']>(
        `moderation/appeals${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (p) => p.nextCursor || undefined,
    enabled: !!me?.roles.includes('PLATFORM_MODERATOR'),
  });
  const refresh = () => void qc.resetQueries({ queryKey: ['appeal-review'] });
  const items = q.data?.pages.flatMap((p) => p.items) || [];
  return (
    <section className="staff-stack" aria-labelledby="appeal-review-heading">
      <div className="staff-card">
        <h2 id="appeal-review-heading">Independent appeals</h2>
        <p>
          Reviewers must differ from the author, original decision maker and original reporter. Take
          a review before recording its outcome.
        </p>
        <button className="text-button" onClick={refresh} disabled={q.isFetching}>
          Refresh appeal queue
        </button>
      </div>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={refresh} />
      ) : !items.length ? (
        <Empty title="No eligible appeals">
          Only independent, unassigned reviews and your own claims appear here.
        </Empty>
      ) : (
        items.map((v) => (
          <AppealReviewCard
            key={`${v.id}-${v.version}-${v.targetVersion}`}
            item={v}
            refresh={refresh}
          />
        ))
      )}
      {q.hasNextPage && (
        <button
          className="secondary"
          disabled={q.isFetchingNextPage}
          onClick={() => void q.fetchNextPage()}
        >
          Load more appeal reviews
        </button>
      )}
    </section>
  );
}
function AppealReviewCard({
  item: v,
  refresh,
}: {
  item: Schema['AppealReview'];
  refresh: () => void;
}) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [reason, setReason] = useState('');
  const [confirm, setConfirm] = useState<'UPHELD' | 'REVERSED' | null>(null);
  const action = useMutation({
    mutationFn: (result: 'CLAIM' | 'UPHELD' | 'REVERSED') =>
      api<Schema['AppealReceipt']>(
        `moderation/appeals/${v.id}/${result === 'CLAIM' ? 'claim' : 'decisions'}`,
        {
          method: 'POST',
          version: v.version,
          ...(result === 'CLAIM'
            ? {}
            : {
                body: {
                  result,
                  authorReason: reason.trim(),
                  targetRevision: v.original.target.revision,
                  targetVersion: v.targetVersion,
                },
              }),
        },
      ),
    onSuccess: async (_, result) => {
      setConfirm(null);
      notify(result === 'CLAIM' ? 'Appeal assigned to you.' : 'Appeal outcome recorded.');
      await refreshSocialVisibility(qc);
    },
  });
  const valid = count(reason) >= 5 && count(reason) <= 1000;
  return (
    <article className="staff-card" data-testid={`appeal-review-${v.id}`}>
      <div className="receipt-eyebrow">
        <span>
          {v.original.target.type} · REVISION {v.original.target.revision}
        </span>
        <Badge state={v.state} />
      </div>
      <h3>
        {readable(v.original.action)} · Policy {v.original.ruleVersion}
      </h3>
      <small>{dateLabel(v.createdAt)}</small>
      <h4>Author’s appeal</h4>
      <p className="appeal-copy">{v.grounds}</p>
      <details>
        <summary>Original internal review note · staff only</summary>
        <p className="appeal-copy">{v.original.internalReason}</p>
      </details>
      <h4>Exact reviewed revision</h4>
      {v.preview ? (
        <div className="appeal-preview">
          {v.preview.title && <strong>{v.preview.title}</strong>}
          <p className="appeal-copy">{v.preview.body}</p>
        </div>
      ) : (
        <p className="muted">Source preview unavailable under current rules.</p>
      )}
      <p>{restoration[v.restorationReason]}</p>
      <FormError error={action.error} />
      {action.error && (
        <button className="text-button" onClick={refresh} disabled={action.isPending}>
          Refresh before deciding again
        </button>
      )}
      {v.state === 'OPEN' ? (
        <button
          className="primary"
          disabled={action.isPending}
          onClick={() => action.mutate('CLAIM')}
        >
          Take review
        </button>
      ) : (
        v.assignedToMe && (
          <>
            <label>
              Reason shared with author
              <textarea
                rows={4}
                maxLength={1000}
                value={reason}
                disabled={action.isPending}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
            <p className="muted">
              Explain the result without including reporter identities or private complaint details.
            </p>
            <div className="form-actions">
              <button
                className="secondary"
                disabled={!valid || action.isPending}
                onClick={() => setConfirm('UPHELD')}
              >
                Uphold decision
              </button>
              <button
                className="primary"
                disabled={!valid || action.isPending}
                onClick={() => setConfirm('REVERSED')}
              >
                Overturn decision
              </button>
            </div>
          </>
        )
      )}
      {confirm && (
        <Modal
          title={confirm === 'UPHELD' ? 'Uphold this decision?' : 'Overturn this decision?'}
          onClose={() => !action.isPending && setConfirm(null)}
        >
          <p>
            {confirm === 'UPHELD'
              ? 'The original publication decision remains in effect.'
              : v.restorationReason === 'NONE'
                ? 'The exact reviewed revision will be published if it still meets current rules when this decision is saved.'
                : `The original decision will be overturned, but content will remain unavailable. ${restoration[v.restorationReason]}`}
          </p>
          <p className="appeal-copy">{reason}</p>
          <FormError error={action.error} />
          {action.error && (
            <button
              className="text-button"
              disabled={action.isPending}
              onClick={() => {
                setConfirm(null);
                refresh();
              }}
            >
              Refresh review context
            </button>
          )}
          <div className="form-actions">
            <button
              className="secondary"
              disabled={action.isPending}
              onClick={() => setConfirm(null)}
            >
              Cancel
            </button>
            <button
              className="primary"
              disabled={action.isPending}
              onClick={() => action.mutate(confirm)}
            >
              {action.isPending ? 'Saving…' : 'Confirm appeal outcome'}
            </button>
          </div>
        </Modal>
      )}
    </article>
  );
}

export function PrivateAppealCard({
  appeal: v,
  link = false,
}: {
  appeal: Schema['AppealReceipt'];
  link?: boolean;
}) {
  return (
    <article className="content-report-receipt" data-testid={`appeal-${v.id}`}>
      <div className="receipt-eyebrow">
        <span>APPEAL {v.id.slice(0, 8)}</span>
        <Badge state={v.state} />
      </div>
      <h3>{states[v.state]}</h3>
      <p className="appeal-copy">{v.grounds}</p>
      <small>{dateLabel(v.createdAt)}</small>
      {link && (
        <Link className="text-button" href={`/account/appeals/${v.id}`}>
          Open private appeal
        </Link>
      )}
      {v.outcome && (
        <div className="appeal-outcome">
          <strong>Reviewer’s explanation</strong>
          <p className="appeal-copy">{v.outcome.reason}</p>
          <p>
            {v.outcome.restorationState === 'RESTORED'
              ? 'Reviewed revision published.'
              : v.outcome.restorationState === 'UNCHANGED'
                ? 'Publication remains unchanged.'
                : `Content was not restored. ${restoration[v.outcome.restorationReason]}`}
          </p>
        </div>
      )}
    </article>
  );
}
