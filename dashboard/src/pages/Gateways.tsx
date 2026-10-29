import { useRef, useState } from 'react';
import { useApi } from '../app-context';
import { isApiError } from '../api/errors';
import type { DrillPlan, Gateway } from '../api/contracts';
import { ConfirmDialog } from '../components/Modal';
import { GatewayTopology } from '../components/GatewayTopology';
import { QueryView } from '../components/QueryView';
import {
  ActionButton,
  Card,
  CapabilityNote,
  EmptyState,
  PageHeader,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import { useCapability, useGateways, useOperationHistory } from '../hooks/queries';
import { newKey } from '../lib/format';
import { useOperations } from '../operations/OperationsProvider';

export function Gateways() {
  const api = useApi();
  const { run } = useOperations();
  const failover = useCapability('gateway_failover');
  const drill = useCapability('failover_drill');
  const gateways = useGateways();
  const history = useOperationHistory({ q: 'failover', pageSize: 5 });
  const [target, setTarget] = useState<Gateway | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [plan, setPlan] = useState<DrillPlan | null>(null);
  const key = useRef(newKey());

  async function confirm(t: Gateway) {
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      await run(
        `Fail over virtual IP to ${t.name}`,
        () => api.gateways.failover({ target_gateway_id: t.id }, key.current),
        {
          // Success is only claimed if the API now reports the target as VIP owner.
          verify: async () => {
            const view = await api.gateways.get();
            return view.vip.owner_gateway_id === t.id
              ? { ok: true, detail: `The API reports ${t.name} as the virtual IP owner.` }
              : { ok: false, detail: `The API does not report ${t.name} as the virtual IP owner.` };
          },
        },
      );
      key.current = newKey();
      setTarget(null);
    } catch (err) {
      setError(isApiError(err) ? err.message : 'The request failed unexpectedly.');
    }
    setPending(false);
  }

  return (
    <>
      <PageHeader
        title="Gateways"
        subtitle="Redundant HAProxy and Keepalived gateways sharing one virtual IP."
      />
      <QueryView
        query={gateways}
        rows={4}
        isEmpty={(v) => v.gateways.length === 0}
        empty={<EmptyState title="No gateways reported" message="The API returned no gateways." />}
      >
        {(view) => (
          <>
            <Card
              title="Topology"
              description="Roles and ownership are exactly what the API reports; nothing is assumed."
            >
              <GatewayTopology view={view} detailed />
            </Card>
            <Card
              title="Manual failover"
              description="Move the virtual IP to another gateway. Active connections may be interrupted."
            >
              <CapabilityNote reason={failover.reason} />
              <div className="row wrap">
                {view.gateways.map((g) => (
                  <ActionButton
                    key={g.id}
                    variant="secondary"
                    disabledReason={
                      !failover.enabled
                        ? failover.reason
                        : g.owns_vip
                          ? `${g.name} already owns the virtual IP.`
                          : g.keepalived.status !== 'up'
                            ? `${g.name} Keepalived is not reported up.`
                            : undefined
                    }
                    onClick={() => {
                      setError(null);
                      setTarget(g);
                    }}
                  >
                    Fail over to {g.name}
                  </ActionButton>
                ))}
              </div>
            </Card>
          </>
        )}
      </QueryView>

      <Card title="Failure drill" description="A drill is a dry run. It never stops a service.">
        <CapabilityNote reason={drill.reason} />
        <ActionButton
          variant="secondary"
          disabledReason={drill.reason}
          onClick={() =>
            void api.gateways
              .drill()
              .then(setPlan)
              .catch(() => setPlan(null))
          }
        >
          Show dry-run plan
        </ActionButton>
        {plan && (
          <div className="stack-sm">
            <StatusBadge status="pending" label="Dry run — no service is stopped" />
            <ul>
              {plan.steps.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
          </div>
        )}
      </Card>

      <Card title="Failover history" description="From the backend operation history.">
        <QueryView
          query={history}
          rows={2}
          isEmpty={(d) => d.items.length === 0}
          empty={
            <EmptyState
              title="No failovers recorded"
              message="The API reports no failover operations."
            />
          }
        >
          {(d) => (
            <ul className="plain-list">
              {d.items.map((o) => (
                <li key={o.id} className="row spread">
                  <span>
                    <code>{o.id}</code> {o.message && <span className="muted">— {o.message}</span>}
                  </span>
                  <span>
                    <StatusBadge status={o.status} />{' '}
                    <span className="muted">
                      <Timestamp value={o.started_at} />
                    </span>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </QueryView>
      </Card>

      {target && (
        <ConfirmDialog
          title={`Fail over to ${target.name}?`}
          confirmLabel="Request failover"
          pending={pending}
          error={error}
          onConfirm={() => void confirm(target)}
          onCancel={() => setTarget(null)}
          consequences={
            <p>
              This asks the control plane to move the virtual IP to {target.name}. Connections held
              by the current owner may be interrupted. Success is only reported after the API
              confirms the new owner; downtime is not claimed or measured here.
            </p>
          }
        />
      )}
    </>
  );
}
