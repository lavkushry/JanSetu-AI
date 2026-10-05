'use client';
import { PrivatePhotos } from './media';
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, ShieldCheck, ClipboardList } from 'lucide-react';
import { api, dateLabel, readable, type Schema, type Me } from '@/lib/api';
import { Badge, Empty, Loading, ErrorState, FormError, useSession } from './ui';
import { ContentReportQueue } from './content-reports';

export function Studio() {
  const { me, signIn } = useSession();
  const [tab, setTab] = useState<string | null>(null);
  if (!me)
    return (
      <Empty title="Staff workspace">
        <p>Choose a synthetic staff account to review content or manage a service task.</p>
        <button className="primary" onClick={signIn}>
          Choose demo account
        </button>
      </Empty>
    );
  const coordinator = me.roles.includes('COORDINATOR');
  const moderator = me.roles.includes('PLATFORM_MODERATOR');
  const agency = me.agencies.length > 0;
  if (!coordinator && !moderator && !agency)
    return (
      <Empty title="Staff access required">
        This account can participate in conversations and submit service reports.
      </Empty>
    );
  const active = tab || (moderator ? 'reviews' : 'cases');
  return (
    <>
      <div className="section-intro studio-intro">
        <span className="eyebrow">
          <ShieldCheck size={15} /> STAFF WORKSPACE
        </span>
        <h1>Keep your city moving</h1>
        <p>
          {me.profile.displayName} · {agency ? me.agencies[0].name : 'Coordinator & moderator'}
        </p>
      </div>
      <div className="feed-tabs staff-tabs">
        {moderator && (
          <button
            className={active === 'reviews' ? 'selected' : ''}
            onClick={() => setTab('reviews')}
          >
            Content review
          </button>
        )}
        {moderator && (
          <button
            className={active === 'content-reports' ? 'selected' : ''}
            onClick={() => setTab('content-reports')}
          >
            Reported content
          </button>
        )}
        {coordinator && (
          <button
            className={active === 'intake' ? 'selected' : ''}
            onClick={() => setTab('intake')}
          >
            Service intake
          </button>
        )}
        <button className={active === 'cases' ? 'selected' : ''} onClick={() => setTab('cases')}>
          Service cases
        </button>
      </div>
      {active === 'reviews' ? (
        <Reviews />
      ) : active === 'content-reports' ? (
        <ContentReportQueue />
      ) : active === 'intake' ? (
        <IntakeQueue />
      ) : (
        <CaseQueue me={me} />
      )}
    </>
  );
}
function Reviews() {
  const q = useQuery({
    queryKey: ['reviews'],
    queryFn: () => api<{ items: Schema['Review'][] }>('moderation'),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return q.data.items.length ? (
    <div className="staff-stack">
      {q.data.items.map((v) => (
        <ReviewCard item={v} key={v.id} />
      ))}
    </div>
  ) : (
    <Empty title="Review queue is clear">
      New posts and comments appear here before publication.
    </Empty>
  );
}
function ReviewCard({ item: v }: { item: Schema['Review'] }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState('');
  const action = useMutation({
    mutationFn: (decision: string) =>
      api(`moderation/${v.id}/decisions`, {
        method: 'POST',
        version: v.version,
        body: { action: decision, reason, targetRevision: v.targetRevision },
      }),
    onSuccess: () => qc.invalidateQueries(),
  });
  const obsolete = v.post && v.post.currentRevision !== v.targetRevision;
  return (
    <article className="staff-card" data-testid="review-card">
      <div className="receipt-eyebrow">
        <span>
          {v.post ? 'POST' : 'COMMENT'} · REVISION {v.targetRevision}
        </span>
        <Badge state={obsolete ? 'SUPERSEDED' : 'PENDING'} />
      </div>
      <h3>{v.post?.candidate?.title || 'Comment review'}</h3>
      <p className="post-body full">{v.post?.candidate?.body || v.body}</p>
      <small>
        {v.post?.author?.displayName} · {dateLabel(v.createdAt)}
      </small>
      {obsolete ? (
        <p className="muted">A newer revision exists. Review that revision instead.</p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            action.mutate('ALLOW');
          }}
        >
          <label>
            Review reason
            <input
              required
              minLength={5}
              maxLength={1000}
              placeholder="Why is this content allowed or restricted?"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </label>
          <FormError error={action.error} />
          <div className="form-actions">
            <button
              type="button"
              className="secondary"
              disabled={action.isPending || reason.trim().length < 5}
              onClick={() => action.mutate('RESTRICT')}
            >
              Restrict revision
            </button>
            <button className="primary" disabled={action.isPending}>
              <CheckCircle2 size={16} />
              {action.isPending ? 'Saving…' : 'Approve & publish'}
            </button>
          </div>
        </form>
      )}
    </article>
  );
}
function IntakeQueue() {
  const q = useQuery({
    queryKey: ['intake'],
    queryFn: () => api<{ items: Schema['Intake'][] }>('authority/intake'),
  });
  const agencies = useQuery({
    queryKey: ['agencies'],
    queryFn: () => api<{ items: Schema['Agency'][] }>('authority/agencies'),
  });
  if (q.isPending || agencies.isPending) return <Loading />;
  if (q.error || agencies.error)
    return (
      <ErrorState
        error={(q.error || agencies.error)!}
        retry={() => {
          q.refetch();
          agencies.refetch();
        }}
      />
    );
  return q.data.items.length ? (
    <div className="staff-stack">
      {q.data.items.map((v) => (
        <IntakeCard item={v} agencies={agencies.data.items} key={v.id} />
      ))}
    </div>
  ) : (
    <Empty title="Intake queue is clear">
      New private service reports appear here for assessment.
    </Empty>
  );
}
function IntakeCard({
  item: v,
  agencies,
}: {
  item: Schema['Intake'];
  agencies: Schema['Agency'][];
}) {
  const qc = useQueryClient();
  const [agency, setAgency] = useState(agencies[0]?.id || '');
  const [urgency, setUrgency] = useState(1);
  const [reason, setReason] = useState('');
  const action = useMutation({
    mutationFn: () =>
      api(`authority/reports/${v.id}/triage`, {
        method: 'POST',
        version: v.version,
        body: { agencyId: agency, category: v.metadata.category, urgencyTier: urgency, reason },
      }),
    onSuccess: () => qc.invalidateQueries(),
  });
  return (
    <article className="staff-card">
      <div className="receipt-eyebrow">
        <span>
          <ClipboardList size={16} /> PRIVATE SERVICE INTAKE
        </span>
        <Badge state="PENDING" />
      </div>
      <h3>{v.metadata.locationLabel}</h3>
      <p>{v.statement}</p>
      <PrivatePhotos ids={v.mediaIds} />
      <small>
        {readable(v.metadata.category)} · Received {dateLabel(v.receivedAt)} ·{' '}
        {v.publicationPreference === 'PRIVATE' ? 'Private only' : 'Reviewed public summary allowed'}
      </small>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          action.mutate();
        }}
      >
        <label>
          Proposed responsible agency
          <select required value={agency} onChange={(e) => setAgency(e.target.value)}>
            {agencies.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Assessed urgency
          <select value={urgency} onChange={(e) => setUrgency(Number(e.target.value))}>
            <option value={0}>Routine</option>
            <option value={1}>Standard</option>
            <option value={2}>High</option>
            <option value={3}>Critical</option>
          </select>
        </label>
        <label>
          Assessment reason
          <input
            required
            minLength={5}
            maxLength={1000}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Describe the service impact and routing reason"
          />
        </label>
        <FormError error={action.error} />
        <div className="form-actions">
          <button className="primary" disabled={action.isPending}>
            {action.isPending ? 'Creating…' : 'Create case & propose task'}
          </button>
        </div>
      </form>
    </article>
  );
}
function CaseQueue({ me }: { me: Me }) {
  const q = useQuery({
    queryKey: ['staff-cases'],
    queryFn: () => api<{ items: Schema['StaffCase'][] }>('authority/cases'),
  });
  const [selected, setSelected] = useState<string | null>(null);
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  const cid = selected || q.data.items[0]?.id;
  return q.data.items.length ? (
    <div className="case-workspace">
      <div className="case-queue" aria-label="Service cases">
        {q.data.items.map((c) => (
          <button
            className={c.id === cid ? 'selected' : ''}
            data-testid={`staff-case-${c.id}`}
            key={c.id}
            onClick={() => setSelected(c.id)}
          >
            <Badge state={c.state} />
            <strong>{c.title}</strong>
            <small>
              {readable(c.category)} · {dateLabel(c.firstReportedAt)}
            </small>
          </button>
        ))}
      </div>
      {cid && <CaseWorkspace id={cid} key={cid} me={me} />}
    </div>
  ) : (
    <Empty title="No assigned cases">
      Cases appear here when a coordinator assigns a task to your agency.
    </Empty>
  );
}
function CaseWorkspace({ id, me }: { id: string; me: Me }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const q = useQuery({
    queryKey: ['staff-case', id],
    queryFn: () => api<Schema['CaseDetail']>(`authority/cases/${id}`),
  });
  const [summary, setSummary] = useState('');
  const [result, setResult] = useState('VERIFIED');
  const [title, setTitle] = useState('');
  const [safe, setSafe] = useState('');
  const [area, setArea] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const action = useMutation({
    mutationFn: (command: { path: string; body: unknown; version: number }) =>
      api<Schema['Command']>(command.path, {
        method: 'POST',
        body: command.body,
        version: command.version,
      }),
    onSuccess: () => {
      qc.invalidateQueries();
      notify('Decision saved. Public progress requires a separate publication review.');
      setSummary('');
      setReviewed(false);
    },
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  const c = q.data;
  const isAgent = (agency: string | null) =>
    me.agencies.some(
      (a) => a.agency_id === agency && ['AGENCY_AGENT', 'AGENCY_LEAD'].includes(a.role),
    );
  const publisher = me.roles.includes('PUBLISHER');
  return (
    <section className="case-detail">
      <div className="staff-card">
        <div className="receipt-eyebrow">
          <span>{readable(c.category)} SERVICE CASE</span>
          <Badge state={c.state} />
        </div>
        <h2>{readable(c.category)} restoration</h2>
        <small>
          First report {dateLabel(c.firstReportedAt)} · Urgency {c.urgencyTier}
        </small>
        {c.obligations.map((o) => (
          <div className="obligation" key={o.id}>
            <strong>{o.agency}</strong>
            <Badge state={o.state} />
            <p>{o.workSummary || 'The proposed task has not yet been accepted.'}</p>
            <small>{o.dueAt ? `Due ${dateLabel(o.dueAt)}` : 'Deadline unavailable'}</small>
            {isAgent(o.agencyId) && ['PROPOSED', 'ACCEPTED', 'IN_PROGRESS'].includes(o.state) && (
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
            {o.canVerify && o.state === 'COMPLETION_CLAIMED' && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  action.mutate({
                    path: `authority/cases/${id}/verification-decisions`,
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
          </div>
        ))}
        <FormError error={action.error} />
      </div>
      {publisher && (
        <div className="staff-card publication">
          <h3>Review public progress</h3>
          <p className="muted">
            Write a safe summary for the public card. Keep names, exact private locations, and the
            original statement out of this preview.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              action.mutate({
                path: `authority/cases/${id}/publications`,
                version: c.version,
                body: { title, summary: safe, area, reviewed },
              });
            }}
          >
            <label>
              Public title
              <input
                required
                minLength={5}
                maxLength={180}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
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
                onChange={(e) => setSafe(e.target.value)}
              />
            </label>
            <label>
              Broad public area
              <input
                required
                minLength={3}
                maxLength={80}
                value={area}
                onChange={(e) => setArea(e.target.value)}
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
              <button className="primary" disabled={action.isPending}>
                Publish reviewed progress
              </button>
            </div>
          </form>
        </div>
      )}
      <div className="staff-card">
        <h3>Decision history</h3>
        <div className="timeline">
          {c.events.map((v) => (
            <div className="timeline-item" key={v.sequence}>
              <span className="timeline-dot" />
              <div>
                <strong>{readable(v.type)}</strong>
                <p>{String(v.detail.reason || v.detail.summary || 'Recorded decision')}</p>
                <small>
                  {readable(v.role)} · {dateLabel(v.at)}
                </small>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
