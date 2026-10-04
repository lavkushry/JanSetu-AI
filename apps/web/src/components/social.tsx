'use client';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowUp,
  ArrowDown,
  Bookmark,
  MessageCircle,
  Repeat2,
  Share2,
  MoreHorizontal,
  ImagePlus,
  Send,
  MapPin,
  ShieldCheck,
  Users,
  Check,
  Plus,
} from 'lucide-react';
import {
  api,
  ago,
  dateLabel,
  readable,
  type Post,
  type Receipt,
  type Community,
  type Schema,
} from '@/lib/api';
import { Avatar, Badge, Modal, FormError, Loading, ErrorState, Empty, useSession } from './ui';

export function PostCard({
  post: p,
  detail = false,
  onEdit,
}: {
  post: Post;
  detail?: boolean;
  onEdit?: (p: Post) => void;
}) {
  const { me, notify, signIn } = useSession();
  const qc = useQueryClient();
  const [pending, setPending] = useState(false);
  const [confirm, setConfirm] = useState<'delete' | 'block' | null>(null);
  async function command(path: string, body: unknown, method = 'PUT', version?: number) {
    if (!me) {
      signIn();
      return;
    }
    if (pending) return;
    setPending(true);
    try {
      await api(path, { method, body, version });
      await qc.invalidateQueries();
      if (path.includes('blocks')) notify('Person blocked. Their content is now hidden.');
      if (path.includes('following')) notify('Following this person');
    } catch (e) {
      notify(e instanceof Error ? e.message : 'Please try again');
    } finally {
      setPending(false);
      setConfirm(null);
    }
  }
  const reviewPending = p.candidate?.reviewState === 'PENDING';
  const displayedBody = p.state === 'PENDING' ? p.candidate?.body : p.body;
  return (
    <article
      className={`post-card ${p.kind === 'SHORT' ? 'short-post' : ''}`}
      data-testid={`post-${p.id}`}
    >
      <div className="post-meta">
        <Avatar name={p.author?.displayName || 'Deleted member'} />
        <div>
          <div className="meta-line">
            {p.community ? (
              <Link className="community-label" href={`/communities/${p.community.id}`}>
                j/{p.community.slug}
              </Link>
            ) : (
              <strong>{p.author?.displayName || 'Deleted member'}</strong>
            )}
            <span>· {ago(p.publishedAt || p.createdAt)}</span>
          </div>
          <div className="byline">
            {p.community ? p.author?.displayName : `@${p.author?.handle || 'deleted'}`}{' '}
            <span className="post-kind">{readable(p.kind)}</span>
          </div>
        </div>
        <details className="menu">
          <summary aria-label="Post options">
            <MoreHorizontal size={20} />
          </summary>
          <div className="menu-panel">
            {p.viewer.canEdit && <button onClick={() => onEdit?.(p)}>Edit post</button>}
            {p.viewer.canDelete && (
              <button onClick={() => setConfirm('delete')}>Delete post</button>
            )}
            {p.author && p.author.id !== me?.profile.id && (
              <>
                <button onClick={() => command(`me/following/${p.author!.id}`, { enabled: true })}>
                  Follow {p.author.displayName.split(' ')[0]}
                </button>
                <button
                  onClick={() => {
                    if (!me) signIn();
                    else setConfirm('block');
                  }}
                >
                  Block person
                </button>
              </>
            )}
            <button
              onClick={() => {
                navigator.clipboard.writeText(`${location.origin}/posts/${p.id}`).then(
                  () => notify('Post link copied'),
                  () => notify('Could not copy the link'),
                );
              }}
            >
              Copy link
            </button>
          </div>
        </details>
      </div>
      <div className="post-content">
        {p.state === 'DELETED' ? (
          <p className="muted">This post was deleted.</p>
        ) : (
          <>
            {p.title &&
              (detail ? (
                <h1 className="post-title">{p.title}</h1>
              ) : (
                <Link href={`/posts/${p.id}`}>
                  <h2 className="post-title">{p.title}</h2>
                </Link>
              ))}
            <p className={detail ? 'post-body full' : 'post-body'}>{displayedBody}</p>
            {reviewPending && (
              <div className="review-note">
                {p.publishedRevision
                  ? 'Your edit is awaiting review. The approved version remains public.'
                  : 'Only you can see this post until a moderator approves it.'}
              </div>
            )}
            {p.candidate?.reviewState === 'REJECTED' && (
              <div className="review-note">This revision was restricted by a moderator.</div>
            )}
          </>
        )}
      </div>
      {p.state === 'PUBLISHED' && (
        <div className="post-actions">
          <div className="vote-control">
            <button
              disabled={pending || p.author?.id === me?.profile.id}
              className={p.viewer.vote === 1 ? 'active' : ''}
              aria-label="Upvote"
              aria-pressed={p.viewer.vote === 1}
              onClick={() => command(`posts/${p.id}/vote`, { value: p.viewer.vote === 1 ? 0 : 1 })}
            >
              <ArrowUp size={18} />
            </button>
            <span aria-label={`${p.stats.score} score`}>{p.stats.score}</span>
            <button
              disabled={pending || p.author?.id === me?.profile.id}
              className={p.viewer.vote === -1 ? 'active' : ''}
              aria-label="Downvote"
              aria-pressed={p.viewer.vote === -1}
              onClick={() =>
                command(`posts/${p.id}/vote`, { value: p.viewer.vote === -1 ? 0 : -1 })
              }
            >
              <ArrowDown size={18} />
            </button>
          </div>
          <Link className="action-button" href={`/posts/${p.id}`}>
            <MessageCircle size={17} />
            <span>
              {p.stats.comments} <span className="action-word">comments</span>
            </span>
          </Link>
          <button
            disabled={pending}
            className={`action-button ${p.viewer.reposted ? 'active' : ''}`}
            aria-label="Repost"
            aria-pressed={p.viewer.reposted}
            onClick={() => command(`posts/${p.id}/repost`, { enabled: !p.viewer.reposted })}
          >
            <Repeat2 size={17} />
            <span>{p.stats.reposts || ''}</span>
          </button>
          <button
            className="action-button"
            aria-label="Share post"
            onClick={() =>
              navigator.clipboard.writeText(`${location.origin}/posts/${p.id}`).then(
                () => notify('Post link copied'),
                () => notify('Could not copy the link'),
              )
            }
          >
            <Share2 size={17} />
          </button>
          <button
            disabled={pending}
            className={`action-button save ${p.viewer.bookmarked ? 'active' : ''}`}
            aria-label={p.viewer.bookmarked ? 'Remove bookmark' : 'Bookmark post'}
            aria-pressed={p.viewer.bookmarked}
            onClick={() => command(`posts/${p.id}/bookmark`, { enabled: !p.viewer.bookmarked })}
          >
            <Bookmark size={17} fill={p.viewer.bookmarked ? 'currentColor' : 'none'} />
          </button>
        </div>
      )}
      {confirm && (
        <Modal
          title={confirm === 'delete' ? 'Delete this post?' : 'Block this person?'}
          onClose={() => setConfirm(null)}
        >
          <p>
            {confirm === 'delete'
              ? 'The post will be replaced with a deleted notice.'
              : 'Their posts will be hidden, and existing follows between you will be removed.'}
          </p>
          <div className="form-actions">
            <button className="secondary" onClick={() => setConfirm(null)}>
              Cancel
            </button>
            <button
              className="danger"
              disabled={pending}
              onClick={() =>
                confirm === 'delete'
                  ? command(`posts/${p.id}`, undefined, 'DELETE', p.version)
                  : command(`me/blocks/${p.author!.id}`, { enabled: true })
              }
            >
              {pending ? 'Saving…' : confirm === 'delete' ? 'Delete post' : 'Block person'}
            </button>
          </div>
        </Modal>
      )}
    </article>
  );
}
export function ReceiptCard({
  receipt: r,
  detail = false,
}: {
  receipt: Receipt;
  detail?: boolean;
}) {
  const { me, notify, signIn } = useSession();
  const qc = useQueryClient();
  const follow = useMutation({
    mutationFn: () =>
      api(`case-receipts/${r.id}/follow`, { method: 'PUT', body: { following: !r.following } }),
    onSuccess: () => qc.invalidateQueries(),
    onError: (e) => notify(e.message),
  });
  return (
    <article className="receipt-card">
      <div className="receipt-eyebrow">
        <span>
          <ShieldCheck size={15} /> SERVICE PROGRESS
        </span>
        <Badge state={r.state} />
      </div>
      {detail ? (
        <h1>{r.title}</h1>
      ) : (
        <Link href={`/cases/${r.id}`}>
          <h2>{r.title}</h2>
        </Link>
      )}
      <p>{r.summary}</p>
      <div className="receipt-location">
        <MapPin size={14} />
        {r.area}
        <span>· Reported {dateLabel(r.firstReportedAt)}</span>
      </div>
      <div className="responsibility-list">
        {r.responsibilities.map((v, i) => (
          <div key={i}>
            <span className="agency-dot" />
            <strong>{v.agency}</strong>
            <span>{readable(v.state)}</span>
          </div>
        ))}
      </div>
      <div className="receipt-footer">
        <span>
          {r.urgencyTier >= 2 ? 'Priority issue' : 'Community service issue'} ·{' '}
          {r.nextUpdateDueAt
            ? `Next update ${dateLabel(r.nextUpdateDueAt)}`
            : 'Update deadline unavailable'}
        </span>
        <button
          className={`secondary small ${r.following ? 'selected' : ''}`}
          disabled={follow.isPending}
          onClick={() => (me ? follow.mutate() : signIn())}
        >
          {r.following ? <Check size={14} /> : <Plus size={14} />}{' '}
          {r.following ? 'Following' : 'Follow progress'}
        </button>
      </div>
      {detail && (
        <div className="timeline">
          {r.events?.map((e) => (
            <div className="timeline-item" key={e.sequence}>
              <span className="timeline-dot" />
              <div>
                <strong>{readable(e.type)}</strong>
                <p>{e.text}</p>
                <small>
                  {e.actorType.toLowerCase()} · {dateLabel(e.at)}
                </small>
              </div>
            </div>
          ))}
        </div>
      )}
    </article>
  );
}

export function Composer({
  onClose,
  edit,
  communityId,
}: {
  onClose: () => void;
  edit?: Post;
  communityId?: string;
}) {
  const { me, notify } = useSession();
  const qc = useQueryClient();
  const communities = useQuery({
    queryKey: ['communities'],
    queryFn: () => api<{ items: Community[] }>('communities'),
  });
  const draftKey = `jansetu.post-draft.${me?.profile.id}`;
  const [kind, setKind] = useState<Schema['PostInput']['kind']>(edit?.kind || 'DISCUSSION');
  const [cid, setCid] = useState(edit?.community?.id || communityId || '');
  const [title, setTitle] = useState(edit?.candidate?.title || edit?.title || '');
  const [body, setBody] = useState(edit?.candidate?.body || edit?.body || '');
  const [lang, setLang] = useState(edit?.languageTag || 'en-IN');
  const [keep, setKeep] = useState(false);
  const key = useRef(crypto.randomUUID());
  const [resume, setResume] = useState(false);
  useEffect(() => {
    if (!edit) setResume(!!localStorage.getItem(draftKey));
  }, [draftKey, edit]);
  useEffect(() => {
    if (keep && !edit) {
      localStorage.setItem(draftKey, JSON.stringify({ kind, cid, title, body, lang }));
    }
  }, [keep, kind, cid, title, body, lang, draftKey, edit]);
  function restore() {
    try {
      const d = JSON.parse(localStorage.getItem(draftKey) || '{}');
      setKind(d.kind || 'DISCUSSION');
      setCid(d.cid || '');
      setTitle(d.title || '');
      setBody(d.body || '');
      setLang(d.lang || 'en-IN');
      setKeep(true);
      setResume(false);
    } catch {
      localStorage.removeItem(draftKey);
    }
  }
  const send = useMutation({
    mutationFn: async () => {
      const content: Schema['PostEdit'] = {
        title: title.trim() || null,
        body,
        languageTag: lang,
        mediaIds: [],
        submitForReview: true,
      };
      return edit
        ? api<Post>(`posts/${edit.id}`, { method: 'PATCH', body: content, version: edit.version })
        : api<Post>('posts', {
            method: 'POST',
            body: { ...content, kind, communityId: cid || null } satisfies Schema['PostInput'],
            key: key.current,
          });
    },
    onSuccess: (p) => {
      localStorage.removeItem(draftKey);
      qc.invalidateQueries();
      notify('Submitted for review. Open your post to see its status.');
      onClose();
      window.location.assign(`/posts/${p.id}`);
    },
  });
  const unavailable = () =>
    notify('Image upload and OCR are not enabled yet. You can write your description as text.');
  return (
    <Modal title={edit ? 'Edit your post' : 'Start a conversation'} onClose={onClose} wide>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          send.mutate();
        }}
      >
        {resume && (
          <div className="draft-banner">
            You have a saved draft.{' '}
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
        <div className="composer-person">
          <Avatar name={me?.profile.displayName || 'Resident'} />
          <div>
            <strong>{me?.profile.displayName}</strong>
            <small>Your post will be reviewed before publication.</small>
          </div>
        </div>
        <div className="segmented">
          {(['DISCUSSION', 'SHORT', 'QUESTION'] as const).map((v) => (
            <button
              type="button"
              disabled={!!edit}
              aria-pressed={kind === v}
              key={v}
              className={kind === v ? 'selected' : ''}
              onClick={() => setKind(v)}
            >
              {v === 'SHORT' ? 'Quick thought' : readable(v)}
            </button>
          ))}
        </div>
        <label>
          Community
          <select
            aria-label="Community"
            required={kind !== 'SHORT'}
            disabled={!!edit}
            value={cid}
            onChange={(e) => setCid(e.target.value)}
          >
            <option value="">
              {kind === 'SHORT' ? 'Your public profile' : 'Choose a community'}
            </option>
            {communities.data?.items.map((c) => (
              <option key={c.id} value={c.id}>
                j/{c.slug}
              </option>
            ))}
          </select>
        </label>
        {kind !== 'SHORT' && (
          <label>
            Title
            <input
              required
              maxLength={180}
              placeholder="Give your conversation a clear title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
        )}
        <label>
          {kind === 'QUESTION' ? 'Your question' : 'What’s on your mind?'}
          <textarea
            required
            autoFocus
            rows={6}
            maxLength={kind === 'SHORT' ? 1000 : 8000}
            placeholder="Share an idea, ask a question, or start a local conversation…"
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </label>
        <div className="composer-tools">
          <button type="button" className="text-button" onClick={unavailable}>
            <ImagePlus size={18} /> Add image
          </button>
          <span>
            {body.length}/{kind === 'SHORT' ? 1000 : 8000}
          </span>
        </div>
        <label>
          Text language
          <select aria-label="Text language" value={lang} onChange={(e) => setLang(e.target.value)}>
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
        {!edit && (
          <label className="checkbox">
            <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
            Save this draft on this device
          </label>
        )}
        <FormError error={send.error} />
        <div className="form-actions">
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="primary" disabled={send.isPending}>
            <Send size={16} />
            {send.isPending ? 'Submitting…' : 'Submit for review'}
          </button>
        </div>
      </form>
    </Modal>
  );
}

export function Thread({ id, onEdit }: { id: string; onEdit: (p: Post) => void }) {
  const { me, notify, signIn } = useSession();
  const qc = useQueryClient();
  const post = useQuery({ queryKey: ['post', id], queryFn: () => api<Post>(`posts/${id}`) });
  const comments = useQuery({
    queryKey: ['comments', id],
    queryFn: () => api<{ items: Schema['Comment'][] }>(`posts/${id}/comments`),
    enabled: post.isSuccess,
  });
  const [body, setBody] = useState('');
  const [parent, setParent] = useState<Schema['Comment'] | null>(null);
  const key = useRef(crypto.randomUUID());
  const send = useMutation({
    mutationFn: () =>
      api(`posts/${id}/comments`, {
        method: 'POST',
        body: {
          body,
          languageTag: 'en-IN',
          parentId: parent?.id || null,
        } satisfies Schema['CommentInput'],
        key: key.current,
      }),
    onSuccess: () => {
      setBody('');
      setParent(null);
      key.current = crypto.randomUUID();
      qc.invalidateQueries();
      notify('Comment submitted for review');
    },
  });
  const [edit, setEdit] = useState<Schema['Comment'] | null>(null);
  const [edited, setEdited] = useState('');
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [deletion, setDeletion] = useState<Schema['Comment'] | null>(null);
  async function modify(c: Schema['Comment'], remove = false) {
    setBusy(true);
    setError(null);
    try {
      await api(`comments/${c.id}`, {
        method: remove ? 'DELETE' : 'PATCH',
        body: remove ? undefined : { body: edited, languageTag: 'en-IN' },
        version: c.version,
      });
      await qc.invalidateQueries();
      setEdit(null);
      setDeletion(null);
      notify(remove ? 'Comment deleted' : 'Edit submitted for review');
    } catch (e) {
      setError(e instanceof Error ? e : new Error('Please try again'));
    } finally {
      setBusy(false);
    }
  }
  if (post.isPending) return <Loading />;
  if (post.error) return <ErrorState error={post.error} retry={() => post.refetch()} />;
  const ordered: Schema['Comment'][] = [];
  const all = comments.data?.items || [];
  const seen = new Set<string>();
  function append(c: Schema['Comment']) {
    if (seen.has(c.id)) return;
    seen.add(c.id);
    ordered.push(c);
    all.filter((v) => v.parentId === c.id).forEach(append);
  }
  all.filter((c) => !c.parentId || !all.some((v) => v.id === c.parentId)).forEach(append);
  all.forEach(append);
  return (
    <>
      <PostCard post={post.data} detail onEdit={onEdit} />
      <section className="thread">
        <h2>
          Conversation <span>{all.length}</span>
        </h2>
        {post.data.state === 'PUBLISHED' &&
          (me ? (
            <form
              className="reply-composer"
              onSubmit={(e) => {
                e.preventDefault();
                send.mutate();
              }}
            >
              {parent && (
                <p>
                  Replying to {parent.author?.displayName}
                  <button type="button" className="text-button" onClick={() => setParent(null)}>
                    Cancel reply
                  </button>
                </p>
              )}
              <label htmlFor="reply">Add to the conversation</label>
              <textarea
                id="reply"
                maxLength={4000}
                required
                rows={3}
                placeholder="Be constructive. Share what you know…"
                value={body}
                onChange={(e) => setBody(e.target.value)}
              />
              <div className="form-actions">
                <small>Comments are reviewed before publication.</small>
                <button className="primary small" disabled={send.isPending}>
                  {send.isPending ? 'Submitting…' : 'Submit comment'}
                </button>
              </div>
              <FormError error={send.error} />
            </form>
          ) : (
            <button className="secondary" onClick={signIn}>
              Sign in to join the conversation
            </button>
          ))}
        {comments.isPending ? (
          <Loading />
        ) : comments.error ? (
          <ErrorState error={comments.error} retry={() => comments.refetch()} />
        ) : ordered.length === 0 ? (
          <Empty title="Be the first to join in">
            Thoughtful conversations start with one comment.
          </Empty>
        ) : (
          ordered.map((c) => (
            <article
              className="comment"
              key={c.id}
              style={{ marginLeft: `${Math.min(c.depth, 3) * 16}px` }}
            >
              <div className="comment-meta">
                <Avatar name={c.author?.displayName || 'Deleted member'} size="small" />
                <strong>{c.author?.displayName || 'Deleted member'}</strong>
                <span>· {ago(c.createdAt)}</span>
              </div>
              <p>
                {c.state === 'DELETED'
                  ? 'This comment was deleted.'
                  : c.state === 'PENDING'
                    ? c.candidate?.body
                    : c.body}
              </p>
              {c.candidate?.reviewState === 'PENDING' && (
                <small className="review-note">
                  Your {c.publishedVersion ? 'edit' : 'comment'} is awaiting review.
                </small>
              )}
              <div className="comment-actions">
                {c.state === 'PUBLISHED' && (
                  <button
                    onClick={() => {
                      if (!me) {
                        signIn();
                        return;
                      }
                      setParent(c);
                      document.getElementById('reply')?.focus();
                    }}
                  >
                    Reply
                  </button>
                )}
                {c.viewer.canEdit && (
                  <button
                    onClick={() => {
                      setEdit(c);
                      setEdited(c.candidate?.body || c.body || '');
                      setError(null);
                    }}
                  >
                    Edit
                  </button>
                )}
                {c.viewer.canDelete && (
                  <button
                    onClick={() => {
                      setDeletion(c);
                      setError(null);
                    }}
                  >
                    Delete
                  </button>
                )}
              </div>
            </article>
          ))
        )}
      </section>
      {edit && (
        <Modal title="Edit comment" onClose={() => setEdit(null)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              modify(edit);
            }}
          >
            <label>
              Comment
              <textarea
                autoFocus
                required
                maxLength={4000}
                rows={5}
                value={edited}
                onChange={(e) => setEdited(e.target.value)}
              />
            </label>
            <FormError error={error} />
            <div className="form-actions">
              <button className="primary" disabled={busy}>
                Submit edit for review
              </button>
            </div>
          </form>
        </Modal>
      )}
      {deletion && (
        <Modal title="Delete comment?" onClose={() => setDeletion(null)}>
          <p>Its place in the conversation will remain as a deleted notice.</p>
          <FormError error={error} />
          <div className="form-actions">
            <button className="secondary" onClick={() => setDeletion(null)}>
              Cancel
            </button>
            <button className="danger" disabled={busy} onClick={() => modify(deletion, true)}>
              Delete comment
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}

export function Communities({ onCompose }: { onCompose: (id: string) => void }) {
  const q = useQuery({
    queryKey: ['communities'],
    queryFn: () => api<{ items: Community[] }>('communities'),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return (
    <div className="community-grid">
      {q.data.items.map((c) => (
        <CommunityCard c={c} key={c.id} onCompose={onCompose} />
      ))}
    </div>
  );
}
export function CommunityCard({
  c,
  onCompose,
  detail = false,
}: {
  c: Community;
  onCompose: (id: string) => void;
  detail?: boolean;
}) {
  const { me, notify, signIn } = useSession();
  const qc = useQueryClient();
  const action = useMutation({
    mutationFn: (join: boolean) =>
      api(`communities/${c.id}/membership`, {
        method: 'PUT',
        body: { joined: join, rulesRevision: c.rulesRevision },
      }),
    onSuccess: () => qc.invalidateQueries(),
    onError: (e) => notify(e.message),
  });
  const follow = useMutation({
    mutationFn: () =>
      api(`communities/${c.id}/follow`, { method: 'PUT', body: { following: !c.following } }),
    onSuccess: () => qc.invalidateQueries(),
    onError: (e) => notify(e.message),
  });
  const [rules, setRules] = useState(false);
  return (
    <section className={`community-card ${detail ? 'community-cover' : ''}`}>
      <div className="community-art" aria-hidden="true">
        <Users size={28} />
        <span />
      </div>
      <div className="community-card-content">
        <Link href={`/communities/${c.id}`}>
          <h2>{c.title}</h2>
        </Link>
        <small>
          j/{c.slug} · {c.members} members
        </small>
        <p>{c.description}</p>
        <div className="community-buttons">
          <button
            className="primary small"
            disabled={action.isPending}
            onClick={() => {
              if (!me) signIn();
              else if (c.membershipState === 'ACTIVE') action.mutate(false);
              else setRules(true);
            }}
          >
            {c.membershipState === 'ACTIVE' ? 'Joined' : 'Join community'}
          </button>
          <button
            className="secondary small"
            disabled={follow.isPending}
            onClick={() => (me ? follow.mutate() : signIn())}
          >
            {c.following ? 'Following' : 'Follow'}
          </button>
          {detail && (
            <button className="text-button" onClick={() => (me ? onCompose(c.id) : signIn())}>
              Create post
            </button>
          )}
        </div>
        {detail && (
          <details className="community-rules">
            <summary>Community rules</summary>
            <p>{c.rules}</p>
          </details>
        )}
      </div>
      {rules && (
        <Modal title="Community rules" onClose={() => setRules(false)}>
          <p>{c.rules}</p>
          <p className="muted">By joining, you agree to follow these community rules.</p>
          <div className="form-actions">
            <button className="secondary" onClick={() => setRules(false)}>
              Cancel
            </button>
            <button
              className="primary"
              onClick={() => {
                action.mutate(true);
                setRules(false);
              }}
            >
              Agree and join
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
