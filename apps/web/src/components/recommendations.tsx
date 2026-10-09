'use client';
import Link from 'next/link';
import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, type Schema } from '@/lib/api';
import { ErrorState, FormError, Loading, useSession } from './ui';

export function RecommendationSettings() {
  const { me } = useSession();
  const q = useQuery({
    queryKey: ['recommendation-preferences', me?.profile.id],
    queryFn: () => api<Schema['RecommendationPreference']>('me/recommendation-preferences'),
    enabled: !!me,
  });
  return (
    <section
      className="account-panel"
      id="recommendation-settings"
      aria-labelledby="recommendation-settings-heading"
    >
      <h2 id="recommendation-settings-heading">Recommendation preferences</h2>
      <p className="muted">
        Choose your community interests, languages and coarse locality. Behavioral personalization
        is optional.
      </p>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => void q.refetch()} />
      ) : (
        <RecommendationForm key={`${me?.profile.id}:${q.data.version}`} value={q.data} />
      )}
    </section>
  );
}
function RecommendationForm({ value }: { value: Schema['RecommendationPreference'] }) {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const [enabled, setEnabled] = useState(value.personalizationEnabled);
  const [interests, setInterests] = useState(value.interests.join(', '));
  const [languages, setLanguages] = useState(value.languages.join(', '));
  const [locality, setLocality] = useState(value.locality);
  const split = (s: string) => [
    ...new Set(
      s
        .split(',')
        .map((v) => v.trim())
        .filter(Boolean),
    ),
  ];
  const refresh = async (saved: Schema['RecommendationPreference']) => {
    await qc.cancelQueries({ queryKey: ['feed'] });
    qc.setQueryData(['recommendation-preferences', me?.profile.id], saved);
    await qc.resetQueries({ queryKey: ['feed'] });
    notify('Recommendation preferences updated');
  };
  const save = useMutation({
    mutationFn: () =>
      api<Schema['RecommendationPreference']>('me/recommendation-preferences', {
        method: 'PUT',
        version: value.version,
        body: {
          personalizationEnabled: enabled,
          interests: split(interests),
          languages: split(languages),
          locality: locality.trim(),
        },
      }),
    onSuccess: refresh,
  });
  const reset = useMutation({
    mutationFn: () =>
      api<Schema['RecommendationPreference']>('me/recommendation-history/reset', {
        method: 'POST',
        version: value.version,
      }),
    onSuccess: refresh,
  });
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <label className="preference-switch">
        <span>
          <strong>Use my recommendation interactions</strong>
          <small>
            Use feedback and consented reading or skipping. Turning this off clears behavioral
            history.
          </small>
        </span>
        <input
          type="checkbox"
          role="switch"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />
      </label>
      <label>
        Community interests (slugs, separated by commas)
        <input
          value={interests}
          onChange={(e) => setInterests(e.target.value)}
          placeholder="e.g. neighbourhood-discussions"
        />
      </label>
      <label>
        Content language tags (separated by commas)
        <input
          value={languages}
          onChange={(e) => setLanguages(e.target.value)}
          placeholder="e.g. en-IN, hi-IN; blank for all"
        />
      </label>
      <label>
        Coarse locality
        <input
          maxLength={80}
          value={locality}
          onChange={(e) => setLocality(e.target.value)}
          placeholder="Public area label or geographic community slug"
        />
      </label>
      <p className="muted">
        Changing these preferences starts a fresh feed. History reset keeps your chosen preferences.
      </p>
      <FormError error={save.error || reset.error} />
      <button className="primary" disabled={save.isPending || reset.isPending}>
        {save.isPending ? 'Saving…' : 'Save recommendation preferences'}
      </button>{' '}
      <button
        type="button"
        className="secondary"
        disabled={save.isPending || reset.isPending}
        onClick={() => reset.mutate()}
      >
        {reset.isPending ? 'Resetting…' : 'Reset recommendation history'}
      </button>
    </form>
  );
}
const reasons: Record<Schema['RecommendationExplanation']['explanation'], string> = {
  EXPLICIT_INTEREST: 'Matches an interest you chose or a community you asked to see more of.',
  CHOSEN_LOCALITY: 'From the coarse locality you selected.',
  FOLLOWING: 'From a person or community you follow or have joined.',
  RECENT_PUBLIC_POST: 'A recent published conversation available to you.',
  CIVIC_URGENCY: 'Public service updates are ordered by urgency, then age.',
};
type FeedbackKind = 'MORE' | 'LESS' | 'SATISFIED' | 'DISSATISFIED';
export function RecommendationControl({ value }: { value: Schema['RecommendationExplanation'] }) {
  return <RecommendationControlBody key={value.exposureId ?? value.explanation} value={value} />;
}
function RecommendationControlBody({ value }: { value: Schema['RecommendationExplanation'] }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [choice, setChoice] = useState('');
  const [satisfaction, setSatisfaction] = useState('');
  const eventIds = useRef<Partial<Record<FeedbackKind, string>>>({});
  const feedback = useMutation({
    mutationFn: ({ kind, eventId }: { kind: FeedbackKind; eventId: string }) =>
      api('me/recommendation-events', {
        method: 'POST',
        body: { eventId, exposureId: value.exposureId, kind },
      }),
    onSuccess: async (_, { kind }) => {
      if (kind === 'SATISFIED' || kind === 'DISSATISFIED') {
        setSatisfaction(kind);
        notify('Thanks — your usefulness feedback was recorded');
      } else {
        setChoice(kind);
        notify(
          kind === 'MORE'
            ? 'Preference recorded for future recommendations'
            : 'This post will be hidden from recommendations',
        );
      }
      if (kind === 'LESS') {
        await qc.cancelQueries({ queryKey: ['feed'] });
        await qc.resetQueries({ queryKey: ['feed'] });
      }
    },
  });
  const submit = (kind: FeedbackKind) => {
    const eventId = (eventIds.current[kind] ??= crypto.randomUUID());
    feedback.mutate({ kind, eventId });
  };
  return (
    <details className="recommendation-control">
      <summary>Why this?</summary>
      <p>{reasons[value.explanation]}</p>
      {value.exposureId ? (
        <>
          <button
            className="text-button"
            disabled={feedback.isPending || choice === 'MORE'}
            onClick={() => submit('MORE')}
          >
            More like this
          </button>{' '}
          <button
            className="text-button"
            disabled={feedback.isPending || choice === 'LESS'}
            onClick={() => submit('LESS')}
          >
            Less like this
          </button>
          <div role="group" aria-label="Was this recommendation useful?">
            <p>Was this recommendation useful?</p>
            <button
              type="button"
              className="text-button"
              aria-pressed={satisfaction === 'SATISFIED'}
              disabled={feedback.isPending || satisfaction !== ''}
              onClick={() => submit('SATISFIED')}
            >
              Helpful
            </button>{' '}
            <button
              type="button"
              className="text-button"
              aria-pressed={satisfaction === 'DISSATISFIED'}
              disabled={feedback.isPending || satisfaction !== ''}
              onClick={() => submit('DISSATISFIED')}
            >
              Not helpful
            </button>
            {satisfaction && <p role="status">Usefulness feedback recorded.</p>}
          </div>
          <FormError error={feedback.error} />
        </>
      ) : null}
      <p>
        <Link href="/account#recommendation-settings">Manage recommendation preferences</Link>
      </p>
    </details>
  );
}
