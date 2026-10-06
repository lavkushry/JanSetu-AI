'use client';
import { useQuery } from '@tanstack/react-query';
import { MapPin, ExternalLink, Download } from 'lucide-react';
import { api, readable, type Schema } from '@/lib/api';
import { FormError } from './ui';

export const roadTypes: Record<Schema['RoadType'], string> = {
  UNKNOWN: 'Other road / unsure',
  BENGALURU_CITY: 'Bengaluru city road',
  NHAI_HIGHWAY: 'NHAI-managed highway',
  KARNATAKA_PWD: 'Karnataka PWD road',
};
export const emptyRoad: Schema['RoadDetails'] = {
  roadName: '',
  issueKind: 'POTHOLE',
  roadType: 'UNKNOWN',
  travelDirection: '',
};

export function RoadGuidancePanel({ guidance }: { guidance: Schema['RoadGuidance'] }) {
  return (
    <aside className="road-guidance" aria-label="Road contact guidance">
      <strong>
        <MapPin size={16} /> Official contact guidance
      </strong>
      <p className="photo-help">{guidance.note}</p>
      {guidance.contacts.map((contact) => (
        <div className="road-contact" key={contact.sourceUrl}>
          <strong>{contact.name}</strong>
          <p>{contact.scope}</p>
          <p>
            Helpline: <b>{contact.helpline}</b>
          </p>
          <a
            className="text-button"
            href={contact.sourceUrl}
            target="_blank"
            rel="noopener noreferrer"
          >
            Official source <ExternalLink size={14} />
          </a>
          <small className="muted">Source checked {contact.sourceCheckedAt}</small>
        </div>
      ))}
      <p>
        <b>Contractor unconfirmed.</b> {guidance.contractorNote}
      </p>
      {guidance.contractSourceUrl && (
        <a
          className="text-button"
          href={guidance.contractSourceUrl}
          target="_blank"
          rel="noopener noreferrer"
        >
          Official procurement portal <ExternalLink size={14} />
        </a>
      )}
      <small className="muted">JanSetu does not send this report to these contacts.</small>
    </aside>
  );
}

export function RoadFields({
  value,
  onChange,
}: {
  value: Schema['RoadDetails'];
  onChange: (value: Schema['RoadDetails']) => void;
}) {
  const q = useQuery({
    queryKey: ['road-guidance', value.roadType],
    queryFn: () => api<Schema['RoadGuidance']>(`road-guidance?roadType=${value.roadType}`),
  });
  return (
    <fieldset className="road-fields">
      <legend>Road surface observation</legend>
      <p className="photo-help">
        Describe what you observed; a reviewer will assess it. If enabled, optional experimental
        pothole recognition is available for private photos. Review candidates yourself.
      </p>
      <label>
        Road name or number
        <input
          required
          minLength={3}
          maxLength={180}
          value={value.roadName}
          onChange={(e) => onChange({ ...value, roadName: e.target.value })}
          placeholder="e.g. Fictional Main Road or highway number"
        />
      </label>
      <label>
        Observed surface issue
        <select
          value={value.issueKind}
          onChange={(e) =>
            onChange({ ...value, issueKind: e.target.value as Schema['RoadDetails']['issueKind'] })
          }
        >
          <option value="POTHOLE">Pothole</option>
          <option value="BROKEN_SURFACE">Broken road surface</option>
        </select>
      </label>
      <label>
        Road type you believe applies
        <select
          value={value.roadType}
          onChange={(e) => onChange({ ...value, roadType: e.target.value as Schema['RoadType'] })}
        >
          {Object.entries(roadTypes).map(([key, label]) => (
            <option key={key} value={key}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <label>
        Direction or lane (optional)
        <input
          maxLength={100}
          value={value.travelDirection || ''}
          onChange={(e) => onChange({ ...value, travelDirection: e.target.value })}
          placeholder="e.g. Towards the fictional bus stop, left lane"
        />
      </label>
      {q.isPending && (
        <p role="status" className="muted">
          Loading contact guidance…
        </p>
      )}
      {q.error && (
        <>
          <FormError error={q.error} />
          <p className="photo-help">
            You can continue with manual reporting. Responsibility remains unconfirmed.
          </p>
          <button type="button" className="text-button" onClick={() => q.refetch()}>
            Retry contact guidance
          </button>
        </>
      )}
      {q.data && <RoadGuidancePanel guidance={q.data} />}
    </fieldset>
  );
}

export function RoadSummary({
  details,
  guidance,
}: {
  details: Schema['RoadDetails'];
  guidance?: Schema['RoadGuidance'] | null;
}) {
  return (
    <div className="road-summary" aria-label="Reviewed road details">
      <strong>{details.roadName}</strong>
      <p>
        {readable(details.issueKind)} · {roadTypes[details.roadType]}
      </p>
      {details.travelDirection && <p>{details.travelDirection}</p>}
      {details.observedAt && <p>Observed: {details.observedAt}</p>}
      <small className="muted">Resident observation · Road responsibility requires review</small>
      {guidance && (
        <details>
          <summary>Saved contact guidance</summary>
          <RoadGuidancePanel guidance={guidance} />
        </details>
      )}
    </div>
  );
}

export function DownloadRoadComplaint({ report }: { report: Schema['ReportProgress'] }) {
  function download() {
    const d = report.roadDetails;
    if (!d) return;
    const g = report.roadGuidance;
    const text = [
      'PRIVATE JANSETU ROAD REPORT — LOCAL DEMONSTRATION',
      `Platform report: ${report.id}`,
      `Platform received: ${report.receivedAt}`,
      'Receipt confirms JanSetu intake only. No external complaint has been sent.',
      '',
      `Road: ${d.roadName}`,
      `Landmark: ${report.locationLabel}`,
      `Observed issue: ${readable(d.issueKind)}`,
      `Resident-selected road type: ${roadTypes[d.roadType]}`,
      `Direction: ${d.travelDirection || 'Not supplied'}`,
      `Observed at: ${d.observedAt || 'Not supplied'}`,
      '',
      report.statement,
      '',
      `Private attachments: ${report.mediaIds.length} (photos are not included in this text file)`,
      '',
      'ROAD OWNER / INDIVIDUAL OFFICER / CONTRACTOR: UNCONFIRMED',
      ...(g
        ? [
            g.note,
            ...g.contacts.flatMap((c) => [
              c.name,
              c.scope,
              `Helpline: ${c.helpline}`,
              `Source: ${c.sourceUrl} (checked ${c.sourceCheckedAt})`,
            ]),
            g.contractorNote,
            `Guidance version: ${g.version}`,
          ]
        : []),
      '',
      'Review this draft and any recipient before sharing it outside JanSetu.',
    ].join('\n');
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `jansetu-road-report-${report.id}.txt`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return (
    <button type="button" className="secondary small" onClick={download}>
      <Download size={16} /> Download private complaint draft
    </button>
  );
}
