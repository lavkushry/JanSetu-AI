'use client';

import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, dateLabel, type Me, type Schema } from '@/lib/api';
import { Badge, ErrorState, FormError, Loading, useSession } from './ui';
import { PartialAcceptance, TaskSplitReview } from './task-splits';

export function TaskProposal({ caseDetail: c }: { caseDetail: Schema['CaseDetail'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const agencies = useQuery({
    queryKey: ['staff-agencies'],
    queryFn: () => api<{ items: Schema['Agency'][] }>('authority/agencies'),
  });
  const [agency, setAgency] = useState('');
  const [scope, setScope] = useState('');
  const [prerequisiteTaskIds, setPrerequisiteTaskIds] = useState<string[]>([]);
  const [clientTaskId, setClientTaskId] = useState(() => crypto.randomUUID());
  const action = useMutation({
    mutationFn: () =>
      api<Schema['TaskProposalResult']>(`authority/cases/${c.id}/obligations`, {
        method: 'POST',
        version: c.version,
        body: { clientTaskId, agencyId: agency, scope, prerequisiteTaskIds },
      }),
    onSuccess: () => {
      setScope('');
      setPrerequisiteTaskIds([]);
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
          <fieldset className="task-prerequisites">
            <legend>Verify before starting this work</legend>
            <p>
              Choose any prerequisite tasks. Leave all unchecked for work that can proceed
              independently.
            </p>
            {c.obligations.map((task, index) =>
              task.requiredForRestoration && task.state !== 'CANCELLED' ? (
                <label key={task.id}>
                  <input
                    type="checkbox"
                    checked={prerequisiteTaskIds.includes(task.id)}
                    onChange={(e) =>
                      setPrerequisiteTaskIds((ids) =>
                        e.target.checked ? [...ids, task.id] : ids.filter((id) => id !== task.id),
                      )
                    }
                  />
                  <span>
                    Task {index + 1} · {task.agency} ·{' '}
                    {task.scope || 'Restoration task assessed at intake'}
                    <small>
                      {task.state === 'VERIFIED'
                        ? 'Independently verified'
                        : 'Verification pending'}
                    </small>
                  </span>
                </label>
              ) : null,
            )}
            <small>
              Prerequisites are fixed with this proposal. Acceptance is allowed while verification
              is pending.
            </small>
          </fieldset>
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
  const workBlocked = o.blockedByTaskIds.length > 0;
  const splits = c.taskSplitRequests.filter((s) => s.taskId === o.id);
  const pendingSplit = splits.some((s) => s.state === 'PENDING');
  const parentNumber = c.obligations.findIndex((task) => task.id === o.parentTaskId) + 1;
  return (
    <div className="obligation" id={`obligation-${o.id}`} data-testid={`obligation-${o.id}`}>
      <strong>{o.agency}</strong>
      <Badge state={o.state} />
      <p className="task-scope">{o.scope || 'Restoration task assessed at intake'}</p>
      <small>
        {o.scopeReplaced
          ? 'Original scope retained in split history'
          : o.requiredForRestoration
            ? 'Required for restoration'
            : 'Additional task'}{' '}
        · {o.dueAt ? `Due ${dateLabel(o.dueAt)}` : 'Deadline unavailable'}
      </small>
      {o.parentTaskId && <p className="review-note">Required scope from task {parentNumber}.</p>}
      <p>{o.workSummary || 'The proposed task has not yet been accepted.'}</p>
      {o.prerequisiteTaskIds.length > 0 && (
        <div className="task-sequence" data-testid="task-sequence">
          <p>
            {workBlocked
              ? 'Work blocked: prerequisite verification pending.'
              : 'Prerequisites independently verified. Work can proceed.'}
          </p>
          <ul>
            {c.obligations.map((task, index) =>
              o.prerequisiteTaskIds.includes(task.id) ? (
                <li key={task.id}>
                  Task {index + 1} · {task.agency} ·{' '}
                  {task.scope || 'Restoration task assessed at intake'}
                  <small>
                    {o.blockedByTaskIds.includes(task.id)
                      ? task.scopeReplaced
                        ? 'Split scopes awaiting independent verification'
                        : 'Awaiting independent verification'
                      : task.scopeReplaced
                        ? 'Split scopes independently verified'
                        : 'Independently verified'}
                  </small>
                </li>
              ) : null,
            )}
          </ul>
          {workBlocked && (
            <small>
              Agency acceptance is available. Start work after every prerequisite is verified.
            </small>
          )}
        </div>
      )}
      {open &&
        isAgent &&
        !pendingSplit &&
        ['PROPOSED', 'ACCEPTED', 'IN_PROGRESS'].includes(o.state) && (
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
              <button
                className="primary"
                disabled={action.isPending || (o.state !== 'PROPOSED' && workBlocked)}
              >
                {o.state === 'PROPOSED'
                  ? 'Accept task'
                  : o.state === 'ACCEPTED'
                    ? 'Start work'
                    : 'Claim completion'}
              </button>
            </div>
          </form>
        )}
      {o.canPartiallyAccept && <PartialAcceptance obligation={o} />}
      {splits.map((s) => (
        <TaskSplitReview key={s.id} request={s} caseDetail={c} />
      ))}
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
