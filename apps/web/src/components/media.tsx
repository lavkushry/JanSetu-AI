'use client';
import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ImagePlus, LockKeyhole, ScanText, Trash2, RefreshCw } from 'lucide-react';
import { api, readable, APIError, type Schema } from '@/lib/api';
import { FormError, useSession } from './ui';

type Correction = Schema['OCRCorrection'];
async function putPhoto(url: string, file: File): Promise<Schema['CompletedPart']> {
  // Only the local same-origin upload route is accepted; capabilities are never saved in drafts.
  if (!/^\/api\/media\/[a-f0-9-]+\/parts\/1\?token=[\w-]+$/.test(url))
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
}: {
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
  const files = useRef(new Map<string, File>());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [failures, setFailures] = useState<Record<string, Error>>({});
  const currentIds = useRef(ids);
  currentIds.current = ids;
  async function finish(id: string, file: File) {
    const session = await api<Schema['UploadSession']>(`media/${id}/upload`);
    if (session.state === 'COMPLETE') {
      setFailures((f) => {
        const next = { ...f };
        delete next[id];
        return next;
      });
      return;
    }
    if (session.state !== 'OPEN')
      throw new Error('This upload expired. Remove the photo and choose it again.');
    let part = session.completedParts[0];
    if (!part) {
      const renewed = await api<Schema['UploadSession']>(`media/${id}/upload-parts`, {
        method: 'POST',
        version: session.version,
        body: { partNumbers: [1] },
      });
      part = await putPhoto(renewed.parts[0].url, file);
    }
    await api(`media/${id}/complete`, { method: 'POST', body: { parts: [part] } });
    setFailures((f) => {
      const next = { ...f };
      delete next[id];
      return next;
    });
  }
  async function choose(chosen: File[]) {
    if (busy) return;
    setError(null);
    if (currentIds.current.length + chosen.length > 4) {
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
        const upload = await api<Schema['UploadSession']>('media/uploads', {
          method: 'POST',
          body: {
            clientSubmissionId: submissionId,
            mimeType: file.type,
            byteCount: file.size,
            purpose: 'REPORT',
          },
        });
        const id = upload.mediaId;
        files.current.set(id, file);
        currentIds.current = [...currentIds.current, id];
        setIds(currentIds.current);
        try {
          await finish(id, file);
        } catch (e) {
          setFailures((f) => ({ ...f, [id]: e as Error }));
        }
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
          disabled={busy || ids.length >= 4}
          onClick={() => input.current?.click()}
        >
          <ImagePlus size={17} />
          {busy ? 'Uploading…' : 'Add photos'}
        </button>
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
      {ids.map((id, index) => (
        <PhotoCard
          key={id}
          id={id}
          index={index}
          language={language}
          initialJob={analysisIds[id]}
          onJob={(job) => onAnalysisId(id, job)}
          failure={failures[id]}
          onState={onState}
          onApply={(text, corrections) => onApply(id, text, corrections)}
          onRetry={
            files.current.has(id)
              ? async () => {
                  try {
                    await finish(id, files.current.get(id)!);
                  } catch (e) {
                    setFailures((f) => ({ ...f, [id]: e as Error }));
                  }
                }
              : undefined
          }
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
}: {
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
  const [retrying, setRetrying] = useState(false);
  const q = useQuery({
    queryKey: ['media', id],
    queryFn: () => api<Schema['Media']>(`media/${id}`),
    refetchInterval: (query) =>
      ['UPLOADING', 'QUARANTINED'].includes(query.state.data?.state || '') ? 1200 : false,
  });
  useEffect(() => {
    onState(id, q.data?.state || 'UPLOADING');
  }, [id, q.data?.state, onState]);
  const analyze = useMutation({
    mutationFn: () =>
      api<Schema['Analysis']>(`media/${id}/analyses`, {
        method: 'POST',
        body: { tasks: ['QUALITY', 'OCR'], languageTag: language },
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
            disabled={remove.isPending}
            onClick={() => remove.mutate()}
          >
            <Trash2 size={15} />
            Remove photo
          </button>
        </div>
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
                disabled={retrying}
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
          <button
            type="button"
            className="secondary small"
            disabled={analyze.isPending}
            onClick={() => analyze.mutate()}
          >
            <ScanText size={16} />
            {analyze.isPending ? 'Starting…' : 'Read text from photo'}
          </button>
        )}
        {jobId && <AnalysisReview id={jobId} onApply={onApply} />}
      </div>
    </article>
  );
}
function AnalysisReview({
  id,
  onApply,
}: {
  id: string;
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
  const pending = q.data && ['QUEUED', 'RUNNING'].includes(q.data.state);
  const regions = ocr?.state === 'SUCCEEDED' ? ocr.result?.regions || [] : [];
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
            Cancel text extraction
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
