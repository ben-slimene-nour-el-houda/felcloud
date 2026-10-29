# API contract (proposed)

This is a **proposed** contract. If a real OpenAPI specification exists, align
`src/api/contracts.ts` and `src/api/endpoints/*` with it. All paths are relative to
`VITE_API_BASE_URL` (default `/api`). Every response is validated at runtime with Zod.

## Conventions

- JSON in and out. Timestamps are ISO-8601 strings.
- Lists: `GET ...?q=&status=&sort=field:asc|desc&page=1&page_size=10` →
  `{ "items": [...], "total": 42, "page": 1, "page_size": 10 }`.
- Optional header `Authorization: Bearer <token>` — sent only if the operator set a token.
- Mutations send `Idempotency-Key: <uuid>`. The backend should return the original result for a repeated key.

## Endpoints

| Method & path                           | Purpose                                                      | Frontend    | Backend  |
| --------------------------------------- | ------------------------------------------------------------ | ----------- | -------- |
| `GET /health`                           | Health, components, **capabilities**                         | implemented | required |
| `GET /sandboxes`                        | List (`q`, `status`, `sort`, paging)                         | implemented | required |
| `GET /sandboxes/{id}`                   | Detail (used for verification)                               | implemented | required |
| `POST /sandboxes`                       | Provision `{name, team, ttl_hours}` → 201 sandbox or 202 job | implemented | required |
| `DELETE /sandboxes/{id}`                | Teardown → 200 sandbox or 202 job                            | implemented | required |
| `GET /jobs/{id}`                        | Job status                                                   | implemented | required |
| `GET /ipam/stats`                       | Pool statistics                                              | implemented | required |
| `GET /ipam/leases`                      | Leases (`q`, `state`, paging)                                | implemented | required |
| `POST /ipam/reconcile`                  | Reconcile leases → 200 `{message}` or 202 job                | implemented | required |
| `GET /gateways`                         | VIP + gateways                                               | implemented | required |
| `POST /gateways/failover`               | `{target_gateway_id}` → 200 or 202 job                       | implemented | required |
| `GET /gateways/drill-plan`              | Dry-run plan only                                            | implemented | optional |
| `GET /routes`                           | Routes (`q`, `health`, paging)                               | implemented | required |
| `GET /configurations`                   | Generations (optionally with `diff`)                         | implemented | required |
| `POST /configurations/{id}/validate`    | Validate                                                     | implemented | required |
| `POST /configurations/{id}/deploy`      | Deploy                                                       | implemented | required |
| `POST /configurations/{id}/rollback`    | Roll back to this generation                                 | implemented | required |
| `GET /events`                           | Events (`q`, `severity`, paging)                             | implemented | required |
| `GET /operations`                       | Operation history (`q`, `status`, paging)                    | implemented | required |
| `GET /logs/haproxy`, `GET /logs/system` | Logs (`gateway`, `since`, paging)                            | implemented | optional |
| `GET /benchmarks/suites`                | Supported suites and parameters                              | implemented | optional |
| `GET /benchmarks/runs`                  | Persisted runs                                               | implemented | optional |
| `POST /benchmarks/runs`                 | Start a run                                                  | implemented | optional |

Not defined (not implemented in the UI): route mutations, TLS management, event streaming.

## Capabilities

`GET /health` returns a `capabilities` object. Missing or `false` means the action is disabled in the UI
with an explanation. Keys: `sandbox_provision`, `sandbox_teardown`, `lease_reconcile`, `gateway_failover`,
`failover_drill`, `config_validate`, `config_deploy`, `config_rollback`, `logs_haproxy`, `logs_system`,
`benchmark_run`.

```json
{
  "status": "ok",
  "version": "0.4.1",
  "checked_at": "2026-03-12T09:30:00Z",
  "components": [{ "name": "control-plane", "status": "ok" }],
  "capabilities": { "sandbox_provision": true, "gateway_failover": false }
}
```

## Key shapes

Sandbox (`status`: `requested|provisioning|active|failed|terminating|terminated|expired`):

```json
{
  "id": "sbx-001",
  "name": "alpha-api",
  "team": "team-alpha",
  "status": "active",
  "private_ip": "10.20.0.11",
  "hostname": "alpha-api.example",
  "lease_id": "lease-11",
  "created_at": "...",
  "expires_at": "..."
}
```

Lease (`state`: `free|allocated|quarantined`; `until` = lease expiry or cooldown end):

```json
{
  "id": "lease-11",
  "ip": "10.20.0.11",
  "state": "allocated",
  "sandbox_id": "sbx-001",
  "sandbox_name": "alpha-api",
  "allocated_at": "...",
  "until": "..."
}
```

Gateway (`role`: `master|backup|fault|unknown`; service `status`: `up|down|unknown`):

```json
{
  "vip": { "address": "10.20.0.100", "owner_gateway_id": "gw-1" },
  "gateways": [
    {
      "id": "gw-1",
      "name": "gateway-1",
      "role": "master",
      "owns_vip": true,
      "haproxy": { "status": "up" },
      "keepalived": { "status": "up" },
      "last_check_at": "...",
      "config_generation": 42
    }
  ]
}
```

Configuration generation: `validation.status` (`passed|failed|not_run`) and `deployment.status`
(`not_deployed|pending|deployed|partial|failed`) are separate fields; `gateways[]` reports each gateway's
`generation` and `status` (`applied|pending|failed|unknown`). Omit `gateways` if not tracked — the UI then
shows "Not reported by API".

## Errors

Any non-2xx response:

```json
{
  "error": {
    "code": "conflict",
    "message": "Human readable text",
    "details": [{ "field": "name", "message": "Already exists" }]
  },
  "trace_id": "trc-77a1"
}
```

`X-Request-ID` is used as the trace ID if `trace_id` is absent. Handled: 401, 403, 404, 409, 422/400, 429
(`Retry-After`), 5xx, timeouts and network failures. Never put credentials or stack traces in `message`;
the client also redacts them defensively.

## Job lifecycle

1. A mutation returns `202 Accepted` with a job: `{ "id": "job-1", "status": "pending" }`.
2. The UI polls `GET /jobs/{id}`. `status`: `pending → running → succeeded | failed`.
3. Optional `steps: [{ name, status }]` is rendered as-is; **no percentage is ever invented**.
4. On `failed`, `error.message` is shown. On `succeeded`, the UI re-reads authoritative state and only then
   reports a verified result.
5. If `GET /jobs/{id}` returns 404 the outcome is shown as unknown. Job IDs are kept in `sessionStorage`
   so tracking resumes after a reload.

## Idempotency

The UI generates one `Idempotency-Key` per form/dialog and reuses it if the same submission is retried by
the operator after an error. Mutations are never retried automatically. Without server support for the
header, a deliberate retry can duplicate work — the backend should enforce uniqueness itself (for example
sandbox names).
