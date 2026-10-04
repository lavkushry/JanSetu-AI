'use client';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CheckCircle2, LockKeyhole, ImagePlus, MapPin, FileText } from 'lucide-react';
import { api, dateLabel, readable, type Receipt, type Schema } from '@/lib/api';
import { Badge, Modal, FormError, Loading, Empty, ErrorState, useSession } from './ui';
import { ReceiptCard } from './social';

export function ReportWizard({ onClose }: { onClose: () => void }) {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const [step, setStep] = useState(1);
  const [statement, setStatement] = useState('');
  const [category, setCategory] = useState<Schema['ReportInput']['category']>('FOOTPATH');
  const [locationLabel, setLocation] = useState('');
  const [languageTag, setLanguage] = useState('en-IN');
  const [publicationPreference, setPreference] =
    useState<Schema['ReportInput']['publicationPreference']>('SANITIZED_RECEIPT');
  const [keep, setKeep] = useState(false);
  const [resume, setResume] = useState(false);
  const [ack, setAck] = useState<Schema['ReportAck'] | null>(null);
  const submission = useRef(crypto.randomUUID());
  const draftKey = `jansetu.report-draft.${me?.profile.id}`;
  useEffect(() => {
    setResume(!!localStorage.getItem(draftKey));
  }, [draftKey]);
  useEffect(() => {
    if (keep)
      localStorage.setItem(
        draftKey,
        JSON.stringify({
          statement,
          category,
          locationLabel,
          languageTag,
          publicationPreference,
          submission: submission.current,
        }),
      );
  }, [keep, statement, category, locationLabel, languageTag, publicationPreference, draftKey]);
  const send = useMutation({
    mutationFn: () =>
      api<Schema['ReportAck']>('service-reports', {
        method: 'POST',
        key: submission.current,
        body: {
          clientSubmissionId: submission.current,
          statement,
          category,
          locationLabel,
          languageTag,
          publicationPreference,
        } satisfies Schema['ReportInput'],
      }),
    onSuccess: (result) => {
      setAck(result);
      localStorage.removeItem(draftKey);
      qc.invalidateQueries();
    },
  });
  function restore() {
    try {
      const d = JSON.parse(localStorage.getItem(draftKey) || '{}');
      setStatement(d.statement || '');
      setCategory(d.category || 'OTHER');
      setLocation(d.locationLabel || '');
      setLanguage(d.languageTag || 'en-IN');
      setPreference(d.publicationPreference || 'PRIVATE');
      submission.current = d.submission || crypto.randomUUID();
      setKeep(true);
      setResume(false);
    } catch {
      localStorage.removeItem(draftKey);
    }
  }
  if (ack)
    return (
      <Modal title="Your report was received" onClose={onClose}>
        <div className="report-success">
          <CheckCircle2 size={48} />
          <h3>Received by JanSetu</h3>
          <p>{ack.message}</p>
          <div className="private-note">
            <LockKeyhole size={18} />
            You can track this report privately in My reports.
          </div>
          <small>Received {dateLabel(ack.receivedAt)}</small>
          <Link className="primary" href="/my-reports" onClick={onClose}>
            View my reports <ArrowRight size={16} />
          </Link>
        </div>
      </Modal>
    );
  return (
    <Modal title="Report a service issue" onClose={onClose} wide>
      <div className="steps">
        <span className={step === 1 ? 'current' : ''}>1 · Describe</span>
        <span className={step === 2 ? 'current' : ''}>2 · Review & submit</span>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (step === 1) setStep(2);
          else send.mutate();
        }}
      >
        {step === 1 ? (
          <>
            {resume && (
              <div className="draft-banner">
                A report draft is saved on this device.{' '}
                <button type="button" className="text-button" onClick={restore}>
                  Restore draft
                </button>
                <button
                  type="button"
                  className="text-button"
                  onClick={() => {
                    localStorage.removeItem(draftKey);
                    setResume(false);
                  }}
                >
                  Discard
                </button>
              </div>
            )}
            <p className="muted">
              Tell us what needs attention. This local demonstration uses synthetic agencies; please
              use fictional details.
            </p>
            <label>
              Service category
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value as Schema['ReportInput']['category'])}
              >
                <option value="FOOTPATH">Roads & footpaths</option>
                <option value="LIGHT">Street lighting</option>
                <option value="WASTE">Waste & cleanliness</option>
                <option value="WATER">Water & drainage</option>
                <option value="OTHER">Other public service</option>
              </select>
            </label>
            <label>
              Location or landmark
              <input
                required
                minLength={3}
                maxLength={180}
                autoFocus
                placeholder="e.g. Crossing near 12th Main, Indiranagar"
                value={locationLabel}
                onChange={(e) => setLocation(e.target.value)}
              />
            </label>
            <label>
              Describe the issue
              <textarea
                required
                minLength={10}
                maxLength={8000}
                rows={5}
                placeholder="What happened? What service is affected?"
                value={statement}
                onChange={(e) => setStatement(e.target.value)}
              />
            </label>
            <button
              type="button"
              className="upload-fallback"
              onClick={() =>
                notify(
                  'Image upload, OCR, and voice are not enabled yet. Please describe the issue in text.',
                )
              }
            >
              <ImagePlus size={22} />
              <span>
                Add a photo <small>Not enabled yet · text descriptions are available</small>
              </span>
            </button>
            <label>
              Text language
              <select value={languageTag} onChange={(e) => setLanguage(e.target.value)}>
                <option value="en-IN">English</option>
                <option value="hi-IN">Hindi</option>
                <option value="kn-IN">Kannada</option>
                <option value="ta-IN">Tamil</option>
                <option value="te-IN">Telugu</option>
                <option value="mr-IN">Marathi</option>
                <option value="bn-IN">Bengali</option>
                <option value="und">Another language</option>
              </select>
            </label>
            <fieldset className="preference">
              <legend>How would you like to track progress?</legend>
              <label className="radio">
                <input
                  type="radio"
                  name="preference"
                  checked={publicationPreference === 'SANITIZED_RECEIPT'}
                  onChange={() => setPreference('SANITIZED_RECEIPT')}
                />
                <span>
                  <strong>Allow a reviewed public progress card</strong>
                  <small>
                    Your original report and identity stay private. A reviewer writes the public
                    summary.
                  </small>
                </span>
              </label>
              <label className="radio">
                <input
                  type="radio"
                  name="preference"
                  checked={publicationPreference === 'PRIVATE'}
                  onChange={() => setPreference('PRIVATE')}
                />
                <span>
                  <strong>Private progress only</strong>
                  <small>No public progress card will be published for this report.</small>
                </span>
              </label>
            </fieldset>
            <label className="checkbox">
              <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
              Save this private draft on this device
            </label>
            {keep && (
              <small className="muted">
                Use a device you trust. You can discard the saved draft when you return.
              </small>
            )}
          </>
        ) : (
          <div className="report-preview">
            <div className="private-note">
              <LockKeyhole size={18} />
              Private submission preview
            </div>
            <h3>{readable(category)} issue</h3>
            <p className="receipt-location">
              <MapPin size={16} />
              {locationLabel}
            </p>
            <p className="post-body full">{statement}</p>
            <dl>
              <div>
                <dt>Text language</dt>
                <dd>{languageTag}</dd>
              </div>
              <div>
                <dt>Public progress</dt>
                <dd>
                  {publicationPreference === 'PRIVATE'
                    ? 'Private only'
                    : 'Only after a separate review'}
                </dd>
              </div>
            </dl>
            <p className="muted">
              Submitting confirms platform receipt. An agency must separately accept a proposed
              task.
            </p>
            <label className="checkbox">
              <input required type="checkbox" />I have reviewed this fictional report.
            </label>
          </div>
        )}
        <FormError error={send.error} />
        <div className="form-actions">
          <button
            type="button"
            className="secondary"
            disabled={send.isPending}
            onClick={() => (step === 2 ? setStep(1) : onClose())}
          >
            {step === 2 ? 'Back' : 'Cancel'}
          </button>
          <button className="primary" disabled={send.isPending}>
            {send.isPending ? 'Submitting…' : step === 1 ? 'Review report' : 'Submit report'}
            <ArrowRight size={16} />
          </button>
        </div>
      </form>
    </Modal>
  );
}
export function MyReports({ onReport }: { onReport: () => void }) {
  const { me, signIn } = useSession();
  const q = useQuery({
    queryKey: ['my-reports'],
    queryFn: () => api<{ items: Schema['ReportProgress'][] }>('my-reports'),
    enabled: !!me,
  });
  if (!me)
    return (
      <Empty title="Your reports are private">
        <p>Sign in to track your own service reports.</p>
        <button className="primary" onClick={signIn}>
          Choose a demo account
        </button>
      </Empty>
    );
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return (
    <>
      <div className="section-intro">
        <h1>My reports</h1>
        <p>Private progress, from platform receipt to verified restoration.</p>
        <button className="primary small" onClick={onReport}>
          New report
        </button>
      </div>
      {q.data.items.length === 0 ? (
        <Empty title="No reports yet">
          <p>Spot a service issue? Start with a clear description.</p>
          <button className="primary" onClick={onReport}>
            Report an issue
          </button>
        </Empty>
      ) : (
        q.data.items.map((v) => (
          <article className="my-report" key={v.id}>
            <div className="receipt-eyebrow">
              <span>
                <FileText size={16} /> YOUR PRIVATE REPORT
              </span>
              <Badge state={v.state} />
            </div>
            <p>{v.statement}</p>
            <small>Received {dateLabel(v.receivedAt)}</small>
            {v.responsibilities.map((o, i) => (
              <div className="report-responsibility" key={i}>
                <strong>{o.agency}</strong>
                <span>{readable(o.state)}</span>
              </div>
            ))}
            {v.state === 'PLATFORM_RECEIVED' && (
              <p className="muted">
                Agency acceptance has not been confirmed. A coordinator will assess the report.
              </p>
            )}
            {v.receiptId && (
              <Link className="text-button" href={`/cases/${v.receiptId}`}>
                View reviewed public progress <ArrowRight size={14} />
              </Link>
            )}
          </article>
        ))
      )}
    </>
  );
}
export function ReceiptDetail({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ['receipt', id],
    queryFn: () => api<Receipt>(`case-receipts/${id}`),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return <ReceiptCard receipt={q.data} detail />;
}
