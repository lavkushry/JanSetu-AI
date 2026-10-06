'use client';
import { PrivatePhotos } from './media';
import { RoadSummary } from './roads';
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, ShieldCheck, ClipboardList } from 'lucide-react';
import { api, dateLabel, readable, type Schema, type Me } from '@/lib/api';
import { Badge, Empty, Loading, ErrorState, FormError, useSession } from './ui';
import { ContentReportQueue } from './content-reports';
import { AppealQueue } from './appeals';
import { PublicationWithdrawalQueue } from './public-sharing';
import { PublicationReview } from './publication-review';
import { ObligationCard, TaskProposal } from './case-tasks';

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
  const publisher = me.roles.includes('PUBLISHER');
  const agency = me.agencies.length > 0;
  if (!coordinator && !moderator && !publisher && !agency)
    return (
      <Empty title="Staff access required">
        This account can participate in conversations and submit service reports.
      </Empty>
    );
  const active = tab || (agency ? 'cases' : moderator ? 'reviews' : 'cases');
  return (
    <>
      <div className="section-intro studio-intro">
        <span className="eyebrow">
          <ShieldCheck size={15} /> STAFF WORKSPACE
        </span>
        <h1>Keep your city moving</h1>
        <p>
          {me.profile.displayName} ·{' '}
          {agency
            ? me.agencies[0].name
            : publisher && !coordinator && !moderator
              ? 'Publication reviewer'
              : 'Coordinator & moderator'}
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
        {moderator && (
          <button
            className={active === 'appeals' ? 'selected' : ''}
            onClick={() => setTab('appeals')}
          >
            Appeals
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
        {publisher && (
          <button
            className={active === 'public-sharing' ? 'selected' : ''}
            onClick={() => setTab('public-sharing')}
          >
            Public sharing
          </button>
        )}
        <button className={active === 'cases' ? 'selected' : ''} onClick={() => setTab('cases')}>
          Service cases
        </button>
      </div>
      {active === 'public-sharing' ? (
        <PublicationWithdrawalQueue />
      ) : active === 'appeals' ? (
        <AppealQueue />
      ) : active === 'reviews' ? (
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
  const [authorReason, setAuthorReason] = useState('');
  const action = useMutation({
    mutationFn: (decision: string) =>
      api(`moderation/${v.id}/decisions`, {
        method: 'POST',
        version: v.version,
        body: { action: decision, reason, authorReason, targetRevision: v.targetRevision },
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
          <label>
            Reason shared with author
            <textarea
              maxLength={1000}
              value={authorReason}
              onChange={(e) => setAuthorReason(e.target.value)}
              placeholder="Explain what the author needs to correct"
            />
            <small>
              Required to restrict. Keep reporter identities and private complaint details out.
            </small>
          </label>
          <FormError error={action.error} />
          <div className="form-actions">
            <button
              type="button"
              className="secondary"
              disabled={
                action.isPending ||
                Array.from(reason.trim()).length < 5 ||
                Array.from(authorReason.trim()).length < 5
              }
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
      {v.metadata.roadDetails && (
        <RoadSummary details={v.metadata.roadDetails} guidance={v.metadata.roadGuidance} />
      )}
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
  const q = useQuery({
    queryKey: ['staff-case', id],
    queryFn: () => api<Schema['CaseDetail']>(`authority/cases/${id}`),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  const c = q.data;
  const required = c.obligations.filter((o) => o.requiredForRestoration);
  const verified = required.filter((o) => o.state === 'VERIFIED').length;
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
        <p className="review-note" data-testid="restoration-summary">
          {verified} of {required.length} required tasks verified.
          {verified < required.length &&
            ' The case stays open until every required task is independently verified.'}
        </p>
        {c.obligations.map((o) => (
          <ObligationCard key={o.id} obligation={o} caseDetail={c} me={me} />
        ))}
      </div>
      {c.canProposeTask && <TaskProposal caseDetail={c} />}
      {me.roles.includes('COORDINATOR') && !c.canProposeTask && (
        <p className="review-note">
          {['RESOLVED', 'WITHDRAWN'].includes(c.state)
            ? 'This case is closed to new task proposals.'
            : 'The case has reached the eight-task limit.'}
        </p>
      )}
      {c.canPublish && <PublicationReview caseDetail={c} />}
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
