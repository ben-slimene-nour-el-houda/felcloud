# Implementation status

Legend: **F** implemented in frontend · **D** demo mode only · **B** requires backend endpoint ·
**I** requires infrastructure verification.

| Area          | Capability                                                                                     | Status                                                                                |
| ------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Shell         | Sidebar / compact / drawer navigation, breadcrumb, connection indicator, refresh, token dialog | F                                                                                     |
| Shell         | Demo banner, API-down warning with last-known timestamp                                        | F                                                                                     |
| Overview      | Health, four metrics, topology, attention list, recent activity                                | F + B (`/health`, `/sandboxes`, `/ipam/stats`, `/gateways`, `/operations`, `/events`) |
| Sandboxes     | List, search, filter, sort, pagination, detail drawer                                          | F + B                                                                                 |
| Sandboxes     | Provision, teardown, 202 job polling, verification                                             | F + B; **I** for real outcomes                                                        |
| Sandboxes     | Simulated provisioning incl. failure case                                                      | D                                                                                     |
| IPAM          | Stats, lease list/filter/detail, lifecycle explainer                                           | F + B                                                                                 |
| IPAM          | Reconciliation                                                                                 | F + B (`POST /ipam/reconcile`) + I                                                    |
| Gateways      | Topology from API data, VIP owner, service health                                              | F + B                                                                                 |
| Gateways      | Manual failover with verification                                                              | F + B (`POST /gateways/failover`) + I                                                 |
| Gateways      | Failure drill (dry-run plan only; never stops a service)                                       | F + B (`GET /gateways/drill-plan`); disabled in demo                                  |
| Routes        | List, filters, detail, stages, optional latency/TLS                                            | F + B                                                                                 |
| Routes        | Route mutations                                                                                | Not implemented (no documented contract)                                              |
| Configuration | Generations, per-gateway state, diff, partial deployment                                       | F + B                                                                                 |
| Configuration | Validate / deploy / rollback with confirmation and verification                                | F + B + I                                                                             |
| Observability | Events, HAProxy logs, system logs, operation history                                           | F + B                                                                                 |
| Benchmarks    | Suites, run form, history, comparison, CSV/JSON export                                         | F + B                                                                                 |
| Benchmarks    | Real execution and persisted history                                                           | B + I                                                                                 |
| Auth          | Optional bearer token (tab-scoped)                                                             | F; login/session flow not included                                                    |
| Theme         | Light only                                                                                     | F                                                                                     |
| Streaming     | Event stream / SSE                                                                             | Not implemented (polling only)                                                        |
