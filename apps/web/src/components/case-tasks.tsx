'use client';

import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, dateLabel, type Me, type Schema } from '@/lib/api';
import { Badge, ErrorState, FormError, Loading, useSession } from './ui';

export function TaskProposal({ caseDetail: c }: { caseDetail: Schema['CaseDetail'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const agencies = useQuery({
    queryKey: ['staff-agencies'],
    queryFn: () => api<{ items: Schema['Agency'][] }>('authority/agencies'),
  });
  const [agency, setAgency] = useState('');
  const [scope, setScope] = useState('');
  const [clientTaskId, setClientTaskId] = useState(() => crypto.randomUUID());
  const action = useMutation({
    mutationFn: () =>
      api<Schema['TaskProposalResult']>(`authority/cases/${c.id}/obligations`, {
        method: 'POST',
        version: c.version,
        body: { clientTaskId, agencyId: agency, scope },
      }),
    onSuccess: () => {
      setScope('');
      setClientTaskId(crypto.randomUUID());
      qc.invalidateQueries();
      notify('Required task proposed. Agency acceptance is still pending.');
    },
  });
  if (agencies.isPending) return <Loading />;
  if (agencies.error) return <ErrorState error={agencies.error} retry={() => agencies.refetch()} />;
  return (
    <div className="staff-card task-proposal" data-testid="task-proposal">
      <h3>Propose another required task</h3>
      <p>
        Define the remaining work for this case. Each task needs agency acceptance and independent
        verification before the case can resolve.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          action.mutate();
        }}
      >
        <fieldset disabled={action.isPending}>
          <label>
            Agency for this task
            <select required value={agency} onChange={(e) => setAgency(e.target.value)}>
              <option value="">Choose an agency</option>
              {agencies.data.items.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Required work scope
            <textarea
              required
              minLength={10}
              maxLength={1000}
              rows={3}
              value={scope}
              onChange={(e) => setScope(e.target.value)}
              placeholder="Describe this task’s distinct restoration work"
            />
          </label>
          <small>
            This proposal stays within the staff workspace. It preserves the original report time;
            public progress needs a separate publication review. Deadline policy is unavailable.
          </small>
          <FormError error={action.error} />
          {action.error && (
            <button type="button" onClick={() => qc.invalidateQueries()}>
              Refresh case
            </button>
          )}
          <div className="form-actions">
            <button className="primary" disabled={!agencies.data.items.length}>
              {action.isPending ? 'Proposing…' : 'Propose required task'}
            </button>
          </div>
        </fieldset>
      </form>
    </div>
  );
}

export function ObligationCard({
  obligation: o,
  caseDetail: c,
  me,
}: {
  obligation: Schema['Obligation'];
  caseDetail: Schema['CaseDetail'];
  me: Me;
}) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [summary, setSummary] = useState('');
  const [result, setResult] = useState('VERIFIED');
  const action = useMutation({
    mutationFn: (command: { path: string; body: unknown; version: number }) =>
      api<Schema['Command']>(command.path, {
        method: 'POST',
        body: command.body,
        version: command.version,
      }),
    onSuccess: () => {
      qc.invalidateQueries();
      setSummary('');
      notify('Decision saved. Public progress requires a separate publication review.');
    },
  });
  const isAgent = me.agencies.some(
    (a) => a.agency_id === o.agencyId && ['AGENCY_AGENT', 'AGENCY_LEAD'].includes(a.role),
  );
  const open = c.state !== 'RESOLVED' && c.state !== 'WITHDRAWN';
  return (
    <div className="obligation" data-testid={`obligation-${o.id}`}>
      <strong>{o.agency}</strong>
      <Badge state={o.state} />
      <p className="task-scope">{o.scope || 'Restoration task assessed at intake'}</p>
      <small>
        {o.requiredForRestoration ? 'Required for restoration' : 'Additional task'} ·{' '}
        {o.dueAt ? `Due ${dateLabel(o.dueAt)}` : 'Deadline unavailable'}
      </small>
      <p>{o.workSummary || 'The proposed task has not yet been accepted.'}</p>
      {open && isAgent && ['PROPOSED', 'ACCEPTED', 'IN_PROGRESS'].includes(o.state) && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const route =
              o.state === 'PROPOSED'
                ? 'accept'
                : o.state === 'ACCEPTED'
                  ? 'start'
                  : 'completion-claims';
            action.mutate({
              path: `authority/obligations/${o.id}/${route}`,
              version: o.version,
              body: { summary },
            });
          }}
        >
          <label>
            Work or decision summary
            <textarea
              required
              minLength={5}
              maxLength={2000}
              rows={3}
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
              placeholder="Record the agency’s decision or work performed"
            />
          </label>
          <div className="form-actions">
            <button className="primary" disabled={action.isPending}>
              {o.state === 'PROPOSED'
                ? 'Accept task'
                : o.state === 'ACCEPTED'
                  ? 'Start work'
                  : 'Claim completion'}
            </button>
          </div>
        </form>
      )}
      {o.state === 'COMPLETION_CLAIMED' && (
        <p className="review-note">Completion is a claim until independently verified.</p>
      )}
      {open && o.canVerify && o.state === 'COMPLETION_CLAIMED' && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            action.mutate({
              path: `authority/cases/${c.id}/verification-decisions`,
              version: c.version,
              body: { obligationId: o.id, result, reason: summary },
            });
          }}
        >
          <label>
            Inspection result
            <select value={result} onChange={(e) => setResult(e.target.value)}>
              <option value="VERIFIED">Restoration verified</option>
              <option value="NOT_RESTORED">Service not restored</option>
              <option value="INSUFFICIENT">Insufficient evidence</option>
            </select>
          </label>
          <label>
            Independent inspection reason
            <textarea
              required
              minLength={10}
              maxLength={2000}
              rows={3}
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
            />
          </label>
          <small>
            Local demonstration: this records a fictional inspection without evidence uploads.
          </small>
          <div className="form-actions">
            <button className="primary" disabled={action.isPending}>
              Record verification decision
            </button>
          </div>
        </form>
      )}
      <FormError error={action.error} />
      {action.error && (
        <button type="button" onClick={() => qc.invalidateQueries()}>
          Refresh case
        </button>
      )}
    </div>
  );
}
