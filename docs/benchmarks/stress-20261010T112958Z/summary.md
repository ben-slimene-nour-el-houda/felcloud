# Concurrent provisioning stress run

- Date (UTC): 2026-10-10T11:32:18.122629+00:00
- Commit: `3ec92f7444ed5585d83aadd29ecb0f096b2d3651`
- Mode: **MOCK — NOT a live benchmark**
- Teams: 12; concurrency: 12
- Successes: 12/12 (100.0%)
- Provision burst wall time: 226.3 ms
- Successful provisions/minute: 3181.01
- Gateway sync events: 9; successful provisions: 12; coalescing ratio: 1.333

## Assertions

- **PASS** — success count: 12/12; failures by step/status: none
- **PASS** — unique valid sandbox IPs: 12/12 unique; subnet=10.20.30.0/24; excluded addresses checked
- **PASS** — unique server_id and port_id values: servers=12/12, ports=12/12
- **PASS** — one allocated lease per successful team: lease endpoint HTTP 200; checked 12 successful teams and duplicate allocated IPs
- **SKIPPED** — successful hostnames respond via VIP/PUBLIC IP: Set VIP_IP or PUBLIC_IP to enable routed HTTP verification
- **PASS** — no sandbox remains PROVISIONING: endpoint HTTP 200; stuck=[]
- **PASS** — gateway sync events observed: 9 SYNC events versus 12 successful provisions (HTTP 200)
- **PASS** — teardown requests succeeded: 12/12 returned 200
- **PASS** — leases enter QUARANTINED: endpoint HTTP 200; verified 12 leases
- **PASS** — quarantined leases become FREE: completed within 120 seconds=True
- **PASS** — torn-down sandboxes are RELEASED: all 12 states RELEASED=True
- **SKIPPED** — OpenStack server/port cleanup: mock mode does not query OpenStack
- **PASS** — success rate threshold: 100.0% >= 100.0%

## Timing aggregates (ms)

| Timing | p50 | p95 | max |
|---|---:|---:|---:|
| allocate | 32.0 | 138.0 | 138.0 |
| port | 11.0 | 11.0 | 11.0 |
| server_active | 22.0 | 22.0 | 22.0 |
| health | 0.0 | 0.0 | 0.0 |
| sync | 0.0 | 0.0 | 0.0 |
| total | 99.0 | 164.0 | 164.0 |
| teardown_ms | 66.912 | 182.437 | 182.437 |

Results saved in `docs/benchmarks/stress-20261010T112958Z`.
