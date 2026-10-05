'use client';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ShieldCheck } from 'lucide-react';
import { api, APIError, type Schema } from '@/lib/api';
import { AuthorDecisionCard } from './moderation-decisions';
import { PrivateAppealCard } from './appeals';
import { PrivateContentReportCard } from './content-reports';
import { Empty, ErrorState, Loading, useSession } from './ui';

type PrivateRecord =
  | { kind: 'MODERATION_DECISION'; value: Schema['AuthorModerationDecision'] }
  | { kind: 'APPEAL'; value: Schema['AppealReceipt'] }
  | { kind: 'CONTENT_REPORT'; value: Schema['ContentReport'] };

export function PrivateReviewRecord({ id, kind }: { id: string; kind: PrivateRecord['kind'] }) {
  const { me, signIn } = useSession();
  const appeal = kind === 'APPEAL';
  const report = kind === 'CONTENT_REPORT';
  const root = report ? 'content-reports' : appeal ? 'appeals' : 'moderation-decisions';
  const record = useQuery({
    queryKey: [root, me?.profile.id || 'guest', id],
    queryFn: async (): Promise<PrivateRecord> => {
      const path = `me/${root}/${id}`;
      if (kind === 'CONTENT_REPORT')
        return { kind, value: await api<Schema['ContentReport']>(path) };
      if (kind === 'APPEAL') return { kind, value: await api<Schema['AppealReceipt']>(path) };
      return { kind, value: await api<Schema['AuthorModerationDecision']>(path) };
    },
    enabled: !!me,
    refetchInterval: 30000,
  });
  if (!me)
    return (
      <Empty title="Private review record">
        <p>
          Sign in to view your own decision, appeal or content report. These records are private to
          their owner.
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
          {report ? 'Content report outcome' : appeal ? 'Appeal outcome' : 'Moderation decision'}
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
            {record.data.kind === 'CONTENT_REPORT' ? (
              <PrivateContentReportCard report={record.data.value} />
            ) : record.data.kind === 'MODERATION_DECISION' ? (
              <AuthorDecisionCard decision={record.data.value} />
            ) : (
              <PrivateAppealCard appeal={record.data.value} />
            )}
          </div>
        )
      )}
    </section>
  );
}
