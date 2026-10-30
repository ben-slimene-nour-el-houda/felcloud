import { useState } from 'react';
import type { Route } from '../api/contracts';
import { ROUTE_STAGES } from '../api/contracts';
import { DataTable, type Column } from '../components/DataTable';
import { DetailPanel } from '../components/Modal';
import { QueryView } from '../components/QueryView';
import {
  EmptyState,
  FilterBar,
  PageHeader,
  Pagination,
  SearchInput,
  SelectFilter,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import { useRoutes } from '../hooks/queries';
import { labelFor } from '../lib/labels';

const HEALTH_OPTIONS = [
  { value: '', label: 'All health states' },
  { value: 'healthy', label: 'Healthy' },
  { value: 'unhealthy', label: 'Unhealthy' },
  { value: 'unknown', label: 'Unknown' },
];

export function Routes() {
  const [q, setQ] = useState('');
  const [health, setHealth] = useState('');
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Route | null>(null);
  const query = useRoutes({ q, status: health, page, pageSize: 10 });

  const columns: Column<Route>[] = [
    {
      key: 'host',
      header: 'Hostname & Features',
      render: (r) => {
        const isCustom =
          !r.hostname.endsWith('.felcloud.test') &&
          !r.hostname.endsWith('.felcloud.internal') &&
          !r.hostname.endsWith('.felcloud.tn') &&
          !r.hostname.endsWith('.internal') &&
          !r.hostname.endsWith('.local');
        return (
          <div>
            <strong>{r.hostname}</strong>
            <div className="route-feature-badges">
              {r.tls && (
                <span
                  className={`route-badge route-badge-tls route-badge-tls-${r.tls.status}`}
                  title={`TLS certificate status: ${r.tls.status}`}
                >
                  TLS ({r.tls.status})
                </span>
              )}
              {isCustom && (
                <span className="route-badge route-badge-domain" title="SNI custom domain routing">
                  Custom domain
                </span>
              )}
            </div>
          </div>
        );
      },
    },

    { key: 'backend', header: 'Backend', render: (r) => <code className="text-nowrap">{r.backend}</code> },
    {
      key: 'sandbox',
      header: 'Sandbox',
      render: (r) => r.sandbox_name ?? <span className="muted">—</span>,
    },
    { key: 'health', header: 'Health', render: (r) => <StatusBadge status={r.health} /> },
    { key: 'stage', header: 'Stage reached', render: (r) => <StatusBadge status={r.stage} /> },
    {
      key: 'gen',
      header: 'Config generation',
      render: (r) => r.config_generation ?? <span className="muted">Not reported</span>,
    },
    { key: 'check', header: 'Last check', render: (r) => <Timestamp value={r.last_check_at} compact /> },
    {
      key: 'details',
      header: 'Details',
      render: (r) => (
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => setSelected(r)}
          aria-label={`Details for ${r.hostname}`}
        >
          Details
        </button>
      ),
    },
  ];

  const totalRoutes = query.data?.total ?? query.data?.items.length ?? 0;
  const tlsCount = query.data?.items.filter((r) => r.tls && r.tls.status === 'valid').length ?? 0;
  const customCount = query.data?.items.filter(
    (r) =>
      !r.hostname.endsWith('.felcloud.test') &&
      !r.hostname.endsWith('.felcloud.internal') &&
      !r.hostname.endsWith('.felcloud.tn') &&
      !r.hostname.endsWith('.internal') &&
      !r.hostname.endsWith('.local'),
  ).length ?? 0;

  return (
    <>
      <PageHeader
        title="Traffic & Routes"
        subtitle="Domain routing, SSL termination, and health monitoring for microVM backends."
      />
      <div className="bonus-feature-strip" aria-label="Routing architecture capabilities">
        <span className="bonus-strip-item">
          <span className="dot-positive" />
          <strong>L7 MicroVM Routes:</strong> {totalRoutes} active
        </span>
        <span className="bonus-strip-sep">·</span>
        <span className="bonus-strip-item">
          <span className={customCount > 0 ? 'dot-positive' : 'dot-neutral'} />
          <strong>Custom Domains:</strong> {customCount} {customCount === 1 ? 'domain' : 'domains'}
        </span>
        <span className="bonus-strip-sep">·</span>
        <span className="bonus-strip-item">
          <span className={tlsCount > 0 ? 'dot-positive' : 'dot-neutral'} />
          <strong>TLS 1.3 Valid:</strong> {tlsCount} verified
        </span>
      </div>

      <FilterBar>
        <SearchInput
          label="Search routes"
          value={q}
          onChange={(v) => {
            setQ(v);
            setPage(1);
          }}
          placeholder="Search hostname, backend or sandbox"
        />
        <SelectFilter
          label="Filter by health"
          value={health}
          onChange={(v) => {
            setHealth(v);
            setPage(1);
          }}
          options={HEALTH_OPTIONS}
        />
      </FilterBar>
      <QueryView
        query={query}
        isEmpty={(d) => d.items.length === 0}
        empty={
          <EmptyState
            title="No routes"
            message="The API returned no routes for this search or filter."
          />
        }
      >
        {(d) => (
          <>
            <DataTable caption="Routes" columns={columns} rows={d.items} rowKey={(r) => r.id} />
            <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
          </>
        )}
      </QueryView>
      {selected && (
        <DetailPanel
          title={selected.hostname}
          description="Configured target and health evidence"
          onClose={() => setSelected(null)}
        >
          <div className="stack">
            <dl className="kv">
              <dt>Target</dt>
              <dd>
                <code>{selected.backend}</code>
              </dd>
              <dt>Sandbox</dt>
              <dd>{selected.sandbox_name ?? '—'}</dd>
              <dt>Health</dt>
              <dd>
                <StatusBadge status={selected.health} />
              </dd>
              <dt>Latency</dt>
              <dd>
                {selected.latency_ms != null
                  ? `${selected.latency_ms} ms (backend probe)`
                  : 'Not measured'}
              </dd>
              <dt>TLS</dt>
              <dd>
                {selected.tls ? (
                  <>
                    <StatusBadge status={selected.tls.status} />{' '}
                    <Timestamp value={selected.tls.expires_at} />
                  </>
                ) : (
                  'Not reported by API'
                )}
              </dd>
              <dt>Evidence</dt>
              <dd>{selected.evidence ?? 'None reported'}</dd>
            </dl>
            <section>
              <h3 className="subsection-title">Rollout stage</h3>
              <ol className="stage-list">
                {ROUTE_STAGES.map((s, i) => {
                  const reached = i <= ROUTE_STAGES.indexOf(selected.stage);
                  return (
                    <li key={s} className={reached ? 'stage reached' : 'stage'}>
                      {labelFor(s).text}
                      {reached ? ' ✓' : ' — not reached'}
                    </li>
                  );
                })}
              </ol>
              <p className="muted">
                Configured, validated, deployed and reachable are separate facts; only the stages
                above are confirmed by the API.
              </p>
            </section>
          </div>
        </DetailPanel>
      )}
    </>
  );
}
