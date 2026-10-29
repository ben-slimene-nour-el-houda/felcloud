import { RefreshCcw } from 'lucide-react';
import { useRef, useState } from 'react';
import { useApi } from '../app-context';
import { isApiError } from '../api/errors';
import type { Lease } from '../api/contracts';
import { DataTable, type Column } from '../components/DataTable';
import { ConfirmDialog, DetailPanel } from '../components/Modal';
import { LeaseLifecycle } from '../components/LeaseLifecycle';
import { QueryView } from '../components/QueryView';
import {
  ActionButton,
  Card,
  CapabilityNote,
  CopyButton,
  EmptyState,
  FilterBar,
  MetricCard,
  PageHeader,
  Pagination,
  SearchInput,
  SelectFilter,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import { useCapability, useLeases, usePoolStats } from '../hooks/queries';
import { newKey } from '../lib/format';
import { useOperations } from '../operations/OperationsProvider';

const STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'allocated', label: 'Allocated' },
  { value: 'quarantined', label: 'Quarantined' },
  { value: 'free', label: 'Free' },
];

export function Ipam() {
  const api = useApi();
  const { run } = useOperations();
  const reconcile = useCapability('lease_reconcile');
  const [q, setQ] = useState('');
  const [state, setState] = useState('');
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Lease | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const key = useRef(newKey());
  const stats = usePoolStats();
  const leases = useLeases({ q, status: state, page, pageSize: 12 });

  const columns: Column<Lease>[] = [
    { key: 'ip', header: 'Address', render: (l) => <code>{l.ip}</code> },
    { key: 'state', header: 'State', render: (l) => <StatusBadge status={l.state} /> },
    {
      key: 'owner',
      header: 'Owner',
      render: (l) => l.sandbox_name ?? <span className="muted">—</span>,
    },
    { key: 'alloc', header: 'Allocated', render: (l) => <Timestamp value={l.allocated_at} /> },
    {
      key: 'until',
      header: 'Expiry / cooldown ends',
      render: (l) => <Timestamp value={l.until} />,
    },
    {
      key: 'actions',
      header: 'Details',
      render: (l) => (
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => setSelected(l)}
          aria-label={`Details for ${l.ip}`}
        >
          View
        </button>
      ),
    },
  ];

  async function confirmReconcile() {
    if (pending) return;
    setPending(true);
    try {
      await run('Reconcile IP leases', () => api.ipam.reconcile(key.current));
      setConfirming(false);
      key.current = newKey();
    } catch (err) {
      setError(isApiError(err) ? err.message : 'The request failed unexpectedly.');
    }
    setPending(false);
  }

  return (
    <>
      <PageHeader
        title="IP Address Management"
        subtitle="The private address pool and how leases move through their lifecycle."
        action={
          <ActionButton
            variant="secondary"
            icon={<RefreshCcw size={16} aria-hidden />}
            disabledReason={reconcile.reason}
            onClick={() => {
              setError(null);
              setConfirming(true);
            }}
          >
            Reconcile leases
          </ActionButton>
        }
      />
      <CapabilityNote reason={reconcile.reason} />
      <QueryView query={stats} rows={2}>
        {(s) => (
          <div className="metric-grid">
            <MetricCard label="Total addresses" value={s.total} hint={s.cidr} />
            <MetricCard label="Allocated" value={s.allocated} />
            <MetricCard
              label="Quarantined"
              value={s.quarantined}
              hint={
                s.quarantine_seconds
                  ? `Cooldown ${Math.round(s.quarantine_seconds / 60)} min`
                  : undefined
              }
            />
            <MetricCard
              label="Free"
              value={s.free}
              tone={s.free === 0 ? 'critical' : undefined}
              hint={s.free === 0 ? 'No addresses available' : undefined}
            />
          </div>
        )}
      </QueryView>
      <Card
        title="Lease lifecycle"
        description="An address in quarantine cannot be reallocated until its cooldown ends."
      >
        <LeaseLifecycle />
      </Card>
      <FilterBar>
        <SearchInput
          label="Search leases"
          value={q}
          onChange={(v) => {
            setQ(v);
            setPage(1);
          }}
          placeholder="Search IP, lease ID or sandbox"
        />
        <SelectFilter
          label="Filter by lease state"
          value={state}
          onChange={(v) => {
            setState(v);
            setPage(1);
          }}
          options={STATE_OPTIONS}
        />
      </FilterBar>
      <QueryView
        query={leases}
        isEmpty={(d) => d.items.length === 0}
        empty={
          <EmptyState
            title="No leases match"
            message="The API returned no leases for this search or filter."
          />
        }
      >
        {(d) => (
          <>
            <DataTable caption="IP leases" columns={columns} rows={d.items} rowKey={(l) => l.id} />
            <Pagination page={d.page} pageSize={d.page_size} total={d.total} onPage={setPage} />
          </>
        )}
      </QueryView>
      {selected && (
        <DetailPanel
          title={selected.ip}
          description={`Lease ${selected.id}`}
          onClose={() => setSelected(null)}
        >
          <dl className="kv">
            <dt>Lease ID</dt>
            <dd>
              <code>{selected.id}</code> <CopyButton value={selected.id} label="lease ID" />
            </dd>
            <dt>State</dt>
            <dd>
              <StatusBadge status={selected.state} />
            </dd>
            <dt>Sandbox</dt>
            <dd>{selected.sandbox_name ?? '—'}</dd>
            <dt>Allocated</dt>
            <dd>
              <Timestamp value={selected.allocated_at} />
            </dd>
            <dt>{selected.state === 'quarantined' ? 'Cooldown ends' : 'Expires'}</dt>
            <dd>
              <Timestamp value={selected.until} />
            </dd>
          </dl>
          {selected.state === 'quarantined' && (
            <p className="muted">
              This address is quarantined and cannot be reallocated until the cooldown ends.
            </p>
          )}
        </DetailPanel>
      )}
      {confirming && (
        <ConfirmDialog
          title="Reconcile IP leases?"
          confirmLabel="Start reconciliation"
          pending={pending}
          error={error}
          onConfirm={() => void confirmReconcile()}
          onCancel={() => setConfirming(false)}
          consequences={
            <p>
              The control plane compares lease records with the compute and network state and
              repairs any drift it finds. The result is shown once the backend job finishes.
            </p>
          }
        />
      )}
    </>
  );
}
