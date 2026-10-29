import { useIsFetching, useQueryClient } from '@tanstack/react-query';
import { Cloud, Menu, RefreshCw, UserRound } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { useApp } from '../app-context';
import { tokenStore } from '../api/auth';
import { isApiError } from '../api/errors';
import { useHealth } from '../hooks/queries';
import { NAV_ITEMS } from '../nav';
import { formatTime } from '../lib/format';
import { useOperations } from '../operations/OperationsProvider';
import { Modal } from './Modal';
import { OperationStatus, ToneBadge } from './ui';

export function DemoModeBanner() {
  const { api } = useApp();
  if (api.mode !== 'demo') return null;
  return (
    <div className="demo-banner" role="status" data-testid="demo-banner">
      DEMO DATA — NOT CONNECTED TO INFRASTRUCTURE
    </div>
  );
}

export function ConnectionIndicator() {
  const { api } = useApp();
  const { data, error, isPending } = useHealth();
  if (isPending) return <ToneBadge tone="neutral">Checking API…</ToneBadge>;
  if (!data) {
    const auth = isApiError(error) && (error.kind === 'auth' || error.kind === 'forbidden');
    return (
      <ToneBadge tone="critical">{auth ? 'API: sign-in required' : 'API: unreachable'}</ToneBadge>
    );
  }
  const label =
    api.mode === 'demo'
      ? 'Simulator'
      : data.status === 'ok'
        ? 'API: connected'
        : data.status === 'degraded'
          ? 'API: degraded'
          : 'API: down';
  return (
    <ToneBadge
      tone={
        api.mode === 'demo'
          ? 'warning'
          : data.status === 'ok'
            ? 'positive'
            : data.status === 'degraded'
              ? 'warning'
              : 'critical'
      }
    >
      {label}
    </ToneBadge>
  );
}

function ConnectionWarning({ onSignIn }: { onSignIn: () => void }) {
  const { config } = useApp();
  const { data, error, isError, dataUpdatedAt, refetch } = useHealth();
  if (!isError) return null;
  const auth = isApiError(error) && (error.kind === 'auth' || error.kind === 'forbidden');
  return (
    <div className="connection-warning" role="alert" data-testid="connection-warning">
      <div>
        <strong>{auth ? 'Authentication required.' : 'The FelCloud API is unavailable.'}</strong>{' '}
        {error instanceof Error ? error.message : ''}{' '}
        {!auth && (
          <>
            Address: <code>{config.apiBaseUrl}</code>.{' '}
          </>
        )}
        {data
          ? `Pages show last-known data from ${formatTime(dataUpdatedAt)} and may be out of date.`
          : 'No data has been received, so nothing is shown as live state.'}
      </div>
      <div className="row">
        {auth && (
          <button type="button" className="btn btn-secondary" onClick={onSignIn}>
            Set API token
          </button>
        )}
        <button type="button" className="btn btn-secondary" onClick={() => void refetch()}>
          Retry connection
        </button>
      </div>
    </div>
  );
}

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Primary">
      <ul className="nav-list">
        {NAV_ITEMS.map(({ to, label, icon: Icon, end }) => (
          <li key={to}>
            <NavLink
              to={to}
              end={end}
              className={({ isActive }) => `nav-link ${isActive ? 'is-active' : ''}`}
              onClick={onNavigate}
              title={label}
            >
              <Icon size={20} aria-hidden />
              <span className="nav-label">{label}</span>
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function Brand() {
  const { config } = useApp();
  return (
    <div className="brand">
      <Link to="/" className="brand-link" aria-label="FelCloud home">
        <Cloud size={24} aria-hidden />
        <span className="brand-text">
          <span className="brand-name">FelCloud</span>
          <span className="muted brand-sub">Cloud infrastructure</span>
        </span>
      </Link>
      <span className="env-badge" title="Display label set by VITE_ENVIRONMENT_LABEL">
        {config.environmentLabel}
      </span>
    </div>
  );
}

export function Sidebar() {
  return (
    <aside className="sidebar">
      <Brand />
      <NavList />
    </aside>
  );
}

export function MobileNavigation({ onClose }: { onClose: () => void }) {
  return (
    <Modal variant="nav" title="Navigation" onClose={onClose} closeLabel="Close navigation">
      <Brand />
      <NavList onNavigate={onClose} />
    </Modal>
  );
}

function AccountDialog({ onClose }: { onClose: () => void }) {
  const [token, setToken] = useState('');
  const [hasToken, setHasToken] = useState(Boolean(tokenStore.get()));
  const qc = useQueryClient();
  return (
    <Modal
      title="API access"
      description="An optional bearer token is sent with API requests. It is kept for this browser tab only."
      onClose={onClose}
    >
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault();
          if (!token.trim()) return;
          tokenStore.set(token);
          setToken('');
          setHasToken(true);
          void qc.invalidateQueries();
        }}
      >
        <div className="field">
          <label htmlFor="api-token">Bearer token</label>
          <input
            id="api-token"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <p className="muted">{hasToken ? 'A token is set for this tab.' : 'No token is set.'}</p>
        </div>
        <div className="row">
          <button type="submit" className="btn btn-primary" disabled={!token.trim()}>
            Save token
          </button>
          {hasToken && (
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => {
                tokenStore.clear();
                setHasToken(false);
                void qc.invalidateQueries();
              }}
            >
              Sign out
            </button>
          )}
        </div>
      </form>
    </Modal>
  );
}

function ActiveOperations() {
  const { ops, dismiss } = useOperations();
  if (ops.length === 0) return null;
  return (
    <section className="ops-panel" aria-label="Operations">
      <h2 className="section-title">Operations</h2>
      <div className="stack">
        {ops.slice(0, 4).map((op) => (
          <OperationStatus key={op.id} op={op} onDismiss={() => dismiss(op.id)} />
        ))}
      </div>
    </section>
  );
}

export function TopBar({ onMenu, onAccount }: { onMenu: () => void; onAccount: () => void }) {
  const qc = useQueryClient();
  const fetching = useIsFetching();
  const { dataUpdatedAt } = useHealth();
  const { pathname } = useLocation();
  const current = NAV_ITEMS.find((n) => (n.end ? pathname === n.to : pathname.startsWith(n.to)));
  return (
    <header className="topbar">
      <button
        type="button"
        className="icon-btn mobile-only"
        aria-label="Open navigation"
        onClick={onMenu}
      >
        <Menu size={22} aria-hidden />
      </button>
      <nav aria-label="Breadcrumb" className="breadcrumb">
        <span className="muted">FelCloud</span>
        <span aria-hidden className="muted">
          /
        </span>
        <span aria-current="page">{current?.label ?? 'Not found'}</span>
      </nav>
      <div className="topbar-right">
        <ConnectionIndicator />
        <span className="muted updated hide-mobile">Last updated {formatTime(dataUpdatedAt)}</span>
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => void qc.invalidateQueries()}
          aria-label="Refresh data"
        >
          <RefreshCw size={16} aria-hidden className={fetching ? 'spin' : ''} />
          <span className="hide-mobile">Refresh</span>
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label="Account and API access"
          onClick={onAccount}
        >
          <UserRound size={20} aria-hidden />
        </button>
      </div>
    </header>
  );
}

export function AppShell(): ReactNode {
  const [navOpen, setNavOpen] = useState(false);
  const [account, setAccount] = useState(false);
  return (
    <div className="shell">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <DemoModeBanner />
      <div className="shell-body">
        <Sidebar />
        <div className="shell-main">
          <TopBar onMenu={() => setNavOpen(true)} onAccount={() => setAccount(true)} />
          <ConnectionWarning onSignIn={() => setAccount(true)} />
          <main id="main" className="content" tabIndex={-1}>
            <ActiveOperations />
            <Outlet />
          </main>
        </div>
      </div>
      {navOpen && <MobileNavigation onClose={() => setNavOpen(false)} />}
      {account && <AccountDialog onClose={() => setAccount(false)} />}
    </div>
  );
}
