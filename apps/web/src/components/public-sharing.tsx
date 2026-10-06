'use client';
import Link from 'next/link';
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, APIError, dateLabel, readable, type Schema } from '@/lib/api';
import { Badge, Empty, ErrorState, FormError, Loading, Modal, useSession } from './ui';

type Request = Schema['PublicationWithdrawalRequest'];
type Review = Schema['PublicationWithdrawalReview'];
const concerns: Record<Schema['PublicationWithdrawalReason'], string> = {
  PRIVACY: 'The public preview could identify someone',
  LOCATION: 'The public area reveals too much location detail',
  SHARING_PREFERENCE: 'I want to change public sharing',
};
export function PublicSharingControl({ reportId }: { reportId: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button className="text-button" onClick={() => setOpen(true)}>
        Manage public sharing
      </button>
      {open && <ResidentSharing reportId={reportId} close={() => setOpen(false)} />}
    </>
  );
}
function ResidentSharing({ reportId, close }: { reportId: string; close: () => void }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const path = `my-reports/${reportId}`;
  const q = useQuery({
    queryKey: ['owned-public-sharing', reportId],
    queryFn: () => api<Schema['OwnerPublicSharing']>(`${path}/public-sharing`),
  });
  const [draft, setDraft] = useState<{
    version: number;
    code: Schema['PublicationWithdrawalReason'];
    client: string;
    key: string;
    confirmed: boolean;
  } | null>(null);
  const [cancel, setCancel] = useState<Request | null>(null);
  const [cancelConfirmed, setCancelConfirmed] = useState(false);
  const resetProtected = (e: unknown) => {
    if (e instanceof APIError && [401, 403, 404].includes(e.status))
      void qc.resetQueries({ queryKey: ['owned-public-sharing', reportId] });
  };
  const refresh = useMutation({
    mutationFn: () => api<Schema['OwnerPublicSharing']>(`${path}/public-sharing`),
    onSuccess: (latest) => {
      qc.setQueryData(['owned-public-sharing', reportId], latest);
      if (draft && latest.publication)
        setDraft({
          ...draft,
          version: latest.publication.version,
          confirmed: false,
          ...(latest.publication.version !== draft.version
            ? { client: crypto.randomUUID(), key: crypto.randomUUID() }
            : {}),
        });
      if (cancel && !latest.requests.some((r) => r.id === cancel.id && r.state === 'REQUESTED'))
        setCancel(null);
      setCancelConfirmed(false);
      submit.reset();
      cancellation.reset();
    },
    onError: resetProtected,
  });
  const submit = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error('Review the current public progress');
      return api<Request>(`${path}/publication-withdrawal-requests`, {
        method: 'POST',
        key: draft.key,
        body: {
          clientRequestId: draft.client,
          publicationVersion: draft.version,
          reasonCode: draft.code,
          confirmed: draft.confirmed,
        },
      });
    },
    onSuccess: async (saved) => {
      setDraft(null);
      await qc.invalidateQueries();
      notify(
        saved.state === 'REQUESTED'
          ? 'Withdrawal request received for private review.'
          : 'Your request has a recorded status.',
      );
    },
    onError: resetProtected,
  });
  const cancellation = useMutation({
    mutationFn: () => {
      if (!cancel) throw new Error('Choose a pending request');
      return api<Request>(`${path}/publication-withdrawal-requests/${cancel.id}/cancellations`, {
        method: 'POST',
        version: cancel.version,
        body: { confirmed: cancelConfirmed },
      });
    },
    onSuccess: async () => {
      setCancel(null);
      setCancelConfirmed(false);
      await qc.invalidateQueries();
      notify('Request cancelled. Public progress is unchanged.');
    },
    onError: resetProtected,
  });
  const busy = submit.isPending || cancellation.isPending || refresh.isPending;
  const conflict = submit.error instanceof APIError && [409, 412].includes(submit.error.status);
  const stale = !!draft && q.data?.publication?.version !== draft.version;
  const existingPreviewRequest = q.data?.requests.find(
    (r) => r.publicationVersion === q.data?.publication?.version && r.state !== 'CANCELLED',
  );
  const refreshButton = (
    <button className="secondary small" disabled={busy} onClick={() => refresh.mutate()}>
      Refresh sharing review
    </button>
  );
  return (
    <Modal
      title={cancel ? 'Cancel withdrawal request?' : 'Manage public sharing'}
      onClose={() => !busy && close()}
    >
      <div className="publication resident-sharing" data-testid="resident-public-sharing">
        {q.isPending ? (
          <Loading />
        ) : q.error ? (
          <ErrorState error={q.error} retry={() => q.refetch()} />
        ) : cancel ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              cancellation.mutate();
            }}
          >
            <p>
              This cancels your pending review request. Current public progress stays visible.
              Private case work continues.
            </p>
            <label className="checkbox">
              <input
                type="checkbox"
                required
                checked={cancelConfirmed}
                onChange={(e) => setCancelConfirmed(e.target.checked)}
              />
              I want to cancel this pending request.
            </label>
            <FormError error={cancellation.error || refresh.error} />
            <div className="form-actions">
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => {
                  setCancel(null);
                  setCancelConfirmed(false);
                }}
              >
                Keep request
              </button>
              <button className="primary" disabled={busy || !cancelConfirmed}>
                Confirm cancellation
              </button>
            </div>
            {refreshButton}
          </form>
        ) : (
          <>
            <p>
              Your request is private. New public updates pause while a publisher reviews it.
              Current public progress stays visible until withdrawal is approved; private case work
              continues.
            </p>
            {q.data.publication ? (
              <div className="public-preview" data-testid="resident-public-preview">
                <span className="eyebrow">
                  CURRENT PUBLIC PROGRESS · REVISION {q.data.publication.version}
                </span>
                <h3>{q.data.publication.title}</h3>
                <p>{q.data.publication.summary}</p>
                <small>{q.data.publication.area}</small>
                <Link className="text-button" href={`/cases/${q.data.publication.receiptId}`}>
                  Open public progress
                </Link>
              </div>
            ) : (
              <p>No reviewed public progress is currently available for this report.</p>
            )}
            {refreshButton}
            <FormError error={refresh.error} />
            {!q.data.blocked && q.data.publication && !existingPreviewRequest && !draft && (
              <button
                className="primary"
                disabled={busy}
                onClick={() =>
                  setDraft({
                    version: q.data.publication!.version,
                    code: 'PRIVACY',
                    client: crypto.randomUUID(),
                    key: crypto.randomUUID(),
                    confirmed: false,
                  })
                }
              >
                Request withdrawal
              </button>
            )}
            {draft && q.data.publication && !q.data.blocked && !existingPreviewRequest && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  submit.mutate();
                }}
              >
                <label>
                  Reason for withdrawal
                  <select
                    value={draft.code}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        code: e.target.value as Schema['PublicationWithdrawalReason'],
                        confirmed: false,
                      })
                    }
                  >
                    {Object.entries(concerns).map(([code, label]) => (
                      <option key={code} value={code}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="muted">
                  {concerns[draft.code]}. No additional identifying details are needed.
                </p>
                <label className="checkbox">
                  <input
                    required
                    type="checkbox"
                    checked={draft.confirmed}
                    onChange={(e) => setDraft({ ...draft, confirmed: e.target.checked })}
                  />
                  I reviewed this public progress and want to request withdrawal.
                </label>
                {(stale || conflict) && (
                  <p role="alert" className="form-error">
                    Public progress or your request changed. Refresh the sharing review and confirm
                    it again. Your concern choice is kept.
                  </p>
                )}
                <FormError error={submit.error} />
                <button
                  className="primary"
                  disabled={busy || stale || conflict || !draft.confirmed}
                >
                  Submit withdrawal request
                </button>
              </form>
            )}
            {q.data.blocked && (
              <p role="status">
                New publication is paused by your sharing preference or withdrawal request.
              </p>
            )}
            {existingPreviewRequest?.state === 'DECLINED' && (
              <p>
                This public revision already has a reviewed request. A new request becomes available
                after public progress changes.
              </p>
            )}
            <h3>Your withdrawal requests</h3>
            {q.data.requests.length ? (
              q.data.requests.map((r) => (
                <article
                  className="publication-history"
                  key={r.id}
                  data-testid={`owned-withdrawal-${r.id}`}
                >
                  <Badge state={r.state} />
                  <p>{concerns[r.reasonCode]}</p>
                  <small>
                    Public revision {r.publicationVersion} · Requested {dateLabel(r.createdAt)}
                  </small>
                  {r.outcome && (
                    <>
                      <p>{r.outcome.reason}</p>
                      <small>Reviewed {dateLabel(r.outcome.decidedAt)}</small>
                    </>
                  )}
                  {r.state === 'REQUESTED' && (
                    <button
                      className="text-button"
                      disabled={busy}
                      onClick={() => {
                        setCancel(r);
                        setCancelConfirmed(false);
                      }}
                    >
                      Cancel request
                    </button>
                  )}
                </article>
              ))
            ) : (
              <p className="muted">You have no withdrawal requests.</p>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}

export function PublicationWithdrawalQueue() {
  const [state, setState] = useState<Review['state']>('REQUESTED');
  const [selected, setSelected] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['publication-withdrawal-queue', state],
    queryFn: () =>
      api<{ items: Review[] }>(`authority/publication-withdrawal-requests?state=${state}`),
  });
  return (
    <section className="staff-stack">
      <div className="staff-card section-intro">
        <h2>Public sharing requests</h2>
        <p>Review resident requests without opening private reports or stopping case work.</p>
        <label>
          Request state
          <select
            value={state}
            onChange={(e) => {
              setState(e.target.value as Review['state']);
              setSelected(null);
            }}
          >
            <option value="REQUESTED">Pending review</option>
            <option value="APPROVED">Approved</option>
            <option value="DECLINED">Declined</option>
            <option value="CANCELLED">Cancelled by resident</option>
          </select>
        </label>
        <button className="secondary small" onClick={() => q.refetch()}>
          Refresh sharing queue
        </button>
      </div>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => q.refetch()} />
      ) : (
        <>
          <div className="case-queue" aria-label="Public sharing requests">
            {q.data.items.length ? (
              q.data.items.map((v) => (
                <button
                  className={selected === v.id ? 'selected' : ''}
                  data-testid={`withdrawal-review-${v.id}`}
                  key={v.id}
                  onClick={() => setSelected(v.id)}
                >
                  <Badge state={v.state} />
                  <strong>{v.publication.title}</strong>
                  <small>
                    {concerns[v.reasonCode]} · {dateLabel(v.createdAt)}
                  </small>
                </button>
              ))
            ) : (
              <Empty title="No requests in this state">
                Choose another state or refresh the queue.
              </Empty>
            )}
          </div>
          {selected && <PublisherWithdrawalReview key={selected} id={selected} />}
        </>
      )}
    </section>
  );
}
function PublisherWithdrawalReview({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ['withdrawal-review', id],
    queryFn: () => api<Review>(`authority/publication-withdrawal-requests/${id}`),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return <WithdrawalDecision key={id} review={q.data} />;
}
function WithdrawalDecision({ review: v }: { review: Review }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [base, setBase] = useState({
    request: v.version,
    case: v.caseVersion,
    publication: v.publication.version,
  });
  const [result, setResult] = useState<'APPROVED' | 'DECLINED'>('APPROVED');
  const [internal, setInternal] = useState('');
  const [shared, setShared] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const path = `authority/publication-withdrawal-requests/${v.id}`;
  const command = useMutation({
    mutationFn: () =>
      api<Review>(`${path}/decisions`, {
        method: 'POST',
        version: base.request,
        body: {
          result,
          caseVersion: base.case,
          publicationVersion: base.publication,
          internalReason: internal,
          residentReason: shared,
          reviewed,
        },
      }),
    onSuccess: async (saved) => {
      qc.setQueryData(['withdrawal-review', v.id], saved);
      if (saved.state === 'APPROVED') {
        const prefixes = ['feed', 'search', 'receipt', 'activity', 'activity-summary'];
        const affected = {
          predicate: (q: { queryKey: readonly unknown[] }) =>
            prefixes.includes(String(q.queryKey[0])),
        };
        await qc.cancelQueries(affected);
        await qc.resetQueries(affected);
      }
      setReviewed(false);
      await qc.invalidateQueries();
      notify('Public sharing decision recorded. Private case work continues.');
    },
    onError: (e) => {
      if (e instanceof APIError && [401, 403, 404].includes(e.status))
        void qc.resetQueries({ queryKey: ['withdrawal-review', v.id] });
    },
  });
  const refresh = useMutation({
    mutationFn: () => api<Review>(path),
    onSuccess: (latest) => {
      qc.setQueryData(['withdrawal-review', v.id], latest);
      setBase({
        request: latest.version,
        case: latest.caseVersion,
        publication: latest.publication.version,
      });
      setReviewed(false);
      command.reset();
    },
    onError: (e) => {
      if (e instanceof APIError && [401, 403, 404].includes(e.status))
        void qc.resetQueries({ queryKey: ['withdrawal-review', v.id] });
    },
  });
  const busy = refresh.isPending || command.isPending;
  const stale =
    v.version > base.request ||
    v.caseVersion > base.case ||
    v.publication.version > base.publication;
  const conflict = command.error instanceof APIError && [409, 412].includes(command.error.status);
  return (
    <div className="staff-card publication" data-testid="publisher-withdrawal-review">
      <h3>Review withdrawal request</h3>
      <Badge state={v.state} />
      <p>{concerns[v.reasonCode]}</p>
      <small>
        Requested public revision {v.requestedPublicationVersion} · {dateLabel(v.createdAt)}
      </small>
      <div className="public-preview" data-testid="withdrawal-current-preview">
        <span className="eyebrow">
          CURRENT PUBLIC REVISION {v.publication.version} · {readable(v.publication.state)}
        </span>
        <h3>{v.publication.title}</h3>
        <p>{v.publication.summary}</p>
        <small>
          {v.publication.area} · Case revision {v.caseVersion}
        </small>
      </div>
      <button className="secondary small" disabled={busy} onClick={() => refresh.mutate()}>
        Refresh withdrawal review
      </button>
      <FormError error={refresh.error} />
      {v.outcome && (
        <div className="publication-history">
          <h4>Recorded outcome</h4>
          <p>Resident-facing reason: {v.outcome.reason}</p>
          <p>Private review reason: {v.outcome.internalReason}</p>
          <small>{dateLabel(v.outcome.decidedAt)}</small>
        </div>
      )}
      {v.state === 'CANCELLED' && (
        <p>The resident cancelled this request. No publisher decision was recorded.</p>
      )}
      {v.state === 'REQUESTED' && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            command.mutate();
          }}
        >
          <p>
            Approval withdraws public progress and prevents republication for this report. Decline
            keeps current public progress eligible for later review. Private reports, case age and
            agency work continue.
          </p>
          {(stale || conflict) && (
            <p role="alert" className="form-error">
              The case, public progress or request changed. Refresh this review and confirm it
              again. Your reason drafts are kept.
            </p>
          )}
          <label>
            Sharing decision
            <select
              value={result}
              onChange={(e) => {
                setResult(e.target.value as typeof result);
                setReviewed(false);
              }}
            >
              <option value="APPROVED">Approve withdrawal</option>
              <option value="DECLINED">Decline request</option>
            </select>
          </label>
          <label>
            Private withdrawal review reason
            <textarea
              required
              minLength={5}
              maxLength={1000}
              rows={3}
              value={internal}
              onChange={(e) => {
                setInternal(e.target.value);
                setReviewed(false);
              }}
            />
          </label>
          <label>
            Reason shared with the resident
            <textarea
              required
              minLength={5}
              maxLength={1000}
              rows={3}
              value={shared}
              onChange={(e) => {
                setShared(e.target.value);
                setReviewed(false);
              }}
            />
          </label>
          <small>Only this shared reason appears in the resident's private request history.</small>
          <label className="checkbox">
            <input
              required
              type="checkbox"
              checked={reviewed}
              onChange={(e) => setReviewed(e.target.checked)}
            />
            I reviewed the current public preview and this sharing decision.
          </label>
          <FormError error={command.error} />
          <button className="primary" disabled={busy || stale || conflict || !reviewed}>
            Record sharing decision
          </button>
        </form>
      )}
    </div>
  );
}
