'use client';
import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import Link from 'next/link';
import { api, APIError, dateLabel, readable, type Schema } from '@/lib/api';
import { Badge, FormError, Modal, useSession } from './ui';

export function PublicationReview({ caseDetail: c }: { caseDetail: Schema['CaseDetail'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const publication = c.publication;
  const [title, setTitle] = useState(publication?.title || '');
  const [safe, setSafe] = useState(publication?.summary || '');
  const [area, setArea] = useState(publication?.area || '');
  const [reason, setReason] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const [base, setBase] = useState({
    caseVersion: c.version,
    publicationVersion: publication?.version || 0,
  });
  const [withdrawing, setWithdrawing] = useState(false);
  const [withdrawReason, setWithdrawReason] = useState('');
  const [withdrawReviewed, setWithdrawReviewed] = useState(false);
  const stale =
    c.version > base.caseVersion || (publication?.version || 0) > base.publicationVersion;
  const refresh = useMutation({
    mutationFn: () => api<Schema['CaseDetail']>(`authority/cases/${c.id}`),
    onSuccess: (latest) => {
      qc.setQueryData(['staff-case', c.id], latest);
      setBase({
        caseVersion: latest.version,
        publicationVersion: latest.publication?.version || 0,
      });
      setReviewed(false);
      setWithdrawReviewed(false);
      command.reset();
    },
    onError: () => void qc.resetQueries({ queryKey: ['staff-case', c.id] }),
  });
  const command = useMutation({
    mutationFn: (withdraw: boolean) =>
      api<Schema['PublicationResult']>(
        `authority/cases/${c.id}/${withdraw ? 'publication-withdrawals' : 'publications'}`,
        {
          method: 'POST',
          version: base.caseVersion,
          body: withdraw
            ? {
                publicationVersion: base.publicationVersion,
                reason: withdrawReason,
                reviewed: withdrawReviewed,
              }
            : {
                title,
                summary: safe,
                area,
                reason,
                reviewed,
                publicationVersion: base.publicationVersion,
              },
        },
      ),
    onSuccess: async (result) => {
      const publicKeys = ['feed', 'search', 'receipt', 'activity', 'activity-summary'];
      const affected = {
        predicate: (q: { queryKey: readonly unknown[] }) =>
          publicKeys.includes(String(q.queryKey[0])),
      };
      await qc.cancelQueries(affected);
      await qc.resetQueries(affected);
      setBase({ caseVersion: result.version, publicationVersion: result.publicationVersion });
      setReviewed(false);
      setReason('');
      setWithdrawing(false);
      setWithdrawReason('');
      setWithdrawReviewed(false);
      await qc.invalidateQueries();
      notify(
        result.state === 'WITHDRAWN'
          ? 'Public progress withdrawn. Case work continues.'
          : 'Reviewed public progress saved.',
      );
    },
    onError: (error) => {
      if (error instanceof APIError && error.code === 'PUBLICATION_PRIVATE')
        void qc.invalidateQueries({ queryKey: ['staff-case', c.id] });
      else if (error instanceof APIError && [401, 403, 404].includes(error.status))
        void qc.resetQueries({ queryKey: ['staff-case', c.id] });
    },
  });
  const busy = command.isPending || refresh.isPending;
  const conflict = command.error instanceof APIError && [409, 412].includes(command.error.status);
  return (
    <div className="staff-card publication" data-testid="publication-review">
      <h3>Review public progress</h3>
      <p className="muted">
        Public progress is reviewed separately from case work. Keep names, exact private locations,
        and the original statement out of the public preview.
      </p>
      {publication ? (
        <div className="public-preview" data-testid="current-publication">
          <div className="receipt-eyebrow">
            <span>PUBLICATION REVISION {publication.version}</span>
            <Badge state={publication.state} />
          </div>
          <h3>{publication.title}</h3>
          <p>{publication.summary}</p>
          <small>
            {publication.area} · Public snapshot of case revision {publication.caseVersion}
          </small>
          {publication.state === 'PUBLISHED' ? (
            <Link className="text-button" href={`/cases/${publication.receiptId}`}>
              Open current public progress
            </Link>
          ) : (
            <p role="status">
              Public progress is withdrawn. Private reports and agency work continue. Publishing
              again requires a fresh review.
            </p>
          )}
        </div>
      ) : (
        <p>No public progress has been published.</p>
      )}
      <div className="form-actions">
        <button className="secondary small" disabled={busy} onClick={() => refresh.mutate()}>
          Refresh publication review
        </button>
        {publication?.state === 'PUBLISHED' && (
          <button
            className="text-button danger"
            disabled={busy || stale}
            onClick={() => {
              command.reset();
              setWithdrawReviewed(false);
              setWithdrawing(true);
            }}
          >
            Withdraw public progress
          </button>
        )}
      </div>
      {(stale || conflict) && (
        <p className="form-error" role="alert">
          The case or public progress changed. Refresh the review, compare the current preview with
          your draft, and confirm it again. Your draft is kept.
        </p>
      )}
      {c.publicationBlocked && (
        <p role="status">
          Public sharing is paused by a private preference or withdrawal request. Private case work
          continues.
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          command.mutate(false);
        }}
      >
        <label>
          Public title
          <input
            required
            minLength={5}
            maxLength={180}
            value={title}
            onChange={(e) => {
              setTitle(e.target.value);
              setReviewed(false);
            }}
          />
        </label>
        <label>
          Safe public summary
          <textarea
            required
            minLength={10}
            maxLength={1500}
            rows={4}
            value={safe}
            onChange={(e) => {
              setSafe(e.target.value);
              setReviewed(false);
            }}
          />
        </label>
        <label>
          Broad public area
          <input
            required
            minLength={3}
            maxLength={80}
            value={area}
            onChange={(e) => {
              setArea(e.target.value);
              setReviewed(false);
            }}
          />
        </label>
        <div className="public-preview">
          <span className="eyebrow">PUBLIC PREVIEW</span>
          <h3>{title || 'Public title'}</h3>
          <p>{safe || 'Your reviewed summary will appear here.'}</p>
          <small>
            {area || 'Broad area'} · {readable(c.state)}
          </small>
        </div>
        <label>
          Private publication reason
          <textarea
            required
            minLength={5}
            maxLength={1000}
            rows={2}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
        <small>
          This reason stays in publisher review history and is never shown on the public card.
        </small>
        <label className="checkbox">
          <input
            required
            type="checkbox"
            checked={reviewed}
            onChange={(e) => setReviewed(e.target.checked)}
          />
          I reviewed this public preview for identifying details.
        </label>
        <div className="form-actions">
          <button
            className="primary"
            disabled={busy || stale || conflict || !reviewed || c.publicationBlocked}
          >
            {publication?.state === 'WITHDRAWN'
              ? 'Republish reviewed progress'
              : publication
                ? 'Save reviewed correction'
                : 'Publish reviewed progress'}
          </button>
        </div>
      </form>
      <FormError error={withdrawing ? null : command.error || refresh.error} />
      {!!publication?.decisions.length && (
        <div className="publication-history">
          <h3>Publication decisions</h3>
          <p className="muted">Private publisher history · newest 20 decisions</p>
          <div className="timeline">
            {publication.decisions.map((d) => (
              <div className="timeline-item" key={d.id}>
                <span className="timeline-dot" />
                <div>
                  <strong>
                    {readable(d.action)} ·{' '}
                    {d.publicationVersion === null
                      ? 'Legacy review'
                      : `Publication revision ${d.publicationVersion}`}
                  </strong>
                  <p>{d.reason || 'Legacy review: no separate publication reason was recorded.'}</p>
                  <small>
                    Case revision {d.caseVersion} · {dateLabel(d.decidedAt)}
                  </small>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {withdrawing && (
        <Modal
          title="Withdraw public progress?"
          onClose={() => {
            if (!busy) setWithdrawing(false);
          }}
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              command.mutate(true);
            }}
          >
            <p>
              This removes the public card, timeline, search results and service notifications.
              Existing followers stay subscribed for future reviewed progress. Private reports and
              case work continue.
            </p>
            <label>
              Private withdrawal reason
              <textarea
                required
                minLength={5}
                maxLength={1000}
                rows={3}
                value={withdrawReason}
                onChange={(e) => setWithdrawReason(e.target.value)}
                autoFocus
              />
            </label>
            <label className="checkbox">
              <input
                required
                type="checkbox"
                checked={withdrawReviewed}
                onChange={(e) => setWithdrawReviewed(e.target.checked)}
              />
              I reviewed the withdrawal of this public progress.
            </label>
            <FormError error={command.error} />
            {conflict && (
              <p>
                Your reason is kept. Close this dialog and refresh the publication review before
                confirming again.
              </p>
            )}
            <div className="form-actions">
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => setWithdrawing(false)}
              >
                Keep public progress
              </button>
              <button
                className="primary danger"
                disabled={busy || stale || !withdrawReviewed || conflict}
              >
                {command.isPending ? 'Withdrawing…' : 'Confirm withdrawal'}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
