'use client';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';
import {
  QueryClient,
  QueryClientProvider,
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import {
  Home,
  Compass,
  Users,
  Bookmark,
  FileText,
  ShieldCheck,
  Search,
  Plus,
  MapPin,
  ChevronDown,
  ArrowUpRight,
  Sun,
  Moon,
  Menu,
  X,
  SlidersHorizontal,
  ArrowLeft,
  Check,
  MessageSquare,
  ArrowRight,
  Bell,
} from 'lucide-react';
import { api, APIError, type Me, type Schema, type Post, type Community } from '@/lib/api';
import { SessionContext, Avatar, Modal, Loading, Empty, ErrorState, useSession } from './ui';
import { PostCard, ReceiptCard, Composer, Thread, Communities, CommunityCard } from './social';
import { ReportWizard, MyReports, ReceiptDetail } from './reports';
import { Studio } from './studio';
import { Accounts, AccountSecurity } from './accounts';
import { PublicProfilePage, PeopleResults } from './profiles';
import { ActivityPage, useActivitySummary } from './activity';

export default function JanSetu() {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 15000,
            retry: (count, e) => !(e instanceof APIError && e.status < 500) && count < 1,
            refetchOnWindowFocus: true,
          },
        },
      }),
  );
  return (
    <QueryClientProvider client={client}>
      <Application />
    </QueryClientProvider>
  );
}
function Logo() {
  return (
    <span className="brand">
      <span className="brand-symbol">
        <svg viewBox="0 0 32 32" fill="none" aria-hidden="true">
          <path
            d="M5 24V12m22 12V12M3 18h26M6 12c5 0 5 6 10 6s5-6 10-6M9 18v6m7-6v6m7-6v6"
            stroke="currentColor"
            strokeWidth="2.4"
            strokeLinecap="round"
          />
          <circle cx="6" cy="8" r="2" fill="currentColor" />
          <circle cx="26" cy="8" r="2" fill="currentColor" />
        </svg>
      </span>
      <span>
        JanSetu<span className="brand-dot">.</span>
      </span>
    </span>
  );
}
function Application() {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const qc = useQueryClient();
  const session = useQuery({
    queryKey: ['me'],
    refetchInterval: 30000,
    queryFn: async () => {
      try {
        return await api<Me>('me');
      } catch (e) {
        if (e instanceof APIError && e.status === 401) return null;
        throw e;
      }
    },
  });
  const me = session.data || null;
  const activity = useActivitySummary(me?.profile.id);
  const unread = activity.data?.unreadCount || 0;
  const previousProfile = useRef<string | null>(null);
  useEffect(() => {
    const next = me?.profile.id || null;
    if (previousProfile.current && previousProfile.current !== next) {
      void qc.cancelQueries({ predicate: (query) => query.queryKey[0] !== 'me' });
      qc.removeQueries({ predicate: (query) => query.queryKey[0] !== 'me' });
    }
    previousProfile.current = next;
  }, [me?.profile.id, qc]);
  const [account, setAccount] = useState(false);
  const [composer, setComposer] = useState<{ edit?: Post; communityId?: string } | null>(null);
  const [report, setReport] = useState(false);
  const [menu, setMenu] = useState(false);
  const [settings, setSettings] = useState(false);
  const [prefsReady, setPrefsReady] = useState(false);
  const [theme, setTheme] = useState('light');
  const [density, setDensity] = useState('comfortable');
  const [toast, setToast] = useState('');
  const [search, setSearch] = useState(params.get('q') || '');
  useEffect(() => {
    setTheme(localStorage.getItem('jansetu.theme') || 'light');
    setDensity(localStorage.getItem('jansetu.density') || 'comfortable');
    setPrefsReady(true);
  }, []);
  useEffect(() => {
    if (prefsReady) {
      document.documentElement.dataset.theme = theme;
      localStorage.setItem('jansetu.theme', theme);
    }
  }, [theme, prefsReady]);
  useEffect(() => {
    if (prefsReady) {
      document.documentElement.dataset.density = density;
      localStorage.setItem('jansetu.density', density);
    }
  }, [density, prefsReady]);
  useEffect(() => {
    if (toast) {
      const timeout = setTimeout(() => setToast(''), 5500);
      return () => clearTimeout(timeout);
    }
  }, [toast]);
  useEffect(() => {
    setMenu(false);
    setSearch(params.get('q') || '');
  }, [pathname, params]);
  const compose = (cid?: string) => {
    if (!me) {
      setAccount(true);
      return;
    }
    setComposer({ communityId: cid });
  };
  const openReport = () => {
    if (!me) {
      setAccount(true);
      return;
    }
    setReport(true);
  };
  const nav = [
    { href: '/', label: 'Home', icon: Home },
    { href: '/explore', label: 'Explore', icon: Compass },
    { href: '/communities', label: 'Communities', icon: Users },
    { href: '/bookmarks', label: 'Bookmarks', icon: Bookmark },
    { href: '/activity', label: 'Activity', icon: Bell },
    { href: '/my-reports', label: 'My reports', icon: FileText },
    { href: '/account', label: 'Account', icon: ShieldCheck },
  ];
  const isStaff = !!me && (me.roles.length > 0 || me.agencies.length > 0);
  return (
    <SessionContext.Provider value={{ me, notify: setToast, signIn: () => setAccount(true) }}>
      <div className="demo-strip">
        <span className="demo-dot" /> SYNTHETIC LOCAL DEMO{' '}
        <span className="demo-strip-detail">Fictional people, agencies, and service reports</span>
      </div>
      <header className="topbar">
        <button
          className="icon-button mobile-menu"
          aria-label="Open navigation"
          onClick={() => setMenu(true)}
        >
          <Menu size={22} />
        </button>
        <Link href="/" aria-label="JanSetu home">
          <Logo />
        </Link>
        <form
          className="global-search"
          onSubmit={(e) => {
            e.preventDefault();
            if (search.trim().length >= 2)
              router.push(`/search?q=${encodeURIComponent(search.trim())}`);
          }}
        >
          <Search size={18} />
          <input
            aria-label="Search your city"
            placeholder="Search conversations, communities, issues…"
            minLength={2}
            maxLength={120}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <kbd>↵</kbd>
        </form>
        <div className="topbar-actions">
          <Link
            href="/activity"
            className="icon-button activity-bell"
            aria-label={unread ? `Activity, ${unread} unread` : 'Activity'}
          >
            <Bell size={20} />
            {unread > 0 && (
              <span className="activity-count" aria-hidden="true">
                {unread > 99 ? '99+' : unread}
              </span>
            )}
          </Link>
          <span className="city-pill">
            <MapPin size={15} />
            Bengaluru
            <span className="online-dot" />
          </span>
          <button
            className="icon-button theme-button"
            aria-label={theme === 'dark' ? 'Use light theme' : 'Use dark theme'}
            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
          >
            {theme === 'dark' ? <Sun size={20} /> : <Moon size={20} />}
          </button>
          <button
            className="avatar-button"
            aria-label="Open account"
            onClick={() => setAccount(true)}
          >
            <Avatar name={me?.profile.displayName || 'Demo account'} size="small" />
          </button>
        </div>
      </header>
      {params.get('auth') === 'failed' && (
        <p className="auth-error" role="alert">
          Sign-in could not be completed. Open your account to try again.
        </p>
      )}
      <div className="app-layout">
        {menu && (
          <button
            className="nav-backdrop"
            aria-label="Close navigation"
            onClick={() => setMenu(false)}
          />
        )}
        <aside className={`sidebar ${menu ? 'open' : ''}`}>
          <div className="sidebar-close">
            <Logo />
            <button
              className="icon-button"
              aria-label="Close navigation"
              onClick={() => setMenu(false)}
            >
              <X size={20} />
            </button>
          </div>
          <nav aria-label="Main navigation">
            {nav.map(({ href, label, icon: Icon }) => (
              <Link
                key={href}
                className={`nav-item ${pathname === href ? 'selected' : ''}`}
                href={href}
              >
                <Icon size={21} />
                <span>{label}</span>
                {href === '/activity' && unread > 0 && (
                  <span className="nav-activity-count" aria-label={`${unread} unread`}>
                    {unread > 99 ? '99+' : unread}
                  </span>
                )}
                {href === '/' && <span className="nav-current-dot" />}
              </Link>
            ))}
            {isStaff && (
              <Link
                className={`nav-item ${pathname === '/studio' ? 'selected' : ''}`}
                href="/studio"
              >
                <ShieldCheck size={21} />
                Staff workspace
              </Link>
            )}
          </nav>
          <button className="primary create-button" onClick={() => compose()}>
            <Plus size={20} />
            Create a post
          </button>
          <SidebarCommunities />
          <div className="sidebar-report">
            <span className="report-icon">
              <MapPin size={22} />
            </span>
            <strong>
              See an issue?
              <br />
              Help your city move.
            </strong>
            <p>Report a service issue and follow its progress.</p>
            <button className="text-button" onClick={openReport}>
              Report an issue <ArrowUpRight size={16} />
            </button>
          </div>
          <div className="sidebar-bottom">
            <button className="nav-item" onClick={() => setSettings(true)}>
              <SlidersHorizontal size={19} />
              Appearance
            </button>
            <button className="account-control" onClick={() => setAccount(true)}>
              <Avatar name={me?.profile.displayName || 'Choose account'} />
              <span>
                <strong>{me?.profile.displayName || 'Explore JanSetu'}</strong>
                <small>{me ? `@${me.profile.handle}` : 'Sign in to JanSetu'}</small>
              </span>
              <ChevronDown size={15} />
            </button>
            <p className="sidebar-fine">Built for better neighbourhoods.</p>
          </div>
        </aside>
        <main id="main" className="main-content" tabIndex={-1}>
          {session.error && <ErrorState error={session.error} retry={() => session.refetch()} />}
          <Page
            pathname={pathname}
            term={params.get('q') || ''}
            onCompose={compose}
            onEdit={(p) => setComposer({ edit: p })}
            onReport={openReport}
          />
        </main>
        <RightRail onReport={openReport} />
      </div>
      <nav className="bottom-nav" aria-label="Mobile navigation">
        <Link href="/" className={pathname === '/' ? 'selected' : ''}>
          <Home size={21} />
          <span>Home</span>
        </Link>
        <Link href="/explore" className={pathname === '/explore' ? 'selected' : ''}>
          <Compass size={21} />
          <span>Explore</span>
        </Link>
        <button className="mobile-create" onClick={() => compose()} aria-label="Create post">
          <Plus size={24} />
        </button>
        <Link href="/my-reports" className={pathname === '/my-reports' ? 'selected' : ''}>
          <FileText size={21} />
          <span>Reports</span>
        </Link>
        <button onClick={() => setAccount(true)}>
          <Avatar name={me?.profile.displayName || 'Demo'} size="small" />
          <span>Account</span>
        </button>
      </nav>
      {toast && (
        <div className="toast" role="status">
          <Check size={18} />
          <span>{toast}</span>
          <button aria-label="Dismiss notification" onClick={() => setToast('')}>
            <X size={16} />
          </button>
        </div>
      )}
      {account && <Accounts onClose={() => setAccount(false)} />}{' '}
      {composer && (
        <Composer
          edit={composer.edit}
          communityId={composer.communityId}
          onClose={() => setComposer(null)}
        />
      )}{' '}
      {report && <ReportWizard onClose={() => setReport(false)} />}{' '}
      {settings && (
        <Modal title="Make JanSetu yours" onClose={() => setSettings(false)}>
          <p className="muted">Appearance preferences are saved on this device.</p>
          <label>
            Theme
            <select value={theme} onChange={(e) => setTheme(e.target.value)}>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </label>
          <label>
            Feed density
            <select value={density} onChange={(e) => setDensity(e.target.value)}>
              <option value="comfortable">Comfortable</option>
              <option value="compact">Compact</option>
            </select>
          </label>
          <div className="form-actions">
            <button className="primary" onClick={() => setSettings(false)}>
              Done
            </button>
          </div>
        </Modal>
      )}
    </SessionContext.Provider>
  );
}
function SidebarCommunities() {
  const q = useQuery({
    queryKey: ['communities'],
    queryFn: () => api<{ items: Community[] }>('communities'),
  });
  return (
    <div className="sidebar-communities">
      <h2>
        YOUR COMMUNITIES
        <Link href="/communities" aria-label="Browse communities">
          <Plus size={16} />
        </Link>
      </h2>
      {q.data?.items
        .filter((c) => c.following || c.membershipState === 'ACTIVE')
        .slice(0, 4)
        .map((c, i) => (
          <Link href={`/communities/${c.id}`} key={c.id}>
            <span className={`community-avatar color-${i}`}>
              <Users size={15} />
            </span>
            <span>j/{c.slug}</span>
          </Link>
        ))}
      {!q.data?.items.some((c) => c.following || c.membershipState === 'ACTIVE') && (
        <Link className="muted" href="/communities">
          Find your people <ArrowRight size={14} />
        </Link>
      )}
    </div>
  );
}
function RightRail({ onReport }: { onReport: () => void }) {
  const communities = useQuery({
    queryKey: ['communities'],
    queryFn: () => api<{ items: Community[] }>('communities'),
  });
  return (
    <aside className="right-rail">
      <section className="city-context">
        <div className="eyebrow">
          <span className="online-dot" /> YOUR CITY
        </div>
        <h2>Bengaluru</h2>
        <p>
          A little local knowledge.
          <br />A lot of collective possibility.
        </p>
        <div className="city-illustration" aria-hidden="true">
          <svg viewBox="0 0 270 100">
            <g fill="currentColor" opacity=".12">
              <path d="M15 90V45h30v45h12V25h35v65h18V55h32v35h16V35h32v55h12V15h26v75h25v10H0V90z" />
            </g>
            <g fill="none" stroke="currentColor" strokeWidth="2" opacity=".5">
              <path d="M0 90h270M33 75V42m-10 5 10-12 10 12M67 70V30m145 55V20M106 90q31-45 63 0M120 90V73m35 17V73" />
            </g>
            <circle cx="248" cy="21" r="10" fill="currentColor" opacity=".1" />
          </svg>
        </div>
      </section>
      <section className="rail-section">
        <div className="rail-heading">
          <h2>Find your community</h2>
          <Users size={18} />
        </div>
        {communities.data?.items.slice(0, 3).map((c, i) => (
          <Link className="rail-community" href={`/communities/${c.id}`} key={c.id}>
            <span className={`community-avatar color-${i}`}>
              <Users size={17} />
            </span>
            <div>
              <strong>{c.title}</strong>
              <small>{c.members} members · Local conversation</small>
            </div>
            <ArrowUpRight size={16} />
          </Link>
        ))}
        <Link className="text-button" href="/communities">
          Explore all communities <ArrowRight size={14} />
        </Link>
      </section>
      <section className="rail-progress">
        <span className="eyebrow">
          <ShieldCheck size={14} /> ACCOUNTABLE PROGRESS
        </span>
        <h3>Every update has a meaning.</h3>
        <p>Platform receipt, agency acceptance, and independent verification are separate steps.</p>
        <Link className="text-button" href="/?mode=UNRESOLVED">
          See service progress <ArrowRight size={14} />
        </Link>
      </section>
      <button className="rail-report-link" onClick={onReport}>
        <MapPin size={17} />
        Report a service issue
        <ArrowUpRight size={16} />
      </button>
      <footer className="rail-footer">
        JanSetu · Synthetic local demo
        <br />
        Conversations that bring us together.
      </footer>
    </aside>
  );
}
function Page({
  pathname,
  term,
  onCompose,
  onEdit,
  onReport,
}: {
  pathname: string;
  term: string;
  onCompose: (id?: string) => void;
  onEdit: (p: Post) => void;
  onReport: () => void;
}) {
  const parts = pathname.split('/').filter(Boolean);
  if (parts[0] === 'posts' && parts[1])
    return (
      <>
        <BackLink />
        <Thread id={parts[1]} onEdit={onEdit} />
      </>
    );
  if (parts[0] === 'cases' && parts[1])
    return (
      <>
        <BackLink />
        <ReceiptDetail id={parts[1]} />
      </>
    );
  if (pathname === '/my-reports') return <MyReports onReport={onReport} />;
  if (pathname === '/studio') return <Studio />;
  if (pathname === '/account') return <AccountSecurity />;
  if (pathname === '/activity') return <ActivityPage />;
  if (parts[0] === 'profiles' && parts[1])
    return <PublicProfilePage key={parts[1]} id={parts[1]} onEdit={onEdit} />;
  if (pathname === '/communities')
    return (
      <>
        <div className="section-intro">
          <span className="eyebrow">FIND YOUR PEOPLE</span>
          <h1>Small circles. Big possibilities.</h1>
          <p>Local knowledge, shared interests, and a place to belong.</p>
        </div>
        <Communities onCompose={onCompose} />
      </>
    );
  if (parts[0] === 'communities' && parts[1])
    return <CommunityPage id={parts[1]} onCompose={onCompose} onEdit={onEdit} />;
  if (pathname === '/search')
    return <SearchPage term={term} onEdit={onEdit} onCompose={onCompose} />;
  if (['/', '/explore', '/bookmarks'].includes(pathname))
    return (
      <Feed
        saved={pathname === '/bookmarks'}
        explore={pathname === '/explore'}
        onCompose={onCompose}
        onEdit={onEdit}
        onReport={onReport}
      />
    );
  return (
    <Empty title="This page is unavailable">
      <Link className="text-button" href="/">
        Return home
      </Link>
    </Empty>
  );
}
function BackLink() {
  return (
    <Link className="back-link" href="/">
      <ArrowLeft size={16} />
      Back to your feed
    </Link>
  );
}
function CommunityPage({
  id,
  onCompose,
  onEdit,
}: {
  id: string;
  onCompose: (id?: string) => void;
  onEdit: (p: Post) => void;
}) {
  const q = useQuery({
    queryKey: ['community', id],
    queryFn: () => api<Community>(`communities/${id}`),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorState error={q.error} retry={() => q.refetch()} />;
  return (
    <>
      <BackLink />
      <CommunityCard c={q.data} detail onCompose={onCompose} />
      <Feed communityId={id} onCompose={onCompose} onEdit={onEdit} onReport={() => {}} />
    </>
  );
}
function Feed({
  saved = false,
  explore = false,
  communityId,
  onCompose,
  onEdit,
  onReport,
}: {
  saved?: boolean;
  explore?: boolean;
  communityId?: string;
  onCompose: (id?: string) => void;
  onEdit: (p: Post) => void;
  onReport: () => void;
}) {
  const { me, signIn } = useSession();
  const params = useSearchParams();
  const [localMode, setMode] = useState(params.get('mode') || 'HOME');
  const [sort, setSort] = useState('new');
  const mode = communityId || saved ? 'HOME' : localMode;
  const needsAccount = saved || mode === 'FOLLOWING';
  const path = saved ? 'me/bookmarks' : 'feed';
  const q = useInfiniteQuery({
    queryKey: ['feed', path, mode, sort, communityId],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Schema['Feed']>(
        `${path}?${new URLSearchParams({ mode, sort, ...(communityId ? { communityId } : {}), ...(pageParam ? { cursor: pageParam } : {}) })}`,
      ),
    getNextPageParam: (last) => last.nextCursor || undefined,
    enabled: !needsAccount || !!me,
  });
  useEffect(() => {
    setMode(params.get('mode') || 'HOME');
  }, [params]);
  return (
    <>
      {!communityId && (
        <>
          <div className="feed-heading">
            <div>
              <span className="eyebrow">
                {saved
                  ? 'YOUR COLLECTION'
                  : explore
                    ? 'AROUND YOUR CITY'
                    : 'CONVERSATIONS THAT MATTER'}
              </span>
              <h1>
                {saved
                  ? 'Saved for later'
                  : explore
                    ? 'Explore your city'
                    : 'Your neighbourhood, connected.'}
              </h1>
              <p>
                {saved
                  ? 'A place for conversations you want to return to.'
                  : 'Ideas, everyday moments, and a better city — together.'}
              </p>
            </div>
            <span className="heading-decoration" aria-hidden="true">
              <MessageSquare size={26} />
              <span />
            </span>
          </div>
          {!saved && (
            <div className="feed-composer">
              <Avatar name={me?.profile.displayName || 'Your city'} />
              <button onClick={() => onCompose()}>Share a thought, start a conversation…</button>
              <button
                className="compose-plus"
                aria-label="Create a conversation"
                onClick={() => onCompose()}
              >
                <Plus size={20} />
              </button>
            </div>
          )}
          {!saved && (
            <div className="feed-tabs" aria-label="Feed views">
              {[
                ['HOME', 'For you'],
                ['FOLLOWING', 'Following'],
                ['NEARBY', 'City'],
                ['UNRESOLVED', 'Unresolved'],
                ['RESOLVED', 'Resolved'],
              ].map(([v, label]) => (
                <button
                  key={v}
                  className={mode === v ? 'selected' : ''}
                  onClick={() => setMode(v)}
                  aria-pressed={mode === v}
                >
                  {label}
                </button>
              ))}
            </div>
          )}
        </>
      )}
      <div className="feed-toolbar">
        <span>
          {communityId
            ? 'COMMUNITY CONVERSATIONS'
            : mode === 'UNRESOLVED'
              ? 'SERVICE ISSUES · URGENCY, THEN AGE'
              : mode === 'RESOLVED'
                ? 'VERIFIED RESTORATION'
                : saved
                  ? 'SAVED CONVERSATIONS'
                  : 'YOUR DAILY DOSE OF LOCAL'}
        </span>
        {!['UNRESOLVED', 'RESOLVED'].includes(mode) && (
          <label className="sort-select">
            <SlidersHorizontal size={14} />
            <select aria-label="Sort feed" value={sort} onChange={(e) => setSort(e.target.value)}>
              <option value="new">Latest</option>
              <option value="top">Top posts</option>
            </select>
          </label>
        )}
      </div>
      {needsAccount && !me ? (
        <Empty title={saved ? 'Your bookmarks stay with you' : 'Follow what matters to you'}>
          <p>Sign in to continue.</p>
          <button className="primary" onClick={signIn}>
            Sign in
          </button>
        </Empty>
      ) : q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => q.refetch()} />
      ) : q.data.pages.every((p) => p.items.length === 0) ? (
        <Empty title="A fresh start">
          <p>
            {mode === 'FOLLOWING'
              ? 'Follow a community, person, or service issue to build your feed.'
              : 'There are no updates in this view yet.'}
          </p>
          <button className="primary" onClick={() => onCompose(communityId)}>
            Start a conversation
          </button>
        </Empty>
      ) : (
        <div className="feed-list">
          {q.data.pages
            .flatMap((p) => p.items)
            .map((v) =>
              v.type === 'POST' ? (
                <PostCard key={v.post.id} post={v.post} onEdit={onEdit} />
              ) : (
                <ReceiptCard key={v.receipt.id} receipt={v.receipt} />
              ),
            )}
          {q.hasNextPage && (
            <button
              className="secondary load-more"
              disabled={q.isFetchingNextPage}
              onClick={() => q.fetchNextPage()}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Show more updates'}
            </button>
          )}
        </div>
      )}
      {!saved && !communityId && (
        <div className="feed-end">
          <span className="brand-symbol small-symbol">
            <MapPin size={20} />
          </span>
          <strong>Your city gets better with you.</strong>
          <p>See a service issue in your neighbourhood?</p>
          <button className="text-button" onClick={onReport}>
            Report an issue <ArrowUpRight size={14} />
          </button>
        </div>
      )}
    </>
  );
}
function SearchPage({
  term,
  onEdit,
  onCompose,
}: {
  term: string;
  onEdit: (p: Post) => void;
  onCompose: (id?: string) => void;
}) {
  const q = useQuery({
    queryKey: ['search', term],
    queryFn: () => api<Schema['Search']>(`search?q=${encodeURIComponent(term)}`),
    enabled: term.trim().length >= 2,
  });
  return (
    <>
      <div className="section-intro">
        <span className="eyebrow">DISCOVER WHAT MATTERS</span>
        <h1>{term ? `Results for “${term}”` : 'Search your city'}</h1>
        <p>People, conversations, communities, and reviewed service progress.</p>
      </div>
      {term.trim().length < 2 ? (
        <Empty title="Start with a word or a place">Use the search box above.</Empty>
      ) : q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorState error={q.error} retry={() => q.refetch()} />
      ) : !q.data.items.length && !q.data.communities.length && !q.data.profiles.length ? (
        <Empty title="No results yet">Try another phrase or a community name.</Empty>
      ) : (
        <>
          <PeopleResults profiles={q.data.profiles} />
          {q.data.communities.map((c) => (
            <CommunityCard c={c} key={c.id} onCompose={onCompose} />
          ))}
          <div className="feed-list">
            {q.data.items.map((v) =>
              v.type === 'POST' ? (
                <PostCard post={v.post} key={v.post.id} onEdit={onEdit} />
              ) : (
                <ReceiptCard receipt={v.receipt} key={v.receipt.id} />
              ),
            )}
          </div>
        </>
      )}
    </>
  );
}
