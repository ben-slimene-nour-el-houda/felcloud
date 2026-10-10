# FelCloud Gateway API Reference

Base URL: `http://<control-plane>:8081`  
Auth: `Authorization: Bearer <CONTROL_PLANE_TOKEN>` on all admin endpoints  
CORS origin: controlled by `DASHBOARD_ORIGIN` env var

---

## Error envelope

All error responses share this shape:

```json
{ "error": "short message", "detail": "optional context", "code": 400 }
```

---

## GET /health

No auth required.

**Response 200**
```json
{
  "status":    "UP",
  "source":    "controlplane-gateway",
  "db":        "OK",
  "timestamp": "2026-10-10T12:00:57Z"
}
```

---

## GET /api/gateways/status

Live probe of both gateways (parallel Ansible ad-hoc, 8 s timeout, cached 3 s).
An unreachable gateway returns `reachable:false` and never crashes the endpoint.

**Response 200**
```json
{
  "gateways": {
    "gw1": {
      "reachable":        true,
      "haproxy":          "UP",
      "keepalived":       "MASTER",
      "vip_holder_local": true,
      "probed_at":        "2026-10-10T12:00:57Z",
      "error":            ""
    },
    "gw2": {
      "reachable":        true,
      "haproxy":          "UP",
      "keepalived":       "BACKUP",
      "vip_holder_local": false,
      "probed_at":        "2026-10-10T12:00:57Z",
      "error":            ""
    }
  },
  "vip":               "10.20.20.246",
  "vip_holder":        "gw1",
  "latest_generation": "gen-20261010120000",
  "fetched_at_ms":     1728556857123
}
```

**Field values**
| Field | Values |
|-------|--------|
| `haproxy` | `UP` · `DOWN` · `UNKNOWN` |
| `keepalived` | `MASTER` · `BACKUP` · `FAULT` · `UNKNOWN` |
| `vip_holder` (top-level) | `gw1` · `gw2` · `unknown` |

**Environment variables that affect probes**
| Var | Default |
|-----|---------|
| `VIP_ADDRESS` | `10.20.20.246` |
| `BASTION_IP` | `203.0.113.119` |
| `ANSIBLE_INVENTORY` | `../ansible/inventory/hosts.yml` |
| `ANSIBLE_VAULT_PASS` | `../ansible/.vault_pass` |

---

## GET /api/gateways/logs

Tails logs from the requested gateway via Ansible ad-hoc SSH. Whitelist enforced on `gateway` and `source` — no shell injection possible.

**Query params**
| Param | Required | Values | Default |
|-------|----------|--------|---------|
| `gateway` | ✅ | `gw1` · `gw2` | — |
| `source` | ❌ | `haproxy` · `keepalived` | `haproxy` |
| `limit` | ❌ | 1–500 | 50 |
| `since` | ❌ | systemd `--since` string e.g. `"2026-10-10 12:00:00"` | — |

**Response 200**
```json
{
  "gateway": "gw1",
  "source":  "haproxy",
  "since":   "",
  "limit":   50,
  "count":   3,
  "entries": [
    "Oct 10 12:00:01 gw1 haproxy[1234]: ...",
    "Oct 10 11:59:55 gw1 haproxy[1234]: ..."
  ]
}
```

**Error responses**
- `400` — missing/invalid `gateway` or `source`
- `502` — Ansible SSH failed (gateway unreachable)

---

## GET /api/gateways/events

Reads from the `events` table. Supports filtering.

**Query params**
| Param | Example | Description |
|-------|---------|-------------|
| `type` | `keepalived_state` | Filter by event type |
| `since` | `1728555000000` | Epoch ms lower bound (inclusive) |
| `limit` | `50` | 1–500, default 50 |

**Response 200**
```json
{
  "events": [
    {
      "id":          42,
      "ts_ms":       1728556000000,
      "ts_iso":      "2026-10-10T12:06:40Z",
      "sandbox_id":  "",
      "type":        "keepalived_state",
      "detail":      "state=MASTER host=gw1",
      "duration_ms": 0
    }
  ],
  "count": 1,
  "limit": 50
}
```

---

## POST /api/gateways/events

Stores a gateway event. Used by notify scripts on the gateways.

**Auth (dual-token, in priority order)**
1. `Authorization: Bearer <CONTROL_PLANE_TOKEN>` — admin token
2. `X-Events-Token: <EVENTS_TOKEN>` — lightweight token for notify scripts
3. If both env vars are unset: accepts from gateway subnet (`GATEWAY_SUBNET`, default `10.20.10.0/24`)

**Request body**
```json
{
  "type":        "keepalived_state",
  "state":       "MASTER",
  "hostname":    "gw1",
  "ts_ms":       1728556000000,
  "duration_ms": 0,
  "detail":      "optional free-form text",
  "sandbox_id":  ""
}
```

**Allowed `type` values:** `failover` · `rollback` · `keepalived_state` · `sync` · `failover_test` · `health_check`

**Response 201**
```json
{
  "accepted":   true,
  "type":       "keepalived_state",
  "ts_ms":      1728556000000,
  "detail":     "state=MASTER host=gw1",
  "sandbox_id": ""
}
```

**Error responses**
- `400` — missing `type`, unknown `type`, invalid JSON
- `401` — not authorised

---

## GET /api/gateways/generations

HAProxy config generations from `config_generations` table.

**Query params:** `limit` (1–100, default 20)

**Response 200**
```json
{
  "generations": [
    {
      "id":            12,
      "generation_id": "gen-20261010120000",
      "status":        "COMMITTED",
      "created_at":    "2026-10-10 12:00:00",
      "backend_count": 3
    }
  ],
  "count": 1
}
```

**`status` values:** `PROPOSED` · `VALIDATED` · `COMMITTED` · `ROLLED_BACK` · `FAILED`

---

## POST /api/gateways/failover

Trigger a manual VRRP failover. Stub — real Ansible action in next phase.

**Request body**
```json
{ "confirm": true, "mode": "manual" }
```

**Response 200**
```json
{ "success": true, "before": "gw1", "after": "gw2", "downtime_ms": 0 }
```

---

## POST /api/gateways/sync

Regenerate HAProxy config and deploy to both gateways.

**Request body**
```json
{ "dry_run": false }
```

**Response 200** — `{ "success": true, "message": "Generation gen-... applied" }`  
**Response 500** — `{ "error": "sync failed", "detail": "..." }`

---

## POST /api/gateways/rollback

Roll back to the last COMMITTED generation.

**Response 200** — `{ "success": true }`  
**Response 500** — `{ "error": "rollback failed" }`

---

## GET /api/sandboxes

Real-time snapshot from the `sandboxes` table.

**Response 200**
```json
{
  "sandboxes": [
    {
      "id":                  1,
      "sandbox_id":          "team1",
      "hostname":            "team1.cstam.felcloud.tn",
      "subnet_name":         "sandbox",
      "private_ip":          "10.20.30.70",
      "status":              "ACTIVE",
      "created_at":          "2026-10-07 21:31:36",
      "updated_at":          "2026-10-07 21:31:36",
      "expires_at":          null,
      "ttl_remaining_seconds": null
    }
  ],
  "count": 1
}
```

---

## GET /api/leases  ·  GET /ipam/leases

Real-time snapshot from the `ip_allocations` table.

**Response 200**
```json
{
  "leases": [
    {
      "id":               1,
      "ip_address":       "10.20.10.24",
      "subnet_name":      "mgmt",
      "entity_name":      "gw1",
      "entity_type":      "GATEWAY",
      "status":           "ALLOCATED",
      "quarantined_at":   "",
      "quarantine_until": "",
      "assigned_at":      "2026-10-07 21:31:36"
    }
  ],
  "count": 13
}
```

---

## Security-group rule needed (network step)

The notify scripts on GW1/GW2 POST to the control-plane on port 8081.
Add an ingress rule on the **control-plane host's security group**:

| Direction | Protocol | Port | Source |
|-----------|----------|------|--------|
| Ingress | TCP | 8081 | `10.20.10.0/24` (mgmt subnet — gateway management IPs) |

The control-plane also needs to **reach the gateways** on port 22 (SSH via bastion). That path already exists through the bastion proxy; no additional rule is required.

---

## Environment variable reference

| Var | Used by | Default |
|-----|---------|---------|
| `CONTROL_PLANE_TOKEN` | All admin endpoints | _(none — open)_ |
| `EVENTS_TOKEN` | `POST /api/gateways/events` | _(none)_ |
| `GATEWAY_SUBNET` | Events fallback IP auth | `10.20.10.0/24` |
| `DASHBOARD_ORIGIN` | CORS Allow-Origin | _(none)_ |
| `VIP_ADDRESS` | Status probe | `10.20.20.246` |
| `BASTION_IP` | Ansible ad-hoc proxy | `203.0.113.119` |
| `ANSIBLE_INVENTORY` | All Ansible calls | `../ansible/inventory/hosts.yml` |
| `ANSIBLE_VAULT_PASS` | All Ansible calls | `../ansible/.vault_pass` |
| `IPAM_DB_PATH` | DB connection | auto-detected from binary location |
| `PORT` | `serve` port fallback | `8081` |
