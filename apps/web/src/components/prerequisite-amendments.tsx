'use client';

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, dateLabel, type Schema } from '@/lib/api';
import { FormError, useSession } from './ui';

export function PrerequisiteAmendments({
  obligation: o,
  caseDetail: c,
}: {
  obligation: Schema['Obligation'];
  caseDetail: Schema['CaseDetail'];
}) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [expanded, setExpanded] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [reason, setReason] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const [clientAmendmentId, setClientAmendmentId] = useState(() => crypto.randomUUID());
  const action = useMutation({
    mutationFn: () =>
      api<Schema['PrerequisiteAmendmentResult']>(
        `authority/obligations/${o.id}/prerequisite-amendments`,
        {
          method: 'POST',
          version: o.version,
          body: { clientAmendmentId, addedPrerequisiteTaskIds: selected, reason, reviewed },
        },
      ),
    onSuccess: () => {
      setSelected([]);
      setReason('');
      setReviewed(false);
      setClientAmendmentId(crypto.randomUUID());
      qc.invalidateQueries();
      notify(
        'Prerequisites added. Agency acceptance and independent verification remain required.',
      );
    },
  });
  const history = c.prerequisiteAmendments.filter((s) => s.taskId === o.id);
  const eligible = c.obligations.filter((task) => o.availablePrerequisiteTaskIds.includes(task.id));
  return (
    <div className="task-amendments" data-testid={`prerequisite-amendments-${o.id}`}>
      {history.map((s) => (
        <div className="task-amendment-history" key={s.id}>
          <h4>Prerequisites added by coordinator</h4>
          <p>{s.reason}</p>
          <small>{dateLabel(s.createdAt)}</small>
          <ul>
            {c.obligations.map((task, index) =>
              s.addedPrerequisiteTaskIds.includes(task.id) ? (
                <li key={task.id}>
                  <a href={`#obligation-${task.id}`}>
                    Task {index + 1} · {task.agency}
                  </a>
                </li>
              ) : null,
            )}
          </ul>
        </div>
      ))}
      {eligible.length > 0 && (
        <>
          <button type="button" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            Add prerequisite tasks
          </button>
          {expanded && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                action.mutate();
              }}
            >
              <fieldset disabled={action.isPending}>
                <fieldset className="task-prerequisites">
                  <legend>Additional tasks to verify first</legend>
                  <p>
                    Choose required work that must be independently verified before this task
                    begins.
                  </p>
                  {c.obligations.map((task, index) =>
                    o.availablePrerequisiteTaskIds.includes(task.id) ? (
                      <label key={task.id}>
                        <input
                          type="checkbox"
                          checked={selected.includes(task.id)}
                          onChange={(e) =>
                            setSelected((ids) =>
                              e.target.checked
                                ? [...ids, task.id]
                                : ids.filter((id) => id !== task.id),
                            )
                          }
                        />
                        <span>
                          Task {index + 1} · {task.agency} ·{' '}
                          {task.scope || 'Restoration task assessed at intake'}
                        </span>
                      </label>
                    ) : null,
                  )}
                </fieldset>
                <label>
                  Prerequisite addition reason
                  <textarea
                    required
                    minLength={10}
                    maxLength={2000}
                    rows={3}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                  />
                </label>
                <label className="split-check">
                  <input
                    type="checkbox"
                    required
                    checked={reviewed}
                    onChange={(e) => setReviewed(e.target.checked)}
                  />
                  <span>These requirements are necessary before work begins.</span>
                </label>
                <small>
                  Additions are recorded before agency acceptance. Original scope, prerequisites,
                  due date and report time remain. Existing requirements cannot be removed.
                </small>
                <FormError error={action.error} />
                {action.error && (
                  <button type="button" onClick={() => qc.invalidateQueries()}>
                    Refresh case
                  </button>
                )}
                <div className="form-actions">
                  <button className="primary" disabled={!selected.length}>
                    {action.isPending ? 'Saving…' : 'Record prerequisite additions'}
                  </button>
                </div>
              </fieldset>
            </form>
          )}
        </>
      )}
    </div>
  );
}
