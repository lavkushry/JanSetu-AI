'use client';

import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, type Schema } from '@/lib/api';
import { Badge, FormError, useSession } from './ui';

export function PartialAcceptance({ obligation: o }: { obligation: Schema['Obligation'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [expanded, setExpanded] = useState(false);
  const [acceptedScope, setAcceptedScope] = useState('');
  const [remainingScope, setRemainingScope] = useState('');
  const [reason, setReason] = useState('');
  const [clientRequestId] = useState(() => crypto.randomUUID());
  const action = useMutation({
    mutationFn: () =>
      api<Schema['TaskSplitResult']>(`authority/obligations/${o.id}/partial-acceptances`, {
        method: 'POST',
        version: o.version,
        body: {
          clientRequestId,
          acceptedScope,
          remainingScope,
          reason,
          authorityBasisRef: 'synthetic-local-mandate-v1',
        },
      }),
    onSuccess: () => {
      qc.invalidateQueries();
      notify(
        'Partial acceptance saved. Coordinator review is pending; remaining work stays required.',
      );
    },
  });
  return (
    <div className="task-partial-acceptance" data-testid={`partial-acceptance-${o.id}`}>
      <button type="button" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
        Accept part of this task
      </button>
      {expanded && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            action.mutate();
          }}
        >
          <fieldset disabled={action.isPending}>
            <p>
              Define the work your agency can accept and all remaining work. An independent
              coordinator must review the complete split before either scope proceeds.
            </p>
            <label>
              Scope your agency can accept
              <textarea
                required
                minLength={10}
                maxLength={1000}
                rows={3}
                value={acceptedScope}
                onChange={(e) => setAcceptedScope(e.target.value)}
              />
            </label>
            <label>
              Remaining required scope
              <textarea
                required
                minLength={10}
                maxLength={1000}
                rows={3}
                value={remainingScope}
                onChange={(e) => setRemainingScope(e.target.value)}
              />
            </label>
            <label>
              Partial acceptance reason
              <textarea
                required
                minLength={10}
                maxLength={2000}
                rows={3}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
            <small>
              Local demonstration: fictional agency authority. The original report time,
              prerequisites and due date are preserved.
            </small>
            <FormError error={action.error} />
            {action.error && (
              <button type="button" onClick={() => qc.invalidateQueries()}>
                Refresh case
              </button>
            )}
            <div className="form-actions">
              <button className="primary">
                {action.isPending ? 'Saving…' : 'Request partial acceptance'}
              </button>
            </div>
          </fieldset>
        </form>
      )}
    </div>
  );
}

export function TaskSplitReview({
  request: s,
  caseDetail: c,
}: {
  request: Schema['TaskSplitRequest'];
  caseDetail: Schema['CaseDetail'];
}) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const agencies = useQuery({
    queryKey: ['staff-agencies'],
    queryFn: () => api<{ items: Schema['Agency'][] }>('authority/agencies'),
    enabled: s.canDecide,
  });
  const [result, setResult] = useState<'APPROVE' | 'REJECT'>('APPROVE');
  const [agency, setAgency] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const [reason, setReason] = useState('');
  const action = useMutation({
    mutationFn: () =>
      api<Schema['TaskSplitResult']>(`authority/cases/${c.id}/task-split-decisions`, {
        method: 'POST',
        version: c.version,
        body: {
          requestId: s.id,
          result,
          remainingAgencyId: result === 'APPROVE' ? agency : undefined,
          reviewed: result === 'APPROVE' && reviewed,
          reason,
        },
      }),
    onSuccess: () => {
      qc.invalidateQueries();
      notify(
        result === 'APPROVE'
          ? 'Scope split confirmed. Both parts remain required for restoration.'
          : 'Scope split rejected. The original task remains proposed.',
      );
    },
  });
  const capacity = c.obligations.length + 2 <= 8;
  return (
    <div className="task-split-review" data-testid={`task-split-${s.id}`}>
      <h4>Partial acceptance review</h4>
      <Badge state={s.state} />
      <dl>
        <dt>Accepted scope offered by the agency</dt>
        <dd>{s.acceptedScope}</dd>
        <dt>Remaining required scope</dt>
        <dd>{s.remainingScope}</dd>
        <dt>Agency reason</dt>
        <dd>{s.reason}</dd>
      </dl>
      {s.state === 'APPROVED' && (
        <p className="split-links">
          Original scope replaced by two required tasks.{' '}
          <a href={`#obligation-${s.acceptedTaskId}`}>View accepted part</a> ·{' '}
          <a href={`#obligation-${s.remainingTaskId}`}>View remaining part</a>
        </p>
      )}
      {s.decisionReason && <p>Coordinator decision: {s.decisionReason}</p>}
      {s.state === 'PENDING' && (
        <p>
          Awaiting an independent coordinator’s scope review. Whole-task acceptance is paused;
          remaining work stays required.
        </p>
      )}
      {s.canDecide && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            action.mutate();
          }}
        >
          <fieldset disabled={action.isPending}>
            <label>
              Scope split decision
              <select
                value={result}
                onChange={(e) => setResult(e.target.value === 'APPROVE' ? 'APPROVE' : 'REJECT')}
              >
                <option value="APPROVE">Confirm complete scope split</option>
                <option value="REJECT">Reject proposed split</option>
              </select>
            </label>
            {result === 'APPROVE' && (
              <>
                <label>
                  Agency for remaining required work
                  <select required value={agency} onChange={(e) => setAgency(e.target.value)}>
                    <option value="">Choose an agency</option>
                    {agencies.data?.items.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                      </option>
                    ))}
                  </select>
                </label>
                {agencies.isPending && <p>Loading agencies…</p>}
                <FormError error={agencies.error} />
                {agencies.error && (
                  <button type="button" onClick={() => agencies.refetch()}>
                    Retry agencies
                  </button>
                )}
                <label className="split-check">
                  <input
                    type="checkbox"
                    required
                    checked={reviewed}
                    onChange={(e) => setReviewed(e.target.checked)}
                  />
                  <span>Both scopes fully cover the original work without overlap.</span>
                </label>
                {!capacity && (
                  <p className="review-note">
                    Approval needs room for two tasks within the eight-task case limit. Rejection
                    remains available.
                  </p>
                )}
              </>
            )}
            <label>
              Scope split review reason
              <textarea
                required
                minLength={10}
                maxLength={2000}
                rows={3}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
            <small>
              Scope coverage requires human review. Both replacement tasks inherit prerequisites and
              due date; each needs independent verification. Public progress requires a separate
              publication review.
            </small>
            <FormError error={action.error} />
            {action.error && (
              <button type="button" onClick={() => qc.invalidateQueries()}>
                Refresh case
              </button>
            )}
            <div className="form-actions">
              <button
                className="primary"
                disabled={
                  result === 'APPROVE' && (!capacity || agencies.isPending || !!agencies.error)
                }
              >
                {action.isPending
                  ? 'Saving…'
                  : result === 'APPROVE'
                    ? 'Confirm scope split'
                    : 'Reject scope split'}
              </button>
            </div>
          </fieldset>
        </form>
      )}
    </div>
  );
}
