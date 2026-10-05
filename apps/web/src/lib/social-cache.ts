import type { QueryClient } from '@tanstack/react-query';

// Drop visible and inactive snapshots after visibility or moderation changes. Refetching
// alone would retain old content while permission checks are still in flight.
export async function refreshSocialVisibility(client: QueryClient) {
  const roots = new Set([
    'feed',
    'search',
    'post',
    'comments',
    'public-profile',
    'profile-posts',
    'blocked-people',
    'activity',
    'activity-summary',
    'muted-items',
    'communities',
    'community',
    'content-reports',
    'moderation-decisions',
    'appeals',
    'appeal-review',
    'reviews',
  ]);
  const filter = {
    predicate: (query: { queryKey: readonly unknown[] }) => roots.has(String(query.queryKey[0])),
  };
  await client.cancelQueries(filter);
  await client.resetQueries(filter);
}
