import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { PlatformEvent } from '../api/contracts';
import { DataTable, type Column } from '../components/DataTable';
import { QueryView } from '../components/QueryView';
import {
  CapabilityNote,
  CopyButton,
  EmptyState,
  FilterBar,
  PageHeader,
  Pagination,
  SearchInput,
  SelectFilter,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import {
  useCapability,
  useEvents,
  useGateways,
  useHaproxyLogs,
  useOperationHistory,
  useSystemLogs,
} from '../hooks/queries';

type Tab = 'events' | 'haproxy' | 'system' | 'operations';
const TABS: Array<{ id: Tab; label: string }> = [
  { id: 'events', label: 'Events' },
  { id: 'haproxy', label: 'HAProxy access logs' },
  { id: 'system', label: 'Keepalived & system logs' },
  { id: 'operations', label: 'Operation history' },
];

const RESOURCE_LINK: Record<PlatformEvent['resource']['type'], string | null> = {
  sandbox: '/sandboxes',
  gateway: '/gateways',
  configuration: '/configuration',
  lease: '/ipam',
  route: '/routes',
  system: null,
};

export function Observability() {
  const [tab, setTab] = useState<Tab>('events');
  return (
    <>
      <PageHeader title="Observability" subtitle="Understand what happened, when, and why." />
      <div role="tablist" aria-label="Observability views" className="tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            type="button"
            id={`tab-${t.id}`}
            aria-selected={tab === t.id}
            aria-controls={`panel-${t.id}`}
            tabIndex={tab === t.id ? 0 : -1}
            className={`tab ${tab === t.id ? 'is-active' : ''}`}
            onClick={() => setTab(t.id)}
            onKeyDown={(e) => {
              const i = TABS.findIndex((x) => x.id === tab);
              if (e.key === 'ArrowRight') setTab(TABS[(i + 1) % TABS.length]!.id);
              if (e.key === 'ArrowLeft') setTab(TABS[(i + TABS.length - 1) % TABS.length]!.id);
            }}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === 'events' && <EventsTab />}
        {tab === 'haproxy' && <LogsTab kind="haproxy" />}
        {tab === 'system' && <LogsTab kind="system" />}
        {tab === 'operations' && <OperationsTab />}
      </div>
    </>
  );
}

function EventsTab() {
  const [q, setQ] = useState('');
  const [severity, setSeverity] = useState('');
  const [page, setPage] = useState(1);
  const query = useEvents({ q, severity, page, pageSize: 10 });
  const columns: Column<PlatformEvent>[] = [
    { key: 'at', header: 'Time', render: (e) => <Timestamp value={e.at} /> },
    { key: 'sev', header: 'Severity', render: (e) => <StatusBadge status={e.severity} /> },
    {
      key: 'res',
      header: 'Resource',
      render: (e) => {
        const to = RESOURCE_LINK[e.resource.type];
        const text = e.resource.name ?? e.resource.id;
        return to ? <Link to={to}>{text}</Link> : <span>{text}</span>;
      },
    },
    { key: 'msg', header: 'Message', render: (e) => e.message },
    {
      key: 'id',
      header: 'Operation / trace',
      render: (e) => {
        const id = e.operation_id ?? e.trace_id;
        return id ? (
          <span>
            <code>{id}</code> <CopyButton value={id} label="ID" />
          </span>
        ) : (
          <span className="muted">—</span>
        );
      },
    },
  ];
  return (
    <>
      <FilterBar>
        <SearchInput
          label="Search events"
          value={q}
          onChange={(v) => {
            setQ(v);
            setPage(1);
          }}
          placeholder="Search message, resource or ID"
        />
        <SelectFilter
          label="Filter by severity"
          value={severity}
          onChange={(v) => {
            setSeverity(v);
            setPage(1);
          }}
          options={[
            { value: '', label: 'All severities' },
            { value: 'info', label: 'Info' },
            { value: 'warning', label: 'Warning' },
            { value: 'error', label: 'Error' },
            { value: 'critical', label: 'Critical' },
          ]}
        />
      </FilterBar>
      <QueryView
        query={query}
        isEmpty={(d) => d.items.length === 0}
        empty={
          <EmptyState
            title="No events"
            message="The API returned no events for this search or filter."
          />
        }
      >
        {(d) => (
          <>
            <DataTable caption="Events" columns={columns} rows={d.items} rowKey={(e) => e.id} />
            <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
          </>
        )}
      </QueryView>
    </>
  );
}

function LogsTab({ kind }: { kind: 'haproxy' | 'system' }) {
  const cap = useCapability(kind === 'haproxy' ? 'logs_haproxy' : 'logs_system');
  const gateways = useGateways();
  const [gatewayId, setGatewayId] = useState('');
  const [page, setPage] = useState(1);
  const params = { gatewayId: gatewayId || undefined, page, pageSize: 15 };
  const haproxy = useHaproxyLogs(params, cap.enabled && kind === 'haproxy');
  const system = useSystemLogs(params, cap.enabled && kind === 'system');
  if (!cap.enabled) return <CapabilityNote reason={cap.reason} />;
  const gatewayOptions = [
    { value: '', label: 'All gateways' },
    ...(gateways.data?.gateways ?? []).map((g) => ({ value: g.id, label: g.name })),
  ];
  return (
    <>
      <FilterBar>
        <SelectFilter
          label="Filter by gateway"
          value={gatewayId}
          onChange={(v) => {
            setGatewayId(v);
            setPage(1);
          }}
          options={gatewayOptions}
        />
      </FilterBar>
      {kind === 'haproxy' ? (
        <QueryView
          query={haproxy}
          isEmpty={(d) => d.items.length === 0}
          empty={
            <EmptyState
              title="No log lines"
              message="Log retrieval worked, but there are no access-log entries for this scope."
            />
          }
        >
          {(d) => (
            <>
              <DataTable
                caption="HAProxy access logs"
                rows={d.items}
                rowKey={(l) => `${l.at}-${l.client_ip}-${l.path}-${l.gateway_id}`}
                columns={[
                  { key: 'at', header: 'Time', render: (l) => <Timestamp value={l.at} /> },
                  { key: 'gw', header: 'Gateway', render: (l) => l.gateway_id },
                  {
                    key: 'req',
                    header: 'Request',
                    render: (l) => (
                      <code>
                        {l.method} {l.host}
                        {l.path}
                      </code>
                    ),
                  },
                  {
                    key: 'st',
                    header: 'Status',
                    render: (l) => (
                      <StatusBadge
                        status={l.status >= 500 ? 'error' : l.status >= 400 ? 'warning' : 'ok'}
                        label={String(l.status)}
                      />
                    ),
                  },
                  { key: 'be', header: 'Backend', render: (l) => <code>{l.backend}</code> },
                  { key: 'ms', header: 'Duration', render: (l) => `${l.duration_ms} ms` },
                ]}
              />
              <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
            </>
          )}
        </QueryView>
      ) : (
        <QueryView
          query={system}
          isEmpty={(d) => d.items.length === 0}
          empty={
            <EmptyState
              title="No log lines"
              message="Log retrieval worked, but there are no system-log entries for this scope."
            />
          }
        >
          {(d) => (
            <>
              <DataTable
                caption="System logs"
                rows={d.items}
                rowKey={(l) => `${l.at}-${l.unit}-${l.gateway_id}-${l.message}`}
                columns={[
                  { key: 'at', header: 'Time', render: (l) => <Timestamp value={l.at} /> },
                  { key: 'gw', header: 'Gateway', render: (l) => l.gateway_id },
                  { key: 'unit', header: 'Unit', render: (l) => l.unit },
                  { key: 'lvl', header: 'Level', render: (l) => <StatusBadge status={l.level} /> },
                  { key: 'msg', header: 'Message', render: (l) => l.message },
                ]}
              />
              <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
            </>
          )}
        </QueryView>
      )}
    </>
  );
}

function OperationsTab() {
  const [q, setQ] = useState('');
  const [page, setPage] = useState(1);
  const query = useOperationHistory({ q, page, pageSize: 10 });
  return (
    <>
      <FilterBar>
        <SearchInput
          label="Search operations"
          value={q}
          onChange={(v) => {
            setQ(v);
            setPage(1);
          }}
          placeholder="Search ID, kind or resource"
        />
      </FilterBar>
      <QueryView
        query={query}
        isEmpty={(d) => d.items.length === 0}
        empty={
          <EmptyState title="No operations" message="The API returned no operation history." />
        }
      >
        {(d) => (
          <>
            <DataTable
              caption="Operation history"
              rows={d.items}
              rowKey={(o) => o.id}
              columns={[
                {
                  key: 'id',
                  header: 'Operation',
                  render: (o) => (
                    <span>
                      <code>{o.id}</code> <CopyButton value={o.id} label="operation ID" />
                    </span>
                  ),
                },
                { key: 'kind', header: 'Kind', render: (o) => o.kind },
                { key: 'st', header: 'Status', render: (o) => <StatusBadge status={o.status} /> },
                {
                  key: 'res',
                  header: 'Resource',
                  render: (o) => o.resource?.id ?? <span className="muted">—</span>,
                },
                {
                  key: 'start',
                  header: 'Started',
                  render: (o) => <Timestamp value={o.started_at} />,
                },
                {
                  key: 'end',
                  header: 'Finished',
                  render: (o) => <Timestamp value={o.finished_at} />,
                },
              ]}
            />
            <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
          </>
        )}
      </QueryView>
    </>
  );
}
