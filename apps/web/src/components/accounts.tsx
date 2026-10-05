'use client';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ShieldCheck, LogOut, Check, KeyRound } from 'lucide-react';
import { api, type Schema, type Me } from '@/lib/api';
import { Avatar, Modal, Loading, ErrorState, Empty, FormError, useSession } from './ui';
import { BlockedPeopleSettings } from './profiles';
import { NotificationSettings, MutedItemsSettings } from './preferences';
import { MyContentReports } from './content-reports';

export function Accounts({ onClose }: { onClose: () => void }) {
  const q = useQuery({
    queryKey: ['auth-config'],
    queryFn: () => api<Schema['AuthConfig']>('auth/config'),
  });
  if (q.data?.mode === 'demo') return <DemoAccounts onClose={onClose} />;
  return (
    <OIDCAccounts config={q.data} error={q.error} retry={() => q.refetch()} onClose={onClose} />
  );
}

function OIDCAccounts({
  config,
  error,
  retry,
  onClose,
}: {
  config?: Schema['AuthConfig'];
  error: Error | null;
  retry: () => void;
  onClose: () => void;
}) {
  const { me } = useSession();
  const pathname = usePathname();
  const qc = useQueryClient();
  const logout = useMutation({
    mutationFn: () => api('me/logout', { method: 'POST' }),
    onSuccess: async () => {
      await qc.cancelQueries();
      qc.clear();
      qc.setQueryData(['me'], null);
      onClose();
    },
  });
  return (
    <Modal title={me ? 'Your account' : 'Welcome to JanSetu'} onClose={onClose}>
      {error ? (
        <ErrorState error={error} retry={retry} />
      ) : !config ? (
        <Loading />
      ) : (
        <>
          {me && (
            <div className="account-identity">
              <Avatar name={me.profile.displayName} />
              <div>
                <strong>{me.profile.displayName}</strong>
                <small>@{me.profile.handle}</small>
              </div>
            </div>
          )}
          <p className="muted">
            Sign in through our identity provider. Choose your public name in your account settings.
          </p>
          <a
            className="primary login-link"
            href={`${config.loginPath}?returnTo=${encodeURIComponent(pathname)}`}
          >
            <KeyRound size={18} />
            {me ? 'Sign in to another account' : 'Continue to sign in'}
          </a>
          {me && (
            <Link className="account-security-link" href="/account" onClick={onClose}>
              <ShieldCheck size={18} />
              Profile and account security
            </Link>
          )}
          <details className="demo-credentials">
            <summary>Local demonstration accounts</summary>
            <p>
              These accounts are fictional. Password for each: <code>jansetu-demo</code>
            </p>
            <dl>
              <dt>Resident</dt>
              <dd>ananya or rohan</dd>
              <dt>Coordinator & moderator</dt>
              <dd>coordinator</dd>
              <dt>Agency officer</dt>
              <dd>cityworks</dd>
              <dt>Independent verifier</dt>
              <dd>verifier</dd>
              <dt>New account</dt>
              <dd>new-neighbour</dd>
            </dl>
          </details>
          {me && (
            <button
              className="text-button logout"
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
            >
              <LogOut size={16} />
              Sign out
            </button>
          )}
          <FormError error={logout.error} />
        </>
      )}
    </Modal>
  );
}

function DemoAccounts({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { me, notify } = useSession();
  const q = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api<Schema['Accounts']>('dev/accounts'),
  });
  const choose = useMutation({
    mutationFn: async (id: string | null) => {
      await qc.cancelQueries();
      if (id) {
        await api('dev/session', { method: 'POST', body: { principalId: id } });
        return api<Me>('me');
      }
      await api('me/logout', { method: 'POST' });
      return null;
    },
    onSuccess: (next) => {
      qc.clear();
      qc.setQueryData(['me'], next);
      notify('Account updated');
      onClose();
    },
  });
  return (
    <Modal title="Choose a demo account" onClose={onClose}>
      <p className="muted">Fictional accounts for explicitly enabled local development.</p>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => q.refetch()} />
      ) : (
        <div className="accounts-list">
          {q.data.items.map((a) => (
            <button
              className="account-option"
              disabled={choose.isPending}
              key={a.id}
              onClick={() => choose.mutate(a.id)}
            >
              <Avatar name={a.profile.displayName} />
              <span>
                <strong>{a.profile.displayName}</strong>
                <small>{a.role}</small>
              </span>
              {me?.profile.id === a.profile.id && <Check size={18} />}
            </button>
          ))}
        </div>
      )}
      <FormError error={choose.error} />
      {me && (
        <>
          <Link className="account-security-link" href="/account" onClick={onClose}>
            Profile and account security
          </Link>
          <button
            className="text-button logout"
            disabled={choose.isPending}
            onClick={() => choose.mutate(null)}
          >
            <LogOut size={16} />
            Sign out
          </button>
        </>
      )}
    </Modal>
  );
}

export function AccountSecurity() {
  const { me, signIn } = useSession();
  if (!me)
    return (
      <Empty title="Your account, your control">
        <p>Sign in to manage your profile and active sessions.</p>
        <button className="primary" onClick={signIn}>
          Sign in
        </button>
      </Empty>
    );
  return (
    <div className="account-page">
      <div className="section-intro">
        <span className="eyebrow">YOUR ACCOUNT</span>
        <h1>Feel at home. Stay in control.</h1>
        <p>Your public profile, blocked people, and the places where you are signed in.</p>
        <Link className="text-button" href={`/profiles/${me.profile.id}`}>
          View public profile
        </Link>
      </div>
      <ProfileEditor key={me.profile.version} me={me} />
      <BlockedPeopleSettings />
      <NotificationSettings />
      <MutedItemsSettings />
      <MyContentReports />
      <Sessions />
    </div>
  );
}
function ProfileEditor({ me }: { me: Me }) {
  const qc = useQueryClient();
  const { notify } = useSession();
  const [handle, setHandle] = useState(me.profile.handle);
  const [displayName, setDisplayName] = useState(me.profile.displayName);
  const [bio, setBio] = useState(me.profile.bio || '');
  const save = useMutation({
    mutationFn: () =>
      api('me/profile', {
        method: 'PATCH',
        version: me.profile.version,
        body: { handle, displayName, bio },
      }),
    onSuccess: async () => {
      await qc.invalidateQueries();
      notify('Your public profile is updated');
    },
  });
  return (
    <section className="account-panel" aria-labelledby="profile-heading">
      <h2 id="profile-heading">Your public profile</h2>
      <p className="muted">
        This name and handle appear alongside your posts. Your sign-in identity stays private.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div>
          <label htmlFor="profile-name">Display name</label>
          <input
            id="profile-name"
            required
            maxLength={80}
            autoComplete="off"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="profile-handle">Handle</label>
          <input
            id="profile-handle"
            aria-describedby="profile-handle-hint"
            required
            pattern="[a-z][a-z0-9_]{2,29}"
            minLength={3}
            maxLength={30}
            autoComplete="off"
            spellCheck={false}
            value={handle}
            onChange={(e) => setHandle(e.target.value)}
          />
          <small id="profile-handle-hint">
            3–30 lowercase letters, numbers, or underscores. Start with a letter.
          </small>
        </div>
        <div>
          <label htmlFor="profile-bio">Bio</label>
          <textarea
            id="profile-bio"
            maxLength={500}
            rows={3}
            value={bio}
            onChange={(e) => setBio(e.target.value)}
          />
        </div>
        <FormError error={save.error} />
        <div className="form-actions">
          <button className="primary" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : 'Save profile'}
          </button>
        </div>
      </form>
    </section>
  );
}
function Sessions() {
  const qc = useQueryClient();
  const { notify } = useSession();
  const q = useQuery({
    queryKey: ['sessions'],
    queryFn: () => api<{ items: Schema['AccountSession'][] }>('me/sessions'),
    refetchInterval: 30000,
  });
  const revoke = useMutation({
    mutationFn: (id: string | null) =>
      id
        ? api('me/sessions/' + id, { method: 'DELETE' })
        : api('me/sessions/revoke-others', { method: 'POST' }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['sessions'] });
      notify('Other sessions signed out');
    },
  });
  const currentLogout = useMutation({
    mutationFn: () => api('me/logout', { method: 'POST' }),
    onSuccess: async () => {
      await qc.cancelQueries();
      qc.clear();
      qc.setQueryData(['me'], null);
      notify('You are signed out');
    },
  });
  const when = (value: string) =>
    new Date(value).toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' });
  return (
    <section className="account-panel" aria-labelledby="sessions-heading">
      <h2 id="sessions-heading">
        <ShieldCheck size={20} />
        Where you are signed in
      </h2>
      <p className="muted">
        Sessions expire after 30 minutes of inactivity or 12 hours. Signing out here ends access to
        JanSetu on that session.
      </p>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => q.refetch()} />
      ) : (
        <>
          <ul className="sessions-list">
            {q.data.items.map((s) => (
              <li key={s.id}>
                <div>
                  <strong>{s.current ? 'This session' : 'Another session'}</strong>
                  <small>Signed in {when(s.createdAt)}</small>
                  <small>Last active {when(s.lastSeenAt)}</small>
                  <small>Expires {when(s.expiresAt)}</small>
                </div>
                {s.current ? (
                  <span className="badge good">Current</span>
                ) : (
                  <button
                    className="text-button"
                    disabled={revoke.isPending}
                    onClick={() => revoke.mutate(s.id)}
                  >
                    Sign out session
                  </button>
                )}
              </li>
            ))}
          </ul>
          <div className="session-actions">
            <button
              className="secondary"
              disabled={revoke.isPending || q.data.items.filter((s) => !s.current).length === 0}
              onClick={() => revoke.mutate(null)}
            >
              Sign out other sessions
            </button>
            <button
              className="text-button"
              disabled={currentLogout.isPending}
              onClick={() => currentLogout.mutate()}
            >
              Sign out here
            </button>
          </div>
        </>
      )}
      <FormError error={revoke.error || currentLogout.error} />
    </section>
  );
}
