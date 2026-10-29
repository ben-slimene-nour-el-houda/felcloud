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
    { key: 'host', header: 'Hostname', render: (r) => <strong>{r.hostname}</strong> },
    { key: 'backend', header: 'Backend', render: (r) => <code>{r.backend}</code> },
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
    { key: 'check', header: 'Last check', render: (r) => <Timestamp value={r.last_check_at} /> },
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
          View
        </button>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Traffic & Routes"
        subtitle="Domain routing and the health of each backend."
      />
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
