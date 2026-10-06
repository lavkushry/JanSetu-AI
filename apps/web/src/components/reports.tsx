'use client';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CheckCircle2, LockKeyhole, MapPin, FileText } from 'lucide-react';
import { api, dateLabel, readable, type Receipt, type Schema } from '@/lib/api';
import { Badge, Modal, FormError, Loading, Empty, ErrorState, useSession } from './ui';
import { ReceiptCard } from './social';
import { PublicSharingControl } from './public-sharing';
import { ReportPhotos, PrivatePhotos } from './media';
import { RoadFields, RoadSummary, DownloadRoadComplaint, emptyRoad } from './roads';

export function ReportWizard({ onClose }: { onClose: () => void }) {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const [step, setStep] = useState(1);
  const [mediaIds, setMediaIds] = useState<string[]>([]);
  const [analysisIds, setAnalysisIds] = useState<Record<string, string>>({});
  const [photoStates, setPhotoStates] = useState<Record<string, string>>({});
  const [photoCorrections, setPhotoCorrections] = useState<
    Record<string, Schema['OCRCorrection'][]>
  >({});
  const onPhotoState = useCallback(
    (id: string, state: string) =>
      setPhotoStates((v) => (v[id] === state ? v : { ...v, [id]: state })),
    [],
  );
  const photosReady = mediaIds.every((id) => photoStates[id] === 'APPROVED');
  const ocrCorrections = mediaIds.flatMap((id) => photoCorrections[id] || []);
  const [statement, setStatement] = useState('');
  const [category, setCategory] = useState<Schema['ReportInput']['category']>('FOOTPATH');
  const [roadDetails, setRoadDetails] = useState<Schema['RoadDetails']>(emptyRoad);
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
          roadDetails,
          locationLabel,
          languageTag,
          publicationPreference,
          submission: submission.current,
          mediaIds,
          photoCorrections,
          analysisIds,
        }),
      );
  }, [
    keep,
    statement,
    category,
    roadDetails,
    locationLabel,
    languageTag,
    publicationPreference,
    draftKey,
    mediaIds,
    photoCorrections,
    analysisIds,
  ]);
  const send = useMutation({
    mutationFn: () =>
      api<Schema['ReportAck']>('service-reports', {
        method: 'POST',
        key: submission.current,
        body: {
          clientSubmissionId: submission.current,
          statement,
          category,
          ...(category === 'ROAD' ? { roadDetails } : {}),
          locationLabel,
          languageTag,
          publicationPreference,
          mediaIds,
          ocrCorrections,
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
      setRoadDetails(d.roadDetails || emptyRoad);
      setLocation(d.locationLabel || '');
      setLanguage(d.languageTag || 'en-IN');
      setPreference(d.publicationPreference || 'PRIVATE');
      submission.current = d.submission || crypto.randomUUID();
      setMediaIds(
        Array.isArray(d.mediaIds)
          ? d.mediaIds.slice(0, 4).filter((id: unknown) => typeof id === 'string')
          : [],
      );
      setPhotoCorrections(d.photoCorrections || {});
      setAnalysisIds(d.analysisIds || {});
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
          if (!photosReady) return;
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
                <option value="ROAD">Potholes & road surface</option>
                <option value="FOOTPATH">Footpaths & crossings</option>
                <option value="LIGHT">Street lighting</option>
                <option value="WASTE">Waste & cleanliness</option>
                <option value="WATER">Water & drainage</option>
                <option value="OTHER">Other public service</option>
              </select>
            </label>
            {category === 'ROAD' && <RoadFields value={roadDetails} onChange={setRoadDetails} />}
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
            <ReportPhotos
              roadMode={category === 'ROAD'}
              ids={mediaIds}
              setIds={setMediaIds}
              analysisIds={analysisIds}
              onAnalysisId={(id, job) => setAnalysisIds((v) => ({ ...v, [id]: job }))}
              submissionId={submission.current}
              language={languageTag}
              onState={onPhotoState}
              onApply={(id, text, corrections) => {
                if ((statement + '\n' + text).length > 8000) {
                  notify('Your description is full. Shorten it before adding reviewed text.');
                  return false;
                }
                const nextCorrections = mediaIds.flatMap((mid) =>
                  mid === id ? corrections : photoCorrections[mid] || [],
                );
                const size = new TextEncoder().encode(
                  JSON.stringify({
                    clientSubmissionId: submission.current,
                    statement: statement + '\n' + text,
                    category,
                    ...(category === 'ROAD' ? { roadDetails } : {}),
                    locationLabel,
                    languageTag,
                    publicationPreference,
                    mediaIds,
                    ocrCorrections: nextCorrections,
                  }),
                ).length;
                if (nextCorrections.length > 500 || size > 65536) {
                  notify(
                    'Select fewer words before adding reviewed text. You can also type your description.',
                  );
                  return false;
                }
                setStatement((v) => (v ? v + '\n' : '') + text);
                setPhotoCorrections((v) => ({ ...v, [id]: corrections }));
                return true;
              }}
            />
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
            {category === 'ROAD' && <RoadSummary details={roadDetails} />}
            <PrivatePhotos ids={mediaIds} />
            {mediaIds.length > 0 && (
              <p className="muted">
                {mediaIds.length} private photo(s). Photos and raw OCR will not appear on public
                progress cards.
              </p>
            )}
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
          {!photosReady && (
            <p className="photo-help" role="status">
              Preparing photos. You can remove a photo to continue with text.
            </p>
          )}
          <button className="primary" disabled={send.isPending || !photosReady}>
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
            {v.roadDetails && (
              <>
                <p className="receipt-location">
                  <MapPin size={16} /> {v.locationLabel}
                </p>
                <RoadSummary details={v.roadDetails} guidance={v.roadGuidance} />
                <DownloadRoadComplaint report={v} />
              </>
            )}
            <PrivatePhotos ids={v.mediaIds} />
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
            {(v.receiptId || v.hasPublicationRequest) && <PublicSharingControl reportId={v.id} />}
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
