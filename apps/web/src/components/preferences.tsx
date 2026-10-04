'use client';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, VolumeX, Volume2, Users } from 'lucide-react';
import { api, type Schema } from '@/lib/api';
import { refreshSocialVisibility } from '@/lib/social-cache';
import { Avatar, Empty, ErrorState, FormError, Loading, Modal, useSession } from './ui';

export function useNotificationPreference(viewer?: string) {
  return useQuery({
    queryKey: ['notification-preferences', viewer || 'guest'],
    queryFn: () => api<Schema['NotificationPreference']>('me/notification-preferences'),
    enabled: !!viewer,
    refetchInterval: 30000,
  });
}

export function NotificationSettings() {
  const { me } = useSession();
  const q = useNotificationPreference(me?.profile.id);
  const [reloadKey, setReloadKey] = useState(0);
  return (
    <section
      className="account-panel"
      id="activity-settings"
      aria-labelledby="activity-settings-heading"
    >
      <h2 id="activity-settings-heading">
        <Bell size={19} /> Activity preferences
      </h2>
      <p className="muted">
        Choose whether approved replies and followed public service updates appear in your inbox.
      </p>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => void q.refetch()} />
      ) : (
        <NotificationForm
          key={`${me?.profile.id}:${reloadKey}`}
          value={q.data}
          reload={() =>
            void q.refetch().then((result) => {
              if (!result.error) setReloadKey((key) => key + 1);
            })
          }
        />
      )}
    </section>
  );
}
function NotificationForm({
  value,
  reload,
}: {
  value: Schema['NotificationPreference'];
  reload: () => void;
}) {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const [inApp, setInApp] = useState(value.inApp);
  const [baseline, setBaseline] = useState(value);
  const save = useMutation({
    mutationFn: () =>
      api<Schema['NotificationPreference']>('me/notification-preferences', {
        method: 'PATCH',
        version: baseline.version,
        body: { inApp },
      }),
    onSuccess: async (saved) => {
      setBaseline(saved);
      qc.setQueryData(['notification-preferences', me?.profile.id], saved);
      await qc.cancelQueries({ queryKey: ['activity'] });
      await qc.resetQueries({ queryKey: ['activity'] });
      await qc.resetQueries({ queryKey: ['activity-summary'] });
      notify(saved.inApp ? 'In-app notifications enabled' : 'In-app notifications paused');
    },
  });
  // A background refresh can update an untouched form, but must preserve the
  // version and intent of an unsaved edit so stale writes receive a conflict.
  useEffect(() => {
    if (
      value.version > baseline.version &&
      inApp === baseline.inApp &&
      !save.isPending &&
      !save.error
    ) {
      setBaseline(value);
      setInApp(value.inApp);
    }
  }, [value, baseline, inApp, save.isPending, save.error]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <label className="preference-switch">
        <span>
          <strong>In-app notifications</strong>
          <small>Private Activity inbox and unread badge</small>
        </span>
        <input
          type="checkbox"
          role="switch"
          checked={inApp}
          onChange={(e) => setInApp(e.target.checked)}
          disabled={save.isPending}
        />
      </label>
      <p className="muted preference-help">
        Pausing hides your existing inbox and stops new alerts. When enabled again, eligible
        delivered alerts return; updates sent while paused are skipped.
      </p>
      <p className="muted preference-help">
        Email, push notifications and quiet-hour scheduling are not available yet.
      </p>
      <FormError error={save.error} />
      <div className="form-actions">
        <button
          className="secondary small"
          type="button"
          disabled={save.isPending}
          onClick={reload}
        >
          Reload preferences
        </button>
        <button className="primary small" disabled={save.isPending || inApp === baseline.inApp}>
          {save.isPending ? 'Saving…' : 'Save activity preferences'}
        </button>
      </div>
    </form>
  );
}

export function MuteControl({
  type,
  id,
  label,
  active,
  menu = false,
}: {
  type: 'PROFILE' | 'COMMUNITY';
  id: string;
  label: string;
  active: boolean;
  menu?: boolean;
}) {
  const { me, signIn, notify } = useSession();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [duration, setDuration] = useState('FOREVER');
  const kind = type === 'PROFILE' ? 'person' : 'community';
  const save = useMutation({
    mutationFn: (enabled: boolean) =>
      api<Schema['MuteState']>('me/mutes', {
        method: 'PUT',
        body: {
          targetType: type,
          targetId: id,
          active: enabled,
          expiresAt:
            enabled && duration !== 'FOREVER'
              ? new Date(
                  Date.now() +
                    (duration === 'HOUR' ? 3600000 : duration === 'DAY' ? 86400000 : 604800000),
                ).toISOString()
              : null,
        },
      }),
    onSuccess: async (saved) => {
      setOpen(false);
      await refreshSocialVisibility(qc);
      notify(
        saved.active
          ? `${type === 'PROFILE' ? 'Person' : 'Community'} muted. Manage mutes in Account.`
          : 'Mute removed',
      );
    },
    onError: (e) => {
      if (!open) notify(e.message);
    },
  });
  return (
    <>
      <button
        type="button"
        className={menu ? undefined : 'secondary small'}
        aria-pressed={active}
        disabled={save.isPending}
        onClick={() => {
          if (!me) signIn();
          else if (active) save.mutate(false);
          else {
            save.reset();
            setOpen(true);
          }
        }}
      >
        {!menu && (active ? <Volume2 size={16} /> : <VolumeX size={16} />)}
        {active ? `Unmute ${kind}` : `Mute ${kind}`}
      </button>
      {open && (
        <Modal title={`Mute this ${kind}?`} onClose={() => !save.isPending && setOpen(false)}>
          <p>
            <strong>{label}</strong> will be muted in your feed, post search and reply alerts. You
            can still open public profiles, threads and bookmarks. Follows and public service
            progress stay available.
          </p>
          <label>
            Mute duration
            <select
              value={duration}
              onChange={(e) => setDuration(e.target.value)}
              disabled={save.isPending}
            >
              <option value="FOREVER">Until I unmute</option>
              <option value="HOUR">1 hour</option>
              <option value="DAY">24 hours</option>
              <option value="WEEK">7 days</option>
            </select>
          </label>
          <FormError error={save.error} />
          <div className="form-actions">
            <button
              type="button"
              className="secondary"
              disabled={save.isPending}
              onClick={() => setOpen(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              className="primary"
              disabled={save.isPending}
              onClick={() => save.mutate(true)}
            >
              {save.isPending ? 'Saving…' : `Mute ${kind}`}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}

export function MutedItemsSettings() {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const list = useInfiniteQuery({
    queryKey: ['muted-items', me?.profile.id || 'guest'],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['MutePage']>(
        `me/mutes${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!me,
    refetchInterval: 30000,
  });
  const remove = useMutation({
    mutationFn: (item: Schema['Mute']) =>
      api('me/mutes', {
        method: 'PUT',
        body: { targetType: item.targetType, targetId: item.targetId, active: false },
      }),
    onSuccess: async () => {
      await refreshSocialVisibility(qc);
      notify('Mute removed');
    },
  });
  const items = list.data?.pages.flatMap((page) => page.items) || [];
  return (
    <section className="account-panel" id="muted-items" aria-labelledby="muted-items-heading">
      <h2 id="muted-items-heading">
        <VolumeX size={19} /> Muted people and communities
      </h2>
      <p className="muted">
        Mutes are private and reversible. They do not remove follows or restrict another person’s
        account.
      </p>
      <button
        className="text-button"
        type="button"
        disabled={list.isFetching}
        onClick={() => void qc.resetQueries({ queryKey: ['muted-items'] })}
      >
        Refresh mute list
      </button>
      <FormError error={remove.error} />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorState
          error={list.error}
          retry={() => void qc.resetQueries({ queryKey: ['muted-items'] })}
        />
      ) : !items.length ? (
        <Empty title="No muted items">
          Mute a person from their profile or post menu, or mute a community from its page.
        </Empty>
      ) : (
        <div className="mute-list">
          {items.map((item) => (
            <div key={item.id} className="mute-row" data-testid="mute-row">
              {item.targetType === 'PROFILE' ? (
                <Avatar name={item.target?.label || '?'} />
              ) : (
                <span className="mute-community-icon">
                  <Users size={20} />
                </span>
              )}
              <div>
                {item.target ? (
                  <Link
                    href={`/${item.targetType === 'PROFILE' ? 'profiles' : 'communities'}/${item.targetId}`}
                  >
                    <strong>{item.target.label}</strong>
                    <small>
                      {item.targetType === 'PROFILE' ? '@' : 'j/'}
                      {item.target.handle}
                    </small>
                  </Link>
                ) : (
                  <strong>
                    {item.targetType === 'PROFILE' ? 'Unavailable person' : 'Unavailable community'}
                  </strong>
                )}
                <small>
                  {!item.active
                    ? 'Expired'
                    : item.expiresAt
                      ? `Muted until ${new Date(item.expiresAt).toLocaleString()}`
                      : 'Muted until you remove it'}
                </small>
              </div>
              <button
                className="secondary small"
                type="button"
                disabled={remove.isPending}
                aria-label={`Remove mute for ${item.target?.label || 'unavailable item'}`}
                onClick={() => remove.mutate(item)}
              >
                Remove mute
              </button>
            </div>
          ))}
          {list.hasNextPage && (
            <button
              className="secondary small"
              disabled={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {list.isFetchingNextPage ? 'Loading…' : 'Load more muted items'}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
