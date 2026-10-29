import { Plus } from 'lucide-react';
import { Link, useNavigate } from 'react-router-dom';
import { useApp } from '../app-context';
import { GatewayTopology } from '../components/GatewayTopology';
import { QueryView } from '../components/QueryView';
import {
  ActionButton,
  Card,
  CapabilityNote,
  EmptyState,
  MetricCard,
  PageHeader,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import {
  useCapability,
  useConfigurations,
  useEvents,
  useGateways,
  useHealth,
  useOperationHistory,
  usePoolStats,
  useRoutes,
  useSandboxes,
} from '../hooks/queries';
import { percent } from '../lib/format';

export function Overview() {
  const navigate = useNavigate();
  const { config } = useApp();
  const health = useHealth();
  const provision = useCapability('sandbox_provision');
  const active = useSandboxes({ status: 'active', pageSize: 1 });
  const failedSandboxes = useSandboxes({ status: 'failed', pageSize: 5 });
  const pool = usePoolStats();
  const gateways = useGateways();
  const failedOps = useOperationHistory({ status: 'failed', pageSize: 1 });
  const configs = useConfigurations();
  const unhealthyRoutes = useRoutes({ status: 'unhealthy', pageSize: 5 });
  const events = useEvents({ pageSize: 6 });

  if (health.isError && !health.data) {
    return (
      <>
        <PageHeader title="Overview" subtitle="Platform health and what needs attention." />
        <EmptyState
          title="No infrastructure data received yet"
          message={`The console has not been able to reach the API at ${config.apiBaseUrl}. Nothing is shown as live state until a connection is made.`}
          action={
            <button type="button" className="btn btn-primary" onClick={() => void health.refetch()}>
              Retry connection
            </button>
          }
        />
      </>
    );
  }

  const gw = gateways.data?.gateways;
  const healthyGw = gw?.filter(
    (g) => g.haproxy.status === 'up' && g.keepalived.status === 'up',
  ).length;
  const attention: Array<{ key: string; text: string; to: string }> = [];
  for (const s of failedSandboxes.data?.items ?? [])
    attention.push({ key: s.id, text: `Sandbox ${s.name} failed to provision.`, to: '/sandboxes' });
  for (const g of gw ?? [])
    if (g.haproxy.status !== 'up' || g.keepalived.status !== 'up')
      attention.push({
        key: g.id,
        text: `${g.name} reports a service that is not up.`,
        to: '/gateways',
      });
  for (const c of configs.data ?? [])
    if (c.deployment.status === 'partial')
      attention.push({
        key: c.id,
        text: `Configuration generation ${c.generation} is only partially deployed.`,
        to: '/configuration',
      });
  for (const r of unhealthyRoutes.data?.items ?? [])
    attention.push({ key: r.id, text: `Route ${r.hostname} is unhealthy.`, to: '/routes' });
  if (pool.data && pool.data.free === 0)
    attention.push({ key: 'pool', text: 'The IP pool has no free addresses.', to: '/ipam' });

  return (
    <>
      <PageHeader
        title="Overview"
        subtitle="Is the platform healthy, and what needs attention?"
        action={
          <ActionButton
            variant="primary"
            icon={<Plus size={18} aria-hidden />}
            disabledReason={provision.reason}
            onClick={() => navigate('/sandboxes', { state: { provision: true } })}
          >
            Provision sandbox
          </ActionButton>
        }
      />
      <CapabilityNote reason={provision.reason} />

      <Card title="Platform health">
        <QueryView query={health} rows={2}>
          {(h) => (
            <div className="stack-sm">
              <p>
                <StatusBadge status={h.status} />{' '}
                <span className="muted">
                  Checked <Timestamp value={h.checked_at} />
                  {h.version ? ` · API ${h.version}` : ''}
                </span>
              </p>
              <ul className="plain-list">
                {h.components.map((c) => (
                  <li key={c.name} className="row spread">
                    <span>
                      {c.name}
                      {c.detail ? <span className="muted"> — {c.detail}</span> : null}
                    </span>
                    <StatusBadge status={c.status} />
                  </li>
                ))}
              </ul>
            </div>
          )}
        </QueryView>
      </Card>

      <div className="metric-grid">
        <MetricCard
          label="Active sandboxes"
          value={active.data ? active.data.total : '—'}
          hint={active.isError ? 'Unavailable' : undefined}
        />
        <MetricCard
          label="IP pool in use"
          value={
            pool.data
              ? `${percent(pool.data.allocated + pool.data.quarantined, pool.data.total)}%`
              : '—'
          }
          hint={
            pool.data
              ? `${pool.data.allocated} allocated · ${pool.data.quarantined} quarantined · ${pool.data.free} free`
              : pool.isError
                ? 'Unavailable'
                : undefined
          }
        />
        <MetricCard
          label="Healthy gateways"
          value={healthyGw !== undefined && gw ? `${healthyGw} of ${gw.length}` : '—'}
          hint={gateways.isError ? 'Unavailable' : 'HAProxy and Keepalived both up'}
        />
        <MetricCard
          label="Failed operations"
          value={failedOps.data ? failedOps.data.total : '—'}
          hint={failedOps.isError ? 'Unavailable' : 'In operation history'}
          tone={failedOps.data && failedOps.data.total > 0 ? 'critical' : undefined}
        />
      </div>

      {attention.length > 0 && (
        <Card title="Needs attention">
          <ul className="plain-list">
            {attention.map((a) => (
              <li key={a.key} className="row spread">
                <span>{a.text}</span>
                <Link to={a.to}>Review</Link>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <Card
        title="Gateway topology"
        description="Roles and virtual IP ownership as reported by the API."
        action={<Link to="/gateways">Open gateways</Link>}
      >
        <QueryView query={gateways} rows={3}>
          {(view) => <GatewayTopology view={view} />}
        </QueryView>
      </Card>

      <Card title="Recent activity" action={<Link to="/observability">All events</Link>}>
        <QueryView
          query={events}
          rows={4}
          isEmpty={(d) => d.items.length === 0}
          empty={<EmptyState title="No events yet" message="The API returned no events." />}
        >
          {(d) => (
            <ul className="plain-list">
              {d.items.map((e) => (
                <li key={e.id} className="activity">
                  <StatusBadge status={e.severity} />
                  <span>{e.message}</span>
                  <span className="muted">
                    <Timestamp value={e.at} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </QueryView>
      </Card>
    </>
  );
}
