import { Globe, Server } from 'lucide-react';
import type { GatewaysView } from '../api/contracts';
import { StatusBadge, Timestamp } from './ui';

/** Roles, health and VIP ownership are rendered strictly from API data. */
export function GatewayTopology({
  view,
  detailed = false,
}: {
  view: GatewaysView;
  detailed?: boolean;
}) {
  const owner = view.gateways.find((g) => g.id === view.vip.owner_gateway_id);
  return (
    <div className="topology">
      <div className="vip" aria-label="Virtual IP">
        <Globe size={20} aria-hidden />
        <div>
          <p className="muted">Virtual IP</p>
          <p className="vip-address">{view.vip.address}</p>
          <p className="muted">{owner ? `Owned by ${owner.name}` : 'Owner not reported'}</p>
        </div>
      </div>
      <div className="gateway-grid">
        {view.gateways.map((g) => (
          <article
            key={g.id}
            className={`gateway card ${g.owns_vip ? 'gateway-owner' : ''}`}
            aria-label={g.name}
          >
            <header className="gateway-head">
              <Server size={18} aria-hidden />
              <h3 className="gateway-name">{g.name}</h3>
              <StatusBadge status={g.role} />
            </header>
            <dl className="kv">
              <dt>Virtual IP</dt>
              <dd>{g.owns_vip ? 'Owns the virtual IP' : 'Does not own the virtual IP'}</dd>
              <dt>HAProxy</dt>
              <dd>
                <StatusBadge status={g.haproxy.status} />
              </dd>
              <dt>Keepalived</dt>
              <dd>
                <StatusBadge status={g.keepalived.status} />
              </dd>
              {detailed && g.address && (
                <>
                  <dt>Address</dt>
                  <dd>
                    <code>{g.address}</code>
                  </dd>
                </>
              )}
              <dt>Last check</dt>
              <dd>
                <Timestamp value={g.last_check_at} />
              </dd>
              {detailed && (
                <>
                  <dt>Config generation</dt>
                  <dd>{g.config_generation ?? 'Not reported by API'}</dd>
                </>
              )}
            </dl>
          </article>
        ))}
      </div>
    </div>
  );
}
