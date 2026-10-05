'use client';
import Link from 'next/link';
import { useState } from 'react';
import { createPortal } from 'react-dom';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Flag, ShieldCheck, CheckCircle2 } from 'lucide-react';
import { api, dateLabel, type Schema } from '@/lib/api';
import { refreshSocialVisibility } from '@/lib/social-cache';
import { Badge, Empty, ErrorState, FormError, Loading, Modal, useSession } from './ui';

const reasons: Record<Schema['ContentReportReason'], string> = {
  SPAM: 'Spam or misleading promotion',
  HARASSMENT: 'Targeted harassment',
  HATE: 'Hateful content',
  THREATS: 'Threats of harm',
  PRIVACY: 'Private information',
  MISINFORMATION: 'Misleading information',
  OTHER: 'Another concern',
};
type ReportTarget = {
  type: 'POST' | 'COMMENT';
  id: string;
  revision: number;
  label: string;
};
const characterCount = (value: string) => Array.from(value.trim()).length;

export function ContentReportControl({
  menu = false,
  ...target
}: ReportTarget & { menu?: boolean }) {
  const { me, signIn } = useSession();
  const [snapshot, setSnapshot] = useState<ReportTarget | null>(null);
  return (
    <>
      <button
        type="button"
        className={menu ? undefined : 'text-button'}
        onClick={() => (me ? setSnapshot({ ...target }) : signIn())}
      >
        {!menu && <Flag size={14} />} Report {target.type === 'POST' ? 'post' : 'comment'}
      </button>
      {/* A post menu closes on selection; keep the dialog outside that subtree. */}
      {snapshot &&
        createPortal(
          <ReportDialog target={snapshot} close={() => setSnapshot(null)} />,
          document.body,
        )}
    </>
  );
}
function ReportDialog({ target, close }: { target: ReportTarget; close: () => void }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [reason, setReason] = useState<Schema['ContentReportReason']>('SPAM');
  const [details, setDetails] = useState('');
  const [key, setKey] = useState(() => crypto.randomUUID());
  const submit = useMutation({
    mutationFn: () =>
      api<Schema['ContentReportReceipt']>('content-reports', {
        method: 'POST',
        key,
        body: {
          targetType: target.type,
          targetId: target.id,
          targetRevision: target.revision,
          reasonCode: reason,
          details,
        },
      }),
    onSuccess: async (saved) => {
      await qc.invalidateQueries({ queryKey: ['content-reports'] });
      notify(
        saved.state === 'DECIDED'
          ? 'Your existing report has a recorded outcome'
          : 'Content report received for review',
      );
    },
  });
  return (
    <Modal
      title={
        submit.data
          ? submit.data.state === 'DECIDED'
            ? 'Report outcome available'
            : 'Content report received'
          : `Report this ${target.type === 'POST' ? 'post' : 'comment'}?`
      }
      onClose={() => !submit.isPending && close()}
    >
      {submit.data ? (
        <div className="content-report-received" role="status">
          <CheckCircle2 size={30} />
          <p>
            {submit.data.state === 'DECIDED'
              ? 'You already reported this published revision. View the recorded outcome in your existing private receipt.'
              : 'Your report is private. A moderator can review it; submitting a report does not automatically remove content.'}
          </p>
          <small>
            Receipt {submit.data.id.slice(0, 8)} · {dateLabel(submit.data.createdAt)}
          </small>
          <Link className="primary" href="/account#content-reports" onClick={close}>
            View my content reports
          </Link>
          <button className="secondary" type="button" onClick={close}>
            Done
          </button>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit.mutate();
          }}
        >
          <p>
            <strong>{target.label}</strong> · Published revision {target.revision}
          </p>
          <p className="muted">
            Tell us what concerns you about this content. Your report is shared with platform
            moderators; your identity and report are not shown to the author or other residents.
          </p>
          <label>
            Report reason
            <select
              value={reason}
              disabled={submit.isPending}
              onChange={(e) => {
                setReason(e.target.value as Schema['ContentReportReason']);
                setKey(crypto.randomUUID());
              }}
            >
              {Object.entries(reasons).map(([code, label]) => (
                <option key={code} value={code}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Additional detail{reason === 'OTHER' ? ' (required)' : ' (optional)'}
            <textarea
              rows={4}
              maxLength={1000}
              required={reason === 'OTHER'}
              minLength={reason === 'OTHER' ? 5 : undefined}
              value={details}
              disabled={submit.isPending}
              onChange={(e) => {
                setDetails(e.target.value);
                setKey(crypto.randomUUID());
              }}
            />
          </label>
          <FormError error={submit.error} />
          <div className="form-actions">
            <button className="secondary" type="button" disabled={submit.isPending} onClick={close}>
              Cancel
            </button>
            <button
              className="primary"
              disabled={submit.isPending || (reason === 'OTHER' && characterCount(details) < 5)}
            >
              {submit.isPending ? 'Submitting…' : 'Submit content report'}
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}

function useContentReportPages(review: boolean) {
  const { me } = useSession();
  return useInfiniteQuery({
    queryKey: ['content-reports', review ? 'queue' : 'mine', me?.profile.id || 'guest'],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['ContentReportPage']>(
        `${review ? 'moderation' : 'me'}/content-reports${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!me && (!review || me.roles.includes('PLATFORM_MODERATOR')),
    refetchInterval: 30000,
  });
}
function SourcePreview({ report }: { report: Schema['ContentReport'] }) {
  return report.target ? (
    <div className="content-report-source">
      <small>
        Published revision {report.targetRevision} · {report.target.authorName}
      </small>
      {report.target.title && <strong>{report.target.title}</strong>}
      <p className="post-body full">{report.target.body}</p>
      <Link
        className="text-button"
        href={`/posts/${report.target.postId}${report.targetType === 'COMMENT' ? `#comment-${report.targetId}` : ''}`}
      >
        Open current thread
      </Link>
    </div>
  ) : (
    <p className="muted">
      {report.targetState === 'CHANGED'
        ? 'The published revision has changed. This report retains its original revision; no replacement text is previewed.'
        : 'This content is unavailable. Your private receipt remains available.'}
    </p>
  );
}
export function MyContentReports() {
  const qc = useQueryClient();
  const q = useContentReportPages(false);
  const items = q.data?.pages.flatMap((p) => p.items) || [];
  const refresh = () => void qc.resetQueries({ queryKey: ['content-reports', 'mine'] });
  return (
    <section
      className="account-panel"
      id="content-reports"
      aria-labelledby="content-reports-heading"
    >
      <h2 id="content-reports-heading">
        <Flag size={19} /> Your content reports
      </h2>
      <p className="muted">
        Private receipts for posts and comments you reported. Service issue reports remain in
        Reports.
      </p>
      <button type="button" className="text-button" onClick={refresh} disabled={q.isFetching}>
        Refresh content reports
      </button>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={refresh} />
      ) : !items.length ? (
        <Empty title="No content reports">
          Report a published post from its options menu or a comment from its actions.
        </Empty>
      ) : (
        <div className="content-report-list">
          {items.map((r) => (
            <PrivateContentReportCard key={r.id} report={r} link />
          ))}
          {q.hasNextPage && (
            <button
              className="secondary small"
              disabled={q.isFetchingNextPage}
              onClick={() => void q.fetchNextPage()}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Load more content reports'}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
export function ContentReportQueue() {
  const qc = useQueryClient();
  const q = useContentReportPages(true);
  const items = q.data?.pages.flatMap((p) => p.items) || [];
  const refresh = () => void qc.resetQueries({ queryKey: ['content-reports', 'queue'] });
  return (
    <section className="content-report-review">
      <p className="staff-report-intro">
        <ShieldCheck size={18} /> Review the reported published revision. Reporter identities are
        withheld. Reports you filed or content you authored require another moderator.
      </p>
      <button type="button" className="text-button" onClick={refresh} disabled={q.isFetching}>
        Refresh reported content
      </button>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={refresh} />
      ) : !items.length ? (
        <Empty title="Reported content queue is clear">
          Published content reports eligible for your review appear here.
        </Empty>
      ) : (
        <div className="staff-stack">
          {items.map((r) => (
            <ContentReportReviewCard key={r.id} report={r} />
          ))}
          {q.hasNextPage && (
            <button
              className="secondary"
              disabled={q.isFetchingNextPage}
              onClick={() => void q.fetchNextPage()}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Load more reported content'}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
function ContentReportReviewCard({ report: r }: { report: Schema['ContentReport'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [reason, setReason] = useState('');
  const [authorReason, setAuthorReason] = useState('');
  const [confirm, setConfirm] = useState(false);
  const action = useMutation({
    mutationFn: (choice: 'DISMISS' | 'REMOVE') =>
      api<Schema['ContentReportReceipt']>(`moderation/content-reports/${r.id}/decisions`, {
        method: 'POST',
        version: r.version,
        body: { action: choice, reason, authorReason, targetRevision: r.targetRevision },
      }),
    onSuccess: async (saved) => {
      setConfirm(false);
      await refreshSocialVisibility(qc);
      notify(
        saved.decision?.action === 'REMOVE'
          ? 'Content removed after review'
          : 'Content report dismissed',
      );
    },
  });
  const ready = characterCount(reason) >= 5 && !action.isPending;
  return (
    <article className="staff-card" data-testid={`content-report-review-${r.id}`}>
      <div className="receipt-eyebrow">
        <span>
          {r.targetType} REPORT · REVISION {r.targetRevision}
        </span>
        <Badge state="PENDING" />
      </div>
      <h3>{reasons[r.reasonCode]}</h3>
      {r.details && <p className="content-report-grounds">{r.details}</p>}
      <small>Received {dateLabel(r.createdAt)}</small>
      <SourcePreview report={r} />
      <label>
        Decision reason
        <input
          minLength={5}
          maxLength={1000}
          value={reason}
          disabled={action.isPending}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Explain the community policy decision"
        />
      </label>
      <label>
        Reason shared with author
        <textarea
          maxLength={1000}
          value={authorReason}
          disabled={action.isPending}
          onChange={(e) => setAuthorReason(e.target.value)}
          placeholder="Explain the policy violation to the author"
        />
        <small>
          Required to remove. Keep reporter identities and private complaint details out.
        </small>
      </label>
      <FormError error={action.error} />
      <div className="form-actions">
        <button
          className="secondary"
          type="button"
          disabled={!ready}
          onClick={() => action.mutate('DISMISS')}
        >
          Dismiss report
        </button>
        <button
          className="danger"
          type="button"
          disabled={!ready || characterCount(authorReason) < 5 || r.targetState !== 'AVAILABLE'}
          onClick={() => setConfirm(true)}
        >
          Remove reported content
        </button>
      </div>
      {confirm && (
        <Modal
          title="Remove reported content?"
          onClose={() => !action.isPending && setConfirm(false)}
        >
          <p>
            This removes the reported {r.targetType === 'POST' ? 'post' : 'comment'} from public
            access. A pending edit cannot republish it. Replies to a removed comment retain their
            own visibility.
          </p>
          <p>
            <strong>Decision reason</strong>
            <br />
            {reason}
          </p>
          <p>
            <strong>Reason shared with author</strong>
            <br />
            {authorReason}
          </p>
          <FormError error={action.error} />
          <div className="form-actions">
            <button
              className="secondary"
              type="button"
              disabled={action.isPending}
              onClick={() => setConfirm(false)}
            >
              Cancel
            </button>
            <button
              className="danger"
              type="button"
              disabled={action.isPending}
              onClick={() => action.mutate('REMOVE')}
            >
              {action.isPending ? 'Saving…' : 'Confirm removal'}
            </button>
          </div>
        </Modal>
      )}
    </article>
  );
}

export function PrivateContentReportCard({
  report: r,
  link = false,
}: {
  report: Schema['ContentReport'];
  link?: boolean;
}) {
  return (
    <article className="content-report-receipt" data-testid={`content-report-${r.id}`}>
      <div className="receipt-eyebrow">
        <span>
          {r.targetType} · {dateLabel(r.createdAt)}
        </span>
        <Badge
          state={
            r.decision ? (r.decision.action === 'REMOVE' ? 'REMOVED' : 'DISMISSED') : 'PENDING'
          }
        />
      </div>
      {link && (
        <Link className="text-button" href={`/account/content-reports/${r.id}`}>
          Open private report receipt
        </Link>
      )}
      <h3>{reasons[r.reasonCode]}</h3>
      {r.details && <p>{r.details}</p>}
      <SourcePreview report={r} />
      {r.decision && (
        <div className="content-report-outcome">
          <strong>{r.decision.action === 'REMOVE' ? 'Content removed' : 'Report dismissed'}</strong>
          <p>{r.decision.reason}</p>
          <small>{dateLabel(r.decision.decidedAt)}</small>
        </div>
      )}
    </article>
  );
}
