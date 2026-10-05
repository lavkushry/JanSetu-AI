'use client';
import Link from 'next/link';
import { useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, Check, Circle, RefreshCw, ShieldCheck, ArrowUpRight } from 'lucide-react';
import { api, ago, type Schema } from '@/lib/api';
import { Avatar, Empty, ErrorState, FormError, Loading, useSession } from './ui';
import { useNotificationPreference } from './preferences';

export function useActivitySummary(viewer?: string) {
  return useQuery({
    queryKey: ['activity-summary', viewer || 'guest'],
    queryFn: () => api<Schema['ActivitySummary']>('me/activity/summary'),
    enabled: !!viewer,
    refetchInterval: 30000,
  });
}

export function ActivityPage() {
  const { me, signIn } = useSession();
  const [filter, setFilter] = useState<'ALL' | 'SOCIAL' | 'CASES' | 'MODERATION'>('ALL');
  const qc = useQueryClient();
  const viewer = me?.profile.id || 'guest';
  const summary = useActivitySummary(me?.profile.id);
  const preference = useNotificationPreference(me?.profile.id);
  const list = useInfiniteQuery({
    queryKey: ['activity', filter, viewer],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['ActivityPage']>(
        `me/activity?filter=${filter}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!me,
    refetchInterval: 30000,
  });
  const read = useMutation({
    mutationFn: (item: Schema['Activity']) =>
      api<Schema['ActivityRead']>(`me/activity/${item.id}/read`, {
        method: 'PUT',
        body: { read: !item.readAt },
      }),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ['activity'] }),
        qc.invalidateQueries({ queryKey: ['activity-summary'] }),
      ]);
    },
    onError: async () => {
      // A source may have been withdrawn since this card was loaded.
      await qc.resetQueries({ queryKey: ['activity'] });
      await qc.invalidateQueries({ queryKey: ['activity-summary'] });
    },
  });
  const refresh = async () => {
    read.reset();
    await qc.resetQueries({ queryKey: ['activity', filter, viewer] });
    await qc.invalidateQueries({ queryKey: ['activity-summary'] });
  };
  if (!me)
    return (
      <Empty title="Your activity lives here">
        <p>Sign in to follow replies, public service progress and your private review outcomes.</p>
        <button className="primary" onClick={signIn}>
          Sign in
        </button>
      </Empty>
    );
  const items = list.data?.pages.flatMap((page) => page.items) || [];
  return (
    <>
      <div className="section-intro activity-intro">
        <span className="eyebrow">
          <Bell size={15} /> STAY IN THE LOOP
        </span>
        <h1>Activity</h1>
        <p>Your conversations, your city’s progress, and your private review outcomes.</p>
        <Link className="text-button" href="/account#activity-settings">
          Manage Activity preferences
        </Link>
        <div className="activity-toolbar">
          <span className="muted">
            {summary.data ? `${summary.data.unreadCount} unread` : 'Your private inbox'}
          </span>
          <button
            className="secondary small"
            onClick={() => void refresh()}
            disabled={list.isFetching}
          >
            <RefreshCw size={15} /> Refresh activity
          </button>
        </div>
      </div>
      {preference.data?.inApp === false && (
        <p className="activity-paused" role="status">
          In-app notifications are paused. Updates sent while paused won’t be added.{' '}
          <Link href="/account#activity-settings">Manage preferences</Link>
        </p>
      )}
      <div className="feed-tabs" role="group" aria-label="Activity filters">
        {(
          [
            ['ALL', 'All'],
            ['SOCIAL', 'Conversations'],
            ['CASES', 'Service progress'],
            ['MODERATION', 'Moderation'],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            className={filter === value ? 'selected' : ''}
            aria-pressed={filter === value}
            onClick={() => {
              read.reset();
              setFilter(value);
            }}
          >
            {label}
          </button>
        ))}
      </div>
      <p className="muted activity-note">
        Approved replies, reviewed public updates and your private review outcomes appear here. Read
        status is for your inbox.
      </p>
      <FormError error={read.error} />
      {summary.error && <ErrorState error={summary.error} retry={() => void summary.refetch()} />}
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorState error={list.error} retry={() => void refresh()} />
      ) : !items.length ? (
        <Empty title="You’re all caught up">
          <p>Replies, followed case updates and private review notices will appear here.</p>
          <Link className="text-button" href="/">
            Explore your feed <ArrowUpRight size={15} />
          </Link>
        </Empty>
      ) : (
        <div className="activity-list" aria-label="Activity inbox">
          {items.map((item) => (
            <article
              className={`activity-card ${item.readAt ? '' : 'unread'}`}
              key={item.id}
              data-testid="activity-card"
            >
              <span className="activity-avatar">
                {item.actor ? <Avatar name={item.actor.displayName} /> : <ShieldCheck size={24} />}
              </span>
              <div className="activity-content">
                <div className="activity-meta">
                  {item.actor ? (
                    <Link href={`/profiles/${item.actor.id}`}>{item.actor.displayName}</Link>
                  ) : (
                    <strong>
                      {item.kind === 'CASE_PROGRESS'
                        ? 'Public service progress'
                        : item.kind === 'CONTENT_REPORT_OUTCOME'
                          ? 'Private content report'
                          : 'Private moderation'}
                    </strong>
                  )}
                  <time dateTime={item.createdAt} title={new Date(item.createdAt).toLocaleString()}>
                    {ago(item.createdAt)}
                  </time>
                </div>
                <Link className="activity-target" href={activityTargetURL(item.target)}>
                  <p>{item.message}</p>
                  <span>
                    {item.target.title} <ArrowUpRight size={14} />
                  </span>
                </Link>
                <button
                  className="text-button activity-read"
                  aria-label={item.readAt ? 'Mark as unread' : 'Mark as read'}
                  disabled={read.isPending}
                  onClick={() => read.mutate(item)}
                >
                  {item.readAt ? <Circle size={14} /> : <Check size={14} />}
                  {item.readAt ? 'Mark as unread' : 'Mark as read'}
                </button>
              </div>
              {!item.readAt && <span className="activity-unread-dot" aria-label="Unread" />}
            </article>
          ))}
          {list.hasNextPage && (
            <button
              className="secondary load-more"
              disabled={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {list.isFetchingNextPage ? 'Loading…' : 'Load more activity'}
            </button>
          )}
        </div>
      )}
    </>
  );
}

function activityTargetURL(target: Schema['Activity']['target']) {
  switch (target.kind) {
    case 'POST':
      return `/posts/${target.id}`;
    case 'RECEIPT':
      return `/cases/${target.id}`;
    case 'MODERATION_DECISION':
      return `/account/moderation-decisions/${target.id}`;
    case 'APPEAL':
      return `/account/appeals/${target.id}`;
    case 'CONTENT_REPORT':
      return `/account/content-reports/${target.id}`;
  }
}
