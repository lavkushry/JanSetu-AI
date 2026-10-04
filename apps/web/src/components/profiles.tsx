'use client';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarDays, Check, Plus, ShieldBan, ArrowLeft, ArrowUpRight } from 'lucide-react';
import { api, APIError, dateLabel, type Post, type Schema } from '@/lib/api';
import { refreshSocialVisibility } from '@/lib/social-cache';
import { Avatar, Empty, ErrorState, FormError, Loading, Modal, useSession } from './ui';
import { PostCard } from './social';

export function PeopleResults({ profiles }: { profiles: Schema['PublicProfile'][] }) {
  if (!profiles.length) return null;
  return (
    <section className="people-results" aria-label="People search results">
      <h2>People</h2>
      <div className="people-grid">
        {profiles.map((p) => (
          <Link key={p.id} className="person-card" href={`/profiles/${p.id}`}>
            <Avatar name={p.displayName} />
            <div>
              <strong>{p.displayName}</strong>
              <span>@{p.handle}</span>
            </div>
            <ArrowUpRight size={16} aria-hidden="true" />
          </Link>
        ))}
      </div>
    </section>
  );
}

export function PublicProfilePage({ id, onEdit }: { id: string; onEdit: (post: Post) => void }) {
  const { me, signIn, notify } = useSession();
  const qc = useQueryClient();
  const router = useRouter();
  const [confirm, setConfirm] = useState(false);
  const viewer = me?.profile.id || 'guest';
  const profile = useQuery({
    queryKey: ['public-profile', id, viewer],
    queryFn: () => api<Schema['ProfileDetail']>(`profiles/${id}`),
  });
  const posts = useInfiniteQuery({
    queryKey: ['profile-posts', id, viewer],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['ProfilePosts']>(
        `profiles/${id}/posts${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!profile.data && !profile.error,
  });
  const follow = useMutation({
    mutationFn: () =>
      api(`me/following/${id}`, {
        method: 'PUT',
        body: { enabled: !profile.data?.viewer.following },
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['public-profile', id] });
      await qc.invalidateQueries({ queryKey: ['feed'] });
    },
  });
  const block = useMutation({
    mutationFn: () => api(`me/blocks/${id}`, { method: 'PUT', body: { enabled: true } }),
    onSuccess: async () => {
      setConfirm(false);
      router.push('/account#blocked-people');
      await refreshSocialVisibility(qc);
      notify('Person blocked. Manage your blocks in Account.');
    },
  });
  async function edit(post: Post) {
    // Fetch the private owner view only after an explicit edit request; public
    // timelines never contain an unpublished revision.
    try {
      onEdit(await api<Post>(`posts/${post.id}`));
    } catch (e) {
      notify(e instanceof Error ? e.message : 'Could not open this post.');
    }
  }
  if (profile.isPending) return <Loading />;
  if (profile.error) {
    if (profile.error instanceof APIError && profile.error.status === 404)
      return (
        <Empty title="Profile unavailable">
          <p>This profile is not available to view.</p>
          <Link className="text-button" href="/account">
            Manage your account
          </Link>
        </Empty>
      );
    return <ErrorState error={profile.error} retry={() => profile.refetch()} />;
  }
  const p = profile.data.profile;
  return (
    <div className="profile-page">
      <Link className="back-link" href="/">
        <ArrowLeft size={16} />
        Back to your feed
      </Link>
      <section className="public-profile" aria-label="Public profile">
        <div className="profile-cover" aria-hidden="true" />
        <div className="profile-details">
          <div className="profile-heading">
            <Avatar name={p.displayName} />
            <div>
              <h1>{p.displayName}</h1>
              <p className="muted">@{p.handle}</p>
            </div>
          </div>
          {p.bio && <p className="profile-bio">{p.bio}</p>}
          <p className="profile-joined muted">
            <CalendarDays size={15} />
            Joined {dateLabel(p.joinedAt)}
          </p>
          <div className="profile-controls">
            {profile.data.viewer.self ? (
              <Link className="secondary small" href="/account">
                Edit profile
              </Link>
            ) : (
              <>
                <button
                  type="button"
                  className={`primary small ${profile.data.viewer.following ? 'selected' : ''}`}
                  aria-pressed={profile.data.viewer.following}
                  disabled={follow.isPending || block.isPending}
                  onClick={() => (me ? follow.mutate() : signIn())}
                >
                  {profile.data.viewer.following ? <Check size={16} /> : <Plus size={16} />}
                  {follow.isPending
                    ? 'Saving…'
                    : profile.data.viewer.following
                      ? 'Unfollow person'
                      : 'Follow person'}
                </button>
                <button
                  type="button"
                  className="secondary small"
                  disabled={block.isPending || follow.isPending}
                  onClick={() => (me ? setConfirm(true) : signIn())}
                >
                  <ShieldBan size={16} />
                  Block person
                </button>
              </>
            )}
          </div>
          <FormError error={follow.error} />
        </div>
      </section>
      <div className="profile-post-heading">
        <h2>Published posts</h2>
        <p className="muted">Updates and conversations shared with the community.</p>
      </div>
      {posts.isPending ? (
        <Loading />
      ) : posts.error && !posts.data ? (
        <ErrorState error={posts.error} retry={() => posts.refetch()} />
      ) : (
        <>
          {!posts.data?.pages.some((page) => page.items.length) && (
            <Empty title="No published posts yet">Reviewed posts will appear here.</Empty>
          )}
          <div className="feed-list">
            {posts.data?.pages
              .flatMap((page) => page.items)
              .map((post) => (
                <PostCard key={post.id} post={post} onEdit={(post) => void edit(post)} />
              ))}
          </div>
          {posts.error && <FormError error={posts.error} />}
          {posts.hasNextPage && (
            <button
              type="button"
              className="secondary load-more"
              disabled={posts.isFetchingNextPage}
              onClick={() => void posts.fetchNextPage()}
            >
              {posts.isFetchingNextPage ? 'Loading…' : 'Load more posts'}
            </button>
          )}
        </>
      )}
      {confirm && (
        <Modal title="Block this person?" onClose={() => !block.isPending && setConfirm(false)}>
          <p>
            Their posts and profile will be hidden. Follows between you will be removed. You can
            unblock them in Account.
          </p>
          <FormError error={block.error} />
          <div className="form-actions">
            <button
              type="button"
              className="secondary"
              disabled={block.isPending}
              onClick={() => setConfirm(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              className="danger"
              disabled={block.isPending}
              onClick={() => block.mutate()}
            >
              {block.isPending ? 'Blocking…' : 'Block person'}
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

export function BlockedPeopleSettings() {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<Schema['BlockedPerson'] | null>(null);
  const list = useInfiniteQuery({
    queryKey: ['blocked-people', me?.profile.id],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['BlockedPeople']>(
        `me/blocks${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ''}`,
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled: !!me,
  });
  const unblock = useMutation({
    mutationFn: (id: string) => api(`me/blocks/${id}`, { method: 'PUT', body: { enabled: false } }),
    onSuccess: async () => {
      setSelected(null);
      await refreshSocialVisibility(qc);
      notify('Person unblocked. Previous follows have not been restored.');
    },
  });
  return (
    <section
      id="blocked-people"
      className="account-panel blocked-people"
      aria-labelledby="blocks-heading"
    >
      <h2 id="blocks-heading">Blocked people</h2>
      <p className="muted">
        Control whose posts and profile you see. Unblocking keeps previous follows removed.
      </p>
      {list.isPending ? (
        <Loading />
      ) : list.error && !list.data ? (
        <ErrorState error={list.error} retry={() => list.refetch()} />
      ) : (
        <>
          {!list.data?.pages.some((page) => page.items.length) && (
            <p className="blocks-empty">You haven’t blocked anyone.</p>
          )}
          {list.data?.pages
            .flatMap((page) => page.items)
            .map((b) => (
              <div key={b.profileId} className="blocked-person">
                <Avatar name={b.profile?.displayName || 'Unavailable account'} />
                <div>
                  <strong>{b.profile?.displayName || 'Unavailable account'}</strong>
                  {b.profile && <span>@{b.profile.handle}</span>}
                  <small>Blocked {dateLabel(b.blockedAt)}</small>
                </div>
                <button
                  type="button"
                  className="secondary small"
                  disabled={unblock.isPending}
                  onClick={() => {
                    unblock.reset();
                    setSelected(b);
                  }}
                  aria-label={`Unblock ${b.profile?.displayName || 'unavailable account'}`}
                >
                  Unblock
                </button>
              </div>
            ))}
          {list.error && <FormError error={list.error} />}
          {list.hasNextPage && (
            <button
              type="button"
              className="secondary small"
              disabled={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {list.isFetchingNextPage ? 'Loading…' : 'Load more blocked people'}
            </button>
          )}
        </>
      )}
      {selected && (
        <Modal title="Unblock this person?" onClose={() => !unblock.isPending && setSelected(null)}>
          <p>
            Their public profile and posts may appear again. Previous follows will stay removed.
          </p>
          <FormError error={unblock.error} />
          <div className="form-actions">
            <button
              type="button"
              className="secondary"
              disabled={unblock.isPending}
              onClick={() => setSelected(null)}
            >
              Cancel
            </button>
            <button
              type="button"
              className="primary"
              disabled={unblock.isPending}
              onClick={() => unblock.mutate(selected.profileId)}
            >
              {unblock.isPending ? 'Unblocking…' : 'Unblock person'}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
