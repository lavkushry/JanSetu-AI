'use client';
import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ImagePlus, LockKeyhole, ScanText, Trash2, RefreshCw, Camera } from 'lucide-react';
import { api, readable, APIError, type Schema } from '@/lib/api';
import { FormError, useSession } from './ui';
import { VideoFrames } from './video-frames';

type Correction = Schema['OCRCorrection'];
async function fingerprint(file: File): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}
type PendingPhoto = { key: string; file: File; hash: string; error?: Error };
type UploadProgress = { done: number; total: number };
async function putPhoto(url: string, file: Blob): Promise<Schema['CompletedPart']> {
  // Only the local same-origin upload route is accepted; capabilities are never saved in drafts.
  if (!/^\/api\/media\/[a-f0-9-]+\/parts\/[1-5]\?token=[\w-]+$/.test(url))
    throw new Error('Invalid photo upload address');
  const r = await fetch(url, {
    method: 'PUT',
    headers: { 'content-type': 'application/octet-stream', 'x-jansetu-csrf': '1' },
    body: file,
    credentials: 'same-origin',
    cache: 'no-store',
  });
  const data = await r.json();
  if (!r.ok) throw new APIError(r.status, data.code, data.title);
  return data;
}
export function ReportPhotos({
  ids,
  setIds,
  submissionId,
  language,
  onState,
  onApply,
  analysisIds,
  onAnalysisId,
  roadMode = false,
  onBusy,
}: {
  onBusy: (busy: boolean) => void;
  roadMode?: boolean;
  analysisIds: Record<string, string>;
  onAnalysisId: (id: string, job: string) => void;
  ids: string[];
  setIds: (ids: string[]) => void;
  submissionId: string;
  language: string;
  onState: (id: string, state: string) => void;
  onApply: (id: string, text: string, corrections: Correction[]) => boolean;
}) {
  const input = useRef<HTMLInputElement>(null);
  const cameraInput = useRef<HTMLInputElement>(null);
  const files = useRef(new Map<string, File>());
  const [busy, setBusyLocal] = useState(false);
  function setBusy(value: boolean) {
    setBusyLocal(value);
    onBusy(value);
  }
  const [error, setError] = useState<Error | null>(null);
  const [failures, setFailures] = useState<Record<string, Error>>({});
  const [pending, setPending] = useState<PendingPhoto[]>([]);
  const [progress, setProgress] = useState<Record<string, UploadProgress>>({});
  const currentIds = useRef(ids);
  currentIds.current = ids;
  function clearFailure(id: string) {
    setFailures((f) => {
      const next = { ...f };
      delete next[id];
      return next;
    });
  }
  async function finish(id: string, file?: File) {
    const session = await api<Schema['UploadSession']>(`media/${id}/upload`);
    if (session.state === 'COMPLETE') {
      clearFailure(id);
      return;
    }
    if (session.state !== 'OPEN')
      throw new Error('This upload expired. Remove the photo and choose it again.');
    const total = Math.ceil(session.byteCount / session.partSize);
    const completed = new Map(session.completedParts.map((p) => [p.number, p]));
    const missing = Array.from({ length: total }, (_, i) => i + 1).filter((n) => !completed.has(n));
    setProgress((p) => ({ ...p, [id]: { done: completed.size, total } }));
    if (missing.length) {
      if (!file) throw new Error('Choose the same photo to resume this upload.');
      if (
        file.size !== session.byteCount ||
        file.type !== session.mimeType ||
        !session.sourceSha256 ||
        (await fingerprint(file)) !== session.sourceSha256
      )
        throw new Error('This is a different photo. Choose the original photo to resume.');
      files.current.set(id, file);
      const renewed = await api<Schema['UploadSession']>(`media/${id}/upload-parts`, {
        method: 'POST',
        version: session.version,
        body: { partNumbers: missing },
      });
      for (const part of renewed.parts) {
        const offset = (part.number - 1) * session.partSize;
        completed.set(
          part.number,
          await putPhoto(part.url, file.slice(offset, offset + session.partSize)),
        );
        setProgress((p) => ({ ...p, [id]: { done: completed.size, total } }));
      }
    }
    await api(`media/${id}/complete`, {
      method: 'POST',
      body: { parts: [...completed.values()].sort((a, b) => a.number - b.number) },
    });
    clearFailure(id);
  }
  async function allocate(photo: PendingPhoto) {
    try {
      const upload = await api<Schema['UploadSession']>('media/uploads', {
        method: 'POST',
        body: {
          clientSubmissionId: submissionId,
          clientUploadId: photo.key,
          sourceSha256: photo.hash,
          mimeType: photo.file.type,
          byteCount: photo.file.size,
          purpose: 'REPORT',
        },
      });
      const id = upload.mediaId;
      files.current.set(id, photo.file);
      setPending((p) => p.filter((v) => v.key !== photo.key));
      if (!currentIds.current.includes(id)) {
        currentIds.current = [...currentIds.current, id];
        setIds(currentIds.current);
      }
      try {
        await finish(id, photo.file);
      } catch (e) {
        setFailures((f) => ({ ...f, [id]: e as Error }));
      }
    } catch (e) {
      setPending((p) => p.map((v) => (v.key === photo.key ? { ...v, error: e as Error } : v)));
    }
  }
  async function resume(id: string, file?: File) {
    if (busy) return;
    setBusy(true);
    try {
      await finish(id, file || files.current.get(id));
    } catch (e) {
      setFailures((f) => ({ ...f, [id]: e as Error }));
    } finally {
      setBusy(false);
    }
  }
  async function choose(chosen: File[]) {
    if (busy) return;
    setError(null);
    if (currentIds.current.length + pending.length + chosen.length > 4) {
      setError(new Error('Attach up to four photos.'));
      return;
    }
    if (
      chosen.some(
        (f) =>
          !['image/jpeg', 'image/png', 'image/webp'].includes(f.type) ||
          f.size < 1 ||
          f.size > 10 * 1024 * 1024,
      )
    ) {
      setError(new Error('Choose JPEG, PNG, or WebP photos up to 10 MiB each.'));
      return;
    }
    setBusy(true);
    try {
      for (const file of chosen) {
        const photo = { key: crypto.randomUUID(), file, hash: await fingerprint(file) };
        setPending((p) => [...p, photo]);
        await allocate(photo);
      }
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="report-photos" aria-label="Private report photos">
      <div className="photo-section-heading">
        <div>
          <strong>
            <LockKeyhole size={16} /> Private photos
          </strong>
          <p className="muted">Up to 4 photos · JPEG, PNG, WebP · 10 MiB each</p>
        </div>
        <button
          type="button"
          className="secondary small"
          disabled={busy || ids.length + pending.length >= 4}
          onClick={() => input.current?.click()}
        >
          <ImagePlus size={17} />
          {busy ? 'Uploading…' : 'Add photos'}
        </button>
      </div>
      <div className="photo-capture-actions">
        <button
          type="button"
          className="secondary small"
          disabled={busy || ids.length + pending.length >= 4}
          onClick={() => cameraInput.current?.click()}
        >
          <Camera size={16} /> Take photo
        </button>
        <input
          ref={cameraInput}
          type="file"
          hidden
          accept="image/jpeg,image/png,image/webp"
          capture="environment"
          aria-label="Take report photo"
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = '';
            if (file) void choose([file]);
          }}
        />
        {roadMode && (
          <VideoFrames
            disabled={busy || ids.length + pending.length >= 4}
            onUse={(file) => choose([file])}
          />
        )}
      </div>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        multiple
        hidden
        aria-label="Choose report photos"
        onChange={(e) => {
          const chosen = Array.from(e.target.files || []);
          e.target.value = '';
          void choose(chosen);
        }}
      />
      <p className="photo-help">
        Photos stay in your private report. English text extraction is an experimental aid; check
        the words before using them.
      </p>
      <FormError error={error} />
      {pending.map((photo) => (
        <div key={photo.key} className="photo-help" role="status">
          <FormError error={photo.error || null} />
          {photo.error ? (
            <>
              <button
                type="button"
                className="secondary small"
                disabled={busy}
                onClick={async () => {
                  setBusy(true);
                  try {
                    await allocate(photo);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Retry adding photo
              </button>
              <button
                type="button"
                className="text-button"
                disabled={busy}
                onClick={() => {
                  setPending((p) => p.filter((v) => v.key !== photo.key));
                }}
              >
                Discard pending photo
              </button>
            </>
          ) : (
            'Starting private upload…'
          )}
        </div>
      ))}
      {ids.map((id, index) => (
        <PhotoCard
          key={id}
          id={id}
          index={index}
          language={language}
          initialJob={analysisIds[id]}
          onJob={(job) => onAnalysisId(id, job)}
          failure={failures[id]}
          progress={progress[id]}
          busy={busy}
          hasFile={files.current.has(id)}
          onResume={(file) => resume(id, file)}
          onState={onState}
          onApply={(text, corrections) => onApply(id, text, corrections)}
          onRetry={() => resume(id)}
          onRemoved={() => {
            files.current.delete(id);
            currentIds.current = currentIds.current.filter((v) => v !== id);
            setIds(currentIds.current);
          }}
        />
      ))}
    </section>
  );
}
function PhotoCard({
  id,
  index,
  language,
  failure,
  onRetry,
  onRemoved,
  onState,
  onApply,
  initialJob,
  onJob,
  progress,
  busy,
  hasFile,
  onResume,
}: {
  progress?: UploadProgress;
  busy: boolean;
  hasFile: boolean;
  onResume: (file: File) => Promise<void>;
  initialJob?: string;
  onJob: (job: string) => void;
  id: string;
  index: number;
  language: string;
  failure?: Error;
  onRetry?: () => Promise<void>;
  onRemoved: () => void;
  onState: (id: string, state: string) => void;
  onApply: (text: string, corrections: Correction[]) => boolean;
}) {
  const [jobId, setJobId] = useState<string | null>(initialJob || null);
  const [includeObjects, setIncludeObjects] = useState(false);
  const capabilities = useQuery({
    queryKey: ['capabilities'],
    queryFn: () => api<Schema['Capabilities']>('capabilities'),
  });
  const visionEnabled = capabilities.data?.analysisCapabilities.some(
    (c) => c.kind === 'ISSUE_DETECTION' && c.status === 'EVALUATING',
  );
  const [retrying, setRetrying] = useState(false);
  const resumeInput = useRef<HTMLInputElement>(null);
  const q = useQuery({
    queryKey: ['media', id],
    queryFn: () => api<Schema['Media']>(`media/${id}`),
    refetchInterval: (query) =>
      ['UPLOADING', 'QUARANTINED'].includes(query.state.data?.state || '') ? 1200 : false,
  });
  const upload = useQuery({
    queryKey: ['upload', id],
    queryFn: () => api<Schema['UploadSession']>(`media/${id}/upload`),
    enabled: q.data?.state === 'UPLOADING',
    refetchInterval: q.data?.state === 'UPLOADING' ? 1200 : false,
  });
  const uploadProgress =
    progress ||
    (upload.data && {
      done: upload.data.completedParts.length,
      total: Math.ceil(upload.data.byteCount / upload.data.partSize),
    });
  useEffect(() => {
    onState(id, q.data?.state || 'UPLOADING');
  }, [id, q.data?.state, onState]);
  const analyze = useMutation({
    mutationFn: () =>
      api<Schema['Analysis']>(`media/${id}/analyses`, {
        method: 'POST',
        body: {
          tasks: [
            'QUALITY',
            'OCR',
            ...(includeObjects && visionEnabled ? ['ISSUE_DETECTION'] : []),
          ],
          languageTag: language,
        },
      }),
    onSuccess: (v) => {
      setJobId(v.id);
      onJob(v.id);
    },
  });
  const remove = useMutation({
    mutationFn: async () => {
      try {
        const upload = await api<Schema['UploadSession']>(`media/${id}/upload`);
        await api(`media/${id}/upload`, { method: 'DELETE', version: upload.version });
      } catch (e) {
        if (!(e instanceof APIError && e.status === 404)) throw e;
      }
    },
    onSuccess: onRemoved,
  });
  const d = q.data?.derivatives[0];
  return (
    <article className="photo-card" aria-label={`Photo ${index + 1}`}>
      <div className="photo-preview">
        {d ? (
          <img
            src={d.url}
            width={d.width}
            height={d.height}
            alt={`Private report photo ${index + 1}`}
          />
        ) : (
          <div role="status">
            <ImagePlus size={25} />
            <span>
              {q.error
                ? 'Photo unavailable'
                : q.data?.state === 'REJECTED'
                  ? 'Photo could not be processed'
                  : 'Preparing private photo…'}
            </span>
          </div>
        )}
      </div>
      <div className="photo-card-body">
        <div className="photo-card-heading">
          <strong>Photo {index + 1}</strong>
          <button
            type="button"
            className="text-button"
            disabled={remove.isPending || busy || retrying}
            onClick={() => remove.mutate()}
          >
            <Trash2 size={15} />
            Remove photo
          </button>
        </div>
        {q.data?.state === 'UPLOADING' && (
          <div className="upload-progress">
            {uploadProgress && (
              <>
                <progress
                  aria-label={`Photo ${index + 1} upload progress`}
                  value={uploadProgress.done}
                  max={uploadProgress.total}
                />
                <p className="muted">
                  {uploadProgress.done} of {uploadProgress.total} parts uploaded
                </p>
              </>
            )}
            {!hasFile && (
              <>
                <button
                  type="button"
                  className="secondary small"
                  disabled={busy || retrying}
                  onClick={() => resumeInput.current?.click()}
                >
                  Choose same photo to resume
                </button>
                <input
                  ref={resumeInput}
                  type="file"
                  hidden
                  accept="image/jpeg,image/png,image/webp"
                  aria-label={`Resume photo ${index + 1}`}
                  onChange={async (e) => {
                    const file = e.target.files?.[0];
                    e.target.value = '';
                    if (!file) return;
                    setRetrying(true);
                    try {
                      await onResume(file);
                      await q.refetch();
                    } finally {
                      setRetrying(false);
                    }
                  }}
                />
              </>
            )}
            {!failure &&
              !busy &&
              uploadProgress?.done === uploadProgress?.total &&
              uploadProgress && (
                <button
                  type="button"
                  className="secondary small"
                  disabled={retrying}
                  onClick={async () => {
                    setRetrying(true);
                    try {
                      await onRetry?.();
                      await q.refetch();
                    } finally {
                      setRetrying(false);
                    }
                  }}
                >
                  Finish upload
                </button>
              )}
            <FormError error={upload.error} />
          </div>
        )}
        {q.data?.state === 'REJECTED' && (
          <p role="alert">
            {q.data.rejectionCode === 'PIXEL_LIMIT'
              ? 'Choose a smaller image (up to 12 million pixels and 8192 px per side).'
              : 'This file could not be decoded as a supported photo. Choose another image or continue with text.'}
          </p>
        )}
        {failure && (
          <>
            <FormError error={failure} />
            {onRetry && (
              <button
                type="button"
                className="secondary small"
                disabled={retrying || busy}
                onClick={async () => {
                  setRetrying(true);
                  await onRetry();
                  await q.refetch();
                  setRetrying(false);
                }}
              >
                <RefreshCw size={14} />
                Retry upload
              </button>
            )}
            <p className="muted">You can remove this photo and continue with text.</p>
          </>
        )}
        <FormError error={q.error || remove.error || analyze.error} />
        {d && !jobId && (
          <>
            {visionEnabled && (
              <label className="object-opt-in">
                <input
                  type="checkbox"
                  checked={includeObjects}
                  onChange={(e) => setIncludeObjects(e.target.checked)}
                  disabled={analyze.isPending}
                />
                Include experimental object recognition
              </label>
            )}
            <button
              type="button"
              className="secondary small"
              disabled={analyze.isPending}
              onClick={() => analyze.mutate()}
            >
              <ScanText size={16} />
              {analyze.isPending
                ? 'Starting…'
                : includeObjects && visionEnabled
                  ? 'Analyze text and objects'
                  : 'Read text from photo'}
            </button>
          </>
        )}
        {jobId && <AnalysisReview id={jobId} derivative={d} onApply={onApply} />}
      </div>
    </article>
  );
}
function ObjectReview({
  task,
  derivative,
  pending,
  retrying,
  onRetry,
}: {
  task: Schema['AnalysisTask'];
  derivative?: Schema['MediaDerivative'];
  pending: boolean;
  retrying: boolean;
  onRetry: () => void;
}) {
  const [showRegions, setShowRegions] = useState(true);
  const result = task.state === 'SUCCEEDED' ? task.result : null;
  const regions = (result?.regions || []).filter(
    (r): r is Schema['DetectionRegion'] => 'label' in r,
  );
  return (
    <section className="object-review" aria-label="Object recognition review">
      <strong>Object candidates · Experimental</strong>
      <p className="photo-help">
        Review what is visible yourself. This model cannot identify potholes, leaks, waste, or
        damage. Your description and category stay under your control.
      </p>
      {['QUEUED', 'RUNNING'].includes(task.state) && (
        <p role="status">Finding object candidates…</p>
      )}
      {task.state === 'SUCCEEDED' && regions.length === 0 && (
        <p className="muted">
          No supported object candidates found. This does not mean the scene is safe.
        </p>
      )}
      {regions.length > 0 && (
        <>
          {derivative && result && (
            <>
              <label className="object-opt-in">
                <input
                  type="checkbox"
                  checked={showRegions}
                  onChange={(e) => setShowRegions(e.target.checked)}
                />
                Show object regions
              </label>
              <svg
                className="object-overlay"
                viewBox={`0 0 ${result.originalSize.width} ${result.originalSize.height}`}
                role="img"
                aria-label="Object candidate regions"
              >
                <image
                  href={derivative.url}
                  width={result.originalSize.width}
                  height={result.originalSize.height}
                />
                {showRegions &&
                  regions.map((region, i) => (
                    <polygon
                      key={region.id}
                      points={region.polygon.map((point) => point.join(',')).join(' ')}
                      vectorEffect="non-scaling-stroke"
                    >
                      <title>
                        Region {i + 1}: {region.label}
                      </title>
                    </polygon>
                  ))}
              </svg>
            </>
          )}
          <ol className="object-candidates">
            {regions.map((region, i) => (
              <li key={region.id}>
                Region {i + 1}: {region.label} <span className="muted">· possible object</span>
              </li>
            ))}
          </ol>
        </>
      )}
      {task.state === 'FAILED' && (
        <>
          <p role="alert">
            Object recognition failed. Your photo and extracted text are still usable.
          </p>
          {task.retryable && (
            <button
              type="button"
              className="secondary small"
              disabled={pending || retrying}
              onClick={onRetry}
            >
              Retry object recognition
            </button>
          )}
        </>
      )}
      {task.state === 'UNSUPPORTED' && (
        <p className="muted">Object recognition is unavailable. Use your own observations.</p>
      )}
      {task.state === 'CANCELLED' && (
        <p className="muted">Object recognition cancelled. Use your own observations.</p>
      )}
    </section>
  );
}
function AnalysisReview({
  id,
  derivative,
  onApply,
}: {
  id: string;
  derivative?: Schema['MediaDerivative'];
  onApply: (text: string, corrections: Correction[]) => boolean;
}) {
  const { notify } = useSession();
  const q = useQuery({
    queryKey: ['analysis', id],
    queryFn: () => api<Schema['Analysis']>(`analyses/${id}`),
    refetchInterval: (query) =>
      ['QUEUED', 'RUNNING'].includes(query.state.data?.state || '') ? 1200 : false,
  });
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [excluded, setExcluded] = useState<Record<string, boolean>>({});
  const [applied, setApplied] = useState(false);
  const action = useMutation({
    mutationFn: ({ cancel, kinds }: { cancel: boolean; kinds?: string[] }) =>
      api(`analyses/${id}${cancel ? '' : '/retry'}`, {
        method: cancel ? 'DELETE' : 'POST',
        version: q.data!.version,
        body: cancel ? undefined : { tasks: kinds },
      }),
    onSuccess: () => q.refetch(),
  });
  const ocr = q.data?.tasks.find((t) => t.kind === 'OCR');
  const quality = q.data?.tasks.find((t) => t.kind === 'QUALITY');
  const detection = q.data?.tasks.find((t) => t.kind === 'ISSUE_DETECTION');
  const pending = q.data && ['QUEUED', 'RUNNING'].includes(q.data.state);
  const regions =
    ocr?.state === 'SUCCEEDED'
      ? (ocr.result?.regions || []).filter((r): r is Schema['OCRRegion'] => 'text' in r)
      : [];
  return (
    <div className="ocr-review" aria-label="OCR review">
      <p className="ocr-heading">
        <ScanText size={16} />
        <strong>
          {pending
            ? 'Reading photo…'
            : ocr?.state === 'SUCCEEDED'
              ? 'Review extracted text'
              : 'Text extraction unavailable'}
        </strong>
        <span className="muted">English preview</span>
      </p>
      {quality?.result?.codes?.includes('LOW_RESOLUTION') && (
        <p className="photo-help">This image has low resolution. Text may be incomplete.</p>
      )}
      {pending && (
        <>
          <p className="muted">
            You can write your own description and submit while text extraction runs.
          </p>
          <button
            type="button"
            className="text-button"
            disabled={action.isPending}
            onClick={() => action.mutate({ cancel: true })}
          >
            Cancel photo analysis
          </button>
        </>
      )}
      {ocr?.state === 'UNSUPPORTED' && (
        <p className="photo-help">
          OCR for this language is not enabled. Your photo and typed description are still usable.
        </p>
      )}
      {ocr?.state === 'FAILED' && (
        <>
          <p role="alert">Text could not be extracted. Continue with your own description.</p>
          {ocr.retryable && (
            <button
              type="button"
              className="secondary small"
              disabled={action.isPending}
              onClick={() => action.mutate({ cancel: false, kinds: ['OCR'] })}
            >
              Retry text extraction
            </button>
          )}
        </>
      )}
      {ocr?.state === 'CANCELLED' && (
        <p className="muted">Text extraction cancelled. Use your own description.</p>
      )}
      {ocr?.state === 'SUCCEEDED' && regions.length === 0 && (
        <p className="muted">No readable text was found. Write your own description.</p>
      )}
      {detection && (
        <ObjectReview
          task={detection}
          derivative={derivative}
          pending={Boolean(pending)}
          retrying={action.isPending}
          onRetry={() => action.mutate({ cancel: false, kinds: ['ISSUE_DETECTION'] })}
        />
      )}
      {regions.length > 0 && (
        <>
          <p className="photo-help">
            Select the words to use and correct any mistakes. This text does not confirm the issue,
            location, or responsible agency.
          </p>
          <div className="ocr-regions">
            {regions.map((region, i) => (
              <div className="ocr-region" key={region.id}>
                <input
                  type="checkbox"
                  checked={!excluded[region.id]}
                  aria-label={`Use word ${i + 1}: ${region.text}`}
                  onChange={(e) => {
                    setExcluded((v) => ({ ...v, [region.id]: !e.target.checked }));
                    setApplied(false);
                  }}
                />
                <label htmlFor={`${id}-${region.id}`}>
                  Word {i + 1}
                  <span className="sr-only">, original: {region.text}</span>
                </label>
                <input
                  id={`${id}-${region.id}`}
                  value={edits[region.id] ?? region.text}
                  maxLength={500}
                  onChange={(e) => {
                    setEdits((v) => ({ ...v, [region.id]: e.target.value }));
                    setApplied(false);
                  }}
                />
              </div>
            ))}
          </div>
          <button
            type="button"
            className="secondary small"
            disabled={
              applied ||
              Boolean(pending) ||
              action.isPending ||
              !regions.some((r) => !excluded[r.id] && (edits[r.id] ?? r.text).trim())
            }
            onClick={() => {
              const chosen = regions.filter((r) => !excluded[r.id]);
              const appliedAt = new Date().toISOString();
              const added = onApply(
                chosen
                  .map((r) => edits[r.id] ?? r.text)
                  .filter(Boolean)
                  .join(' '),
                chosen.map((r) => ({
                  taskId: ocr!.id,
                  regionId: r.id,
                  originalText: r.text,
                  correctedText: edits[r.id] ?? r.text,
                  appliedAt,
                })),
              );
              if (!added) return;
              setApplied(true);
              notify(
                'Reviewed text added to your description. Confirm the location and category yourself.',
              );
            }}
          >
            {applied ? 'Reviewed text added' : 'Add reviewed text to description'}
          </button>
        </>
      )}
      <FormError error={q.error || action.error} />
      {q.error && (
        <button type="button" className="text-button" onClick={() => q.refetch()}>
          Refresh text extraction
        </button>
      )}
    </div>
  );
}
export function PrivatePhotos({ ids }: { ids: string[] }) {
  return ids.length ? (
    <div className="private-photo-gallery">
      {ids.map((id, i) => (
        <PrivatePhoto key={id} id={id} index={i} />
      ))}
    </div>
  ) : null;
}
function PrivatePhoto({ id, index }: { id: string; index: number }) {
  const q = useQuery({
    queryKey: ['media', id],
    queryFn: () => api<Schema['Media']>(`media/${id}`),
  });
  const photo = q.data?.derivatives[0];
  return (
    <figure className="photo-preview">
      {photo ? (
        <img
          src={photo.url}
          width={photo.width}
          height={photo.height}
          alt={`Private report evidence photo ${index + 1}`}
        />
      ) : (
        <figcaption>
          {q.error ? 'Photo unavailable' : readable(q.data?.state || 'LOADING')}
        </figcaption>
      )}
    </figure>
  );
}
