import type { Schema } from './api';

type Comment = Schema['Comment'];

// Index once, then walk the loaded forest. Missing parents remain explicit
// roots; no unavailable author's identity or body is inferred from a reply.
export function conversation(items: Comment[], collapsed: ReadonlySet<string>) {
  const byId = new Map(items.map((c) => [c.id, c]));
  const children = new Map<string, Comment[]>();
  const roots: Comment[] = [];
  for (const c of byId.values()) {
    if (c.parentId && c.parentId !== c.id && byId.has(c.parentId)) {
      const branch = children.get(c.parentId) || [];
      branch.push(c);
      children.set(c.parentId, branch);
    } else roots.push(c);
  }
  const ordered: { comment: Comment; hidden: boolean }[] = [];
  const replyCounts = new Map<string, number>();
  const seen = new Set<string>();
  function append(c: Comment, hidden = false): number {
    if (seen.has(c.id)) return 0;
    seen.add(c.id);
    ordered.push({ comment: c, hidden });
    let replies = 0;
    for (const child of children.get(c.id) || [])
      replies += append(child, hidden || collapsed.has(c.id));
    replyCounts.set(c.id, replies);
    return 1 + replies;
  }
  roots.forEach((c) => append(c));
  // Defensive fallback avoids losing rows if imported data has broken ancestry.
  byId.forEach((c) => append(c));
  return { byId, children, ordered, replyCounts };
}
