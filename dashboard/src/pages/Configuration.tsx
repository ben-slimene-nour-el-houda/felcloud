import { useRef, useState } from 'react';
import { useApi } from '../app-context';
import { isApiError } from '../api/errors';
import type { ConfigGeneration, Operation } from '../api/contracts';
import { DataTable, type Column } from '../components/DataTable';
import { ConfirmDialog, DetailPanel } from '../components/Modal';
import { QueryView } from '../components/QueryView';
import {
  ActionButton,
  Card,
  CapabilityNote,
  CopyButton,
  EmptyState,
  PageHeader,
  StatusBadge,
  Timestamp,
} from '../components/ui';
import { useCapability, useConfigurations } from '../hooks/queries';
import { newKey } from '../lib/format';
import { useOperations, type VerifyResult } from '../operations/OperationsProvider';

type Verb = 'validate' | 'deploy' | 'rollback';

export function Configuration() {
  const query = useConfigurations();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const columns: Column<ConfigGeneration>[] = [
    {
      key: 'gen',
      header: 'Generation',
      render: (c) => (
        <div>
          <strong>#{c.generation}</strong>{' '}
          {c.last_known_good && <StatusBadge status="passed" label="Last known good" />}
        </div>
      ),
    },
    { key: 'created', header: 'Created', render: (c) => <Timestamp value={c.created_at} /> },
    { key: 'checksum', header: 'Checksum', render: (c) => <code>{c.checksum.slice(0, 19)}</code> },
    {
      key: 'validation',
      header: 'Validation',
      render: (c) => <StatusBadge status={c.validation.status} />,
    },
    {
      key: 'deployment',
      header: 'Deployment',
      render: (c) => <StatusBadge status={c.deployment.status} />,
    },
    { key: 'gateways', header: 'Per gateway', render: (c) => <GatewayStates config={c} /> },
    {
      key: 'details',
      header: 'Details',
      render: (c) => (
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => setSelectedId(c.id)}
          aria-label={`Details for generation ${c.generation}`}
        >
          View
        </button>
      ),
    },
  ];
  return (
    <>
      <PageHeader
        title="Configuration"
        subtitle="Configuration generations: validate, deploy and roll back safely."
      />
      <QueryView
        query={query}
        isEmpty={(d) => d.length === 0}
        empty={
          <EmptyState
            title="No configuration generations"
            message="The API returned no generations."
          />
        }
      >
        {(list) => {
          const partial = list.filter((c) => c.deployment.status === 'partial');
          const selected = list.find((c) => c.id === selectedId) ?? null;
          return (
            <>
              {partial.map((c) => (
                <div key={c.id} className="callout callout-warning" role="alert">
                  <strong>Generation {c.generation} is only partially deployed.</strong>{' '}
                  {c.gateways
                    ?.map((g) => `${g.gateway_id}: ${g.generation ?? 'unknown'} (${g.status})`)
                    .join(' · ')}
                  . Gateways are not running the same configuration.
                </div>
              ))}
              <DataTable
                caption="Configuration generations"
                columns={columns}
                rows={list}
                rowKey={(c) => c.id}
              />
              {selected && <ConfigDetail config={selected} onClose={() => setSelectedId(null)} />}
            </>
          );
        }}
      </QueryView>
    </>
  );
}

function GatewayStates({ config }: { config: ConfigGeneration }) {
  if (!config.gateways || config.gateways.length === 0)
    return <span className="muted">Not reported by API</span>;
  return (
    <ul className="plain-list">
      {config.gateways.map((g) => (
        <li key={g.gateway_id}>
          {g.gateway_id}: {g.generation != null ? `#${g.generation}` : 'unknown'}{' '}
          <StatusBadge status={g.status} />
        </li>
      ))}
    </ul>
  );
}

function ConfigDetail({ config, onClose }: { config: ConfigGeneration; onClose: () => void }) {
  const api = useApi();
  const { run } = useOperations();
  const validate = useCapability('config_validate');
  const deploy = useCapability('config_deploy');
  const rollback = useCapability('config_rollback');
  const [confirm, setConfirm] = useState<Verb | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const key = useRef(newKey());

  const verifyFor = (verb: Verb) => async (): Promise<VerifyResult> => {
    const latest = (await api.configurations.list()).find((c) => c.id === config.id);
    if (!latest)
      return { ok: false, detail: 'The generation could not be found after the operation.' };
    if (verb === 'validate')
      return latest.validation.status === 'passed'
        ? { ok: true, detail: 'The API reports validation as passed.' }
        : { ok: false, detail: `The API reports validation as ${latest.validation.status}.` };
    const allApplied =
      (latest.gateways ?? []).length > 0 &&
      latest.gateways!.every((g) => g.generation === config.generation && g.status === 'applied');
    return latest.deployment.status === 'deployed' && allApplied
      ? { ok: true, detail: `Every gateway reports generation ${config.generation} applied.` }
      : {
          ok: false,
          detail: `The API reports deployment as ${latest.deployment.status}; not every gateway confirms generation ${config.generation}.`,
        };
  };

  async function start(verb: Verb) {
    if (pending) return;
    setPending(true);
    setError(null);
    const call = (): Promise<Operation<ConfigGeneration>> =>
      api.configurations[verb](config.id, key.current);
    try {
      await run(
        `${verb[0]!.toUpperCase()}${verb.slice(1)} configuration generation ${config.generation}`,
        call,
        { verify: verifyFor(verb) },
      );
      key.current = newKey();
      setConfirm(null);
    } catch (err) {
      setError(isApiError(err) ? err.message : 'The request failed unexpectedly.');
    }
    setPending(false);
  }

  const notValidated = config.validation.status !== 'passed';
  return (
    <DetailPanel
      title={`Generation ${config.generation}`}
      description={`${config.id} · ${config.source ?? 'source not reported'}`}
      onClose={onClose}
    >
      <div className="stack">
        <dl className="kv">
          <dt>Checksum</dt>
          <dd>
            <code>{config.checksum}</code> <CopyButton value={config.checksum} label="checksum" />
          </dd>
          <dt>Validation</dt>
          <dd>
            <StatusBadge status={config.validation.status} />{' '}
            {config.validation.message && (
              <span className="muted">{config.validation.message}</span>
            )}
          </dd>
          <dt>Deployment</dt>
          <dd>
            <StatusBadge status={config.deployment.status} />
          </dd>
          <dt>Last known good</dt>
          <dd>
            {config.last_known_good == null
              ? 'Not tracked by API'
              : config.last_known_good
                ? 'Yes'
                : 'No'}
          </dd>
        </dl>
        <Card title="Gateways">
          <GatewayStates config={config} />
        </Card>
        <section>
          <h3 className="subsection-title">Changes</h3>
          {config.diff ? (
            <pre className="diff" tabIndex={0} aria-label="Configuration diff">
              {config.diff}
            </pre>
          ) : (
            <p className="muted">No diff supplied by the API.</p>
          )}
        </section>
        <section className="stack-sm">
          <h3 className="subsection-title">Actions</h3>
          <p className="muted">
            Validation checks syntax only. Deployment applies the generation to the gateways and is
            a separate step.
          </p>
          <CapabilityNote reason={validate.reason} />
          <div className="row wrap">
            <ActionButton
              variant="secondary"
              disabledReason={validate.reason}
              disabled={pending}
              onClick={() => void start('validate')}
            >
              Validate
            </ActionButton>
            <ActionButton
              variant="primary"
              disabledReason={
                deploy.reason ??
                (notValidated
                  ? 'Validate this generation successfully before deploying it.'
                  : undefined)
              }
              onClick={() => {
                setError(null);
                setConfirm('deploy');
              }}
            >
              Deploy
            </ActionButton>
            <ActionButton
              variant="danger"
              disabledReason={
                rollback.reason ??
                (config.validation.status !== 'passed'
                  ? 'Only a validated generation can be a rollback target.'
                  : undefined)
              }
              onClick={() => {
                setError(null);
                setConfirm('rollback');
              }}
            >
              Roll back to this generation
            </ActionButton>
          </div>
        </section>
      </div>
      {confirm && (
        <ConfirmDialog
          title={
            confirm === 'deploy'
              ? `Deploy generation ${config.generation}?`
              : `Roll back to generation ${config.generation}?`
          }
          tone={confirm === 'rollback' ? 'danger' : 'primary'}
          confirmLabel={confirm === 'deploy' ? 'Deploy to gateways' : 'Roll back gateways'}
          pending={pending}
          error={error}
          onConfirm={() => void start(confirm)}
          onCancel={() => setConfirm(null)}
          consequences={
            <p>
              {confirm === 'deploy'
                ? 'This applies the generation to the gateways'
                : `This replaces the running configuration on the gateways with generation ${config.generation}`}
              . Routing changes take effect on each gateway as it reloads. The result is only
              reported as applied after every gateway confirms it, and a partial rollout is shown as
              partial.
            </p>
          }
        />
      )}
    </DetailPanel>
  );
}
