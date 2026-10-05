'use client';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ShieldCheck } from 'lucide-react';
import { api, APIError, type Schema } from '@/lib/api';
import { AuthorDecisionCard } from './moderation-decisions';
import { PrivateAppealCard } from './appeals';
import { Empty, ErrorState, Loading, useSession } from './ui';

export function PrivateReviewRecord({
  id,
  kind,
}: {
  id: string;
  kind: 'MODERATION_DECISION' | 'APPEAL';
}) {
  const { me, signIn } = useSession();
  const appeal = kind === 'APPEAL';
  const root = appeal ? 'appeals' : 'moderation-decisions';
  const record = useQuery({
    queryKey: [root, me?.profile.id || 'guest', id],
    queryFn: () =>
      api<Schema['AuthorModerationDecision'] | Schema['AppealReceipt']>(`me/${root}/${id}`),
    enabled: !!me,
    refetchInterval: 30000,
  });
  if (!me)
    return (
      <Empty title="Private review record">
        <p>
          Sign in to view your own decision or appeal. These records are private to their owner.
        </p>
        <button className="primary" onClick={signIn}>
          Sign in
        </button>
      </Empty>
    );
  const unavailable = record.error instanceof APIError && record.error.status === 404;
  return (
    <section className="account-page" aria-labelledby="private-review-record-heading">
      <Link className="back-link" href={`/account#${root}`}>
        <ArrowLeft size={16} /> Back to your account
      </Link>
      <div className="section-intro">
        <span className="eyebrow">
          <ShieldCheck size={15} /> PRIVATE ACCOUNT RECORD
        </span>
        <h1 id="private-review-record-heading">
          {appeal ? 'Appeal outcome' : 'Moderation decision'}
        </h1>
        <p>
          This is your private record. Current thread links follow the thread’s current visibility
          rules.
        </p>
        <button
          className="secondary small"
          disabled={record.isFetching}
          onClick={() => void record.refetch()}
        >
          Refresh record
        </button>
      </div>
      {record.isPending ? (
        <Loading />
      ) : unavailable ? (
        <Empty title="This private record is unavailable">
          <p>Use your account history to open a record that belongs to you.</p>
          <Link className="text-button" href={`/account#${root}`}>
            Open my history
          </Link>
        </Empty>
      ) : record.error ? (
        <ErrorState error={record.error} retry={() => void record.refetch()} />
      ) : (
        record.data && (
          <div className="account-panel">
            {'target' in record.data ? (
              <AuthorDecisionCard decision={record.data} />
            ) : (
              <PrivateAppealCard appeal={record.data} />
            )}
          </div>
        )
      )}
    </section>
  );
}
