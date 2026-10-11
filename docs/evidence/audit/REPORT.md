# FelCloud Resilient IP Optimizer — Verification Audit

## Summary

The Go unit suite and local mock lifecycle checks passed, but they do not establish that provisioning or gateway operations work against OpenStack or live gateways.
Several live-path blockers are present: `serve` does not configure its provisioner, the default direct health probe is not admitted by the sandbox security group, and generated HAProxy config omits the keepalived health endpoint.
No live verification was attempted intentionally after an initial unshimmed gateway-sync request; whether that request reached a bastion or gateway cannot be established from its response.

## Audit scope and verdicts

All experiments were intended to use `/tmp/audit-copy`, created with `rsync -a --exclude .git`. Temporary reproductions were placed there; the only repository files created by this audit are under `docs/evidence/audit/`. The copy-only server wiring adjustment and all reproduction tests are not product changes.

Verdicts used below: **VERIFIED-TEST** means a passing automated test exercises the behavior; **VERIFIED-MOCK** means a local end-to-end mock run exercised it; **CODE-ONLY** means source was inspected but no test/run verified it; **BROKEN** means a failure was reproduced or the required behavior is incompatible with the source; **CONTRADICTED** means documentation asserts behavior that differs from code; **BLOCKED** means a prerequisite was unavailable. “Live-verified” was not available and is not claimed.

**Safety deviation:** The first full mock smoke was run before command shims had been installed. Its POST to `/api/gateways/sync` returned `502` after 2.018 seconds with “gw1 HAProxy is not active.” The code path runs `ansible` with an inventory and default ProxyJump configuration ([gateway_probe.go](../../../controlplane/gateway_probe.go#L102), [gateway_probe.go](../../../controlplane/gateway_probe.go#L153), [mock-smoke.txt](raw/mock-smoke.txt)). I cannot establish whether that Ansible invocation reached a bastion or gateway. Treat possible infrastructure contact as unverified; this was a procedure violation. The rollback request stopped at a missing local committed snapshot. Subsequent reproduction and script runs used blocking `ansible`, `ansible-playbook`, `ssh`, and `openstack` shims. No OpenStack CLI command was run. No more unshimmed infrastructure operations were attempted.

## Environment

| Item | Observed |
|---|---|
| Repository HEAD | `9d6a0b24d425318169c27746103014d1d1a0a2a5` |
| Initial worktree | Clean except for the audit evidence directory; see [phase0.txt](raw/phase0.txt) |
| Go | `go1.27.1 linux/amd64` |
| Python | `3.14.7` |
| Ansible | `2.10.8` |
| `ansible-lint` | **BLOCKED** — executable missing |
| HAProxy CLI | **BLOCKED** — executable missing, so `haproxy -c` was not run |
| Test location | `/tmp/audit-copy` (source copy); only evidence files written in repo |

Full baseline: [phase0.txt](raw/phase0.txt). Tool/static check output: [phase1.txt](raw/phase1.txt).

## What works

| Claim | Verdict | Evidence / limits |
|---|---|---|
| Go control-plane package passes vet, formats cleanly, and builds from its own module. | VERIFIED-TEST | `go vet ./...`, `gofmt -l .`, and `go build -o /tmp/cp-bin .` succeeded in the copy ([phase1.txt](raw/phase1.txt)). |
| Root README build command works. | BROKEN | `go build ./controlplane` from the root failed because the root module does not contain `felcloud/controlplane/controlplane`; the Go control plane is a nested module ([phase1.txt](raw/phase1.txt), [go.mod](../../../go.mod#L1), [controlplane/go.mod](../../../controlplane/go.mod#L1)). |
| Python support scripts compile and their unit tests pass. | VERIFIED-TEST | `python3 -m py_compile scripts/*.py` passed; `python3 -m unittest` passed 4 tests ([phase1.txt](raw/phase1.txt)). |
| Shell scripts parse. | VERIFIED-TEST | `bash -n scripts/*.sh` passed ([phase1.txt](raw/phase1.txt)). |
| The regular Go test run passes. | VERIFIED-TEST | 211 JSON test-result events passed, 0 failed, 0 skipped; the complete root/subtest-to-file inventory is [go-test-inventory.md](raw/go-test-inventory.md), summary and slowest tests are [go-test-inventory-summary.txt](raw/go-test-inventory-summary.txt). |
| The full project test runner passes. | VERIFIED-TEST | `scripts/run-all-tests.sh` reported 7/7, including its race-test step ([run-all-tests.txt](raw/run-all-tests.txt)). |
| IPAM quarantine and reclaim work in the isolated runner. | VERIFIED-MOCK | `scripts/recycle-test.sh` passed: IP was not reused during quarantine and was reusable after reclaim ([recycle-test.txt](raw/recycle-test.txt)). |
| Mock API auth, basic provisioning/idempotency, input validation, extension, teardown, and reclaim work after configuring the mock provisioner. | VERIFIED-MOCK | Health 200 without auth; protected routes 401 without/wrong token; provision 201 then repeat 200; invalid names/strict payload errors; extend 200/400/422; teardown 200 and QUARANTINED→FREE ([mock-smoke.txt](raw/mock-smoke.txt)). This smoke used a copy-only `configureProvisioner` insertion because the checked-in `serve` path fails as documented in D12. |
| The teardown ownership guard refuses externally managed rows and records one refusal event per row. | VERIFIED-MOCK | Two seeded external ACTIVE rows each returned 403, remained ACTIVE, and had exactly one `TEARDOWN_REFUSED` event ([teardown-guard.txt](raw/teardown-guard.txt)). |
| Twelve mock provisions can run concurrently with unique IPs. | VERIFIED-MOCK | 12/12 returned 201/ACTIVE with 12 distinct addresses. The accompanying stress script recorded 9 `SYNC` events for 12 provisions; VIP routing was skipped ([mock-smoke.txt](raw/mock-smoke.txt), [stress-test.txt](raw/stress-test.txt)). |
| API rate limiting and `CORS_ORIGIN` preflight behavior work locally. | VERIFIED-MOCK | With a limit of 3, the fourth mutating call returned 429; an allowed configured origin received ACAO ([rate-cors-auth.txt](raw/rate-cors-auth.txt)). |
| Functional failover/rollback code paths exist and can be exercised with fakes. | VERIFIED-TEST | Existing failover tests use injected status/command/probe functions. The throwaway D8 test verifies the manual rollback toggle, empty-history failure, and fake auto-rollback redeploy ([gateway_failover_test.go](../../../controlplane/gateway_failover_test.go#L35), [phase4-repro.txt](raw/phase4-repro.txt)). This is not a live gateway test. |

## What is broken

Severity describes operational impact, not exploitability.

| ID | Verdict | Severity | Evidence | Finding | One-line fix proposal (not applied) |
|---|---|---:|---|---|---|
| D1 | BROKEN | HIGH | [phase4-repro.txt](raw/phase4-repro.txt); [gateway_sync.go](../../../controlplane/gateway_sync.go#L143); [haproxy.cfg.j2](../../../ansible/roles/haproxy/templates/haproxy.cfg.j2#L27); [check_haproxy.sh.j2](../../../ansible/roles/keepalived/templates/check_haproxy.sh.j2#L9) | `generateHAProxyConfig` omits `frontend health_check`, `127.0.0.1:9000`, and `/healthz`; the template and Keepalived script require it. With HAProxy unavailable, runtime validation was BLOCKED; the script’s curl check would fail if the generated config were deployed and the endpoint were absent. | Generate the same loopback health frontend and validate generated config against the template contract. |
| D2 | BROKEN | HIGH | [health_probe.go](../../../controlplane/health_probe.go#L30); [security.go](../../../infra/security.go#L100); [source-excerpts.txt](raw/source-excerpts.txt) | Non-mock health probing defaults to `direct` and requests sandbox port 80, while `sg-sandbox` admits port 80 only from `sg-gateway`. A control-plane host attached to the separate control-plane SG is not an admitted source. This makes default real provisioning health checks incompatible with the declared SG rules. | Select a reachable gateway-SSH probe by default or explicitly allow only the control plane’s address to reach the probe endpoint. |
| D3 | BROKEN (against the requested sub-second auto-rollback target) | HIGH | [gateway_deploy.go](../../../controlplane/gateway_deploy.go#L236); [gateway_deploy.go](../../../controlplane/gateway_deploy.go#L248); [gateway_probe.go](../../../controlplane/gateway_probe.go#L102); [gateway_probe.go](../../../controlplane/gateway_probe.go#L153) | The default health regression window is 45 seconds. Each sampled live health check runs two concurrent Ansible gateway probes (each capped at 2 seconds), a VIP HTTP probe, and an Ansible backend-stats command capped at 8 seconds. The loop sleeps one second between samples. Therefore a successful window alone takes at least 45 seconds plus probe/deploy time; rollback verification can add another window. `provisionTimeout` is 180 seconds by default, not a sub-second rollback guarantee. | Separate fast deployment validation from any longer soak window and define/measure an explicit bounded auto-rollback SLO. |
| D4 | BROKEN | MEDIUM | [phase4-repro.txt](raw/phase4-repro.txt); [gateway_sync.go](../../../controlplane/gateway_sync.go#L205) | `updateAnsibleTeams` includes rows with `status='PROVISIONING'`; the reproduction put `not-ready` at `10.20.30.42` into both frontends before it was healthy. | Generate public routes only for ACTIVE rows, or keep a PROVISIONING backend disabled until health succeeds. |
| D5 | BROKEN | HIGH | [main.go](../../../controlplane/main.go#L162); [ipam.go](../../../controlplane/ipam.go#L19); [server.go](../../../controlplane/server.go#L43); [gateway_failover.go](../../../controlplane/gateway_failover.go#L140) | From `controlplane/` with no `IPAM_DB_PATH`, `dbPath` resolves to `controlplane/ipam/ipam.db` (fallback); `newGatewayStore` opens `../ipam/ipam.db` (repo-root `ipam/ipam.db`). With `IPAM_DB_PATH` set, the IPAM/event DB follows it but gateway generations/status and failover event recording still use `store.db` at the relative path. | Resolve one absolute DB path once and pass the same store/connection policy to IPAM, gateway state, and event recording. |
| D6 | BROKEN | HIGH | [double-auth-cases.txt](raw/double-auth-cases.txt); [rate-cors-auth.txt](raw/rate-cors-auth.txt); [server.go](../../../controlplane/server.go#L63); [auth.go](../../../controlplane/auth.go#L251) | `securityHandlerOrUnavailable` requires `ADMIN_TOKEN` around a route whose inner middleware separately requires `CONTROL_PLANE_TOKEN` or `EVENTS_TOKEN`/subnet. With ADMIN set, X-Events-Token alone returns 401; ADMIN bearer alone returns 401 when `EVENTS_TOKEN` is set; both headers reach the handler. A subnet fallback cannot pass the outer admin gate. If `CONTROL_PLANE_TOKEN` and `ADMIN_TOKEN` differ, the single Authorization header cannot satisfy both. Docs also name `DASHBOARD_ORIGIN`, but code reads `CORS_ORIGIN`; a preflight with only `DASHBOARD_ORIGIN` returned 204 without ACAO. | Define one coherent middleware/token contract for notify and admin callers, then align the docs and CORS variable. |
| D8 | BROKEN (manual rollback history selection) | HIGH | [phase4-repro.txt](raw/phase4-repro.txt); [gateway_deploy.go](../../../controlplane/gateway_deploy.go#L305) | A first manual rollback deployed `GOOD_CONFIG`; a second manual rollback selected and redeployed `BAD_CONFIG` because each operation queries the prior committed row with `OFFSET 1`, then inserts another committed row. With no earlier committed row, rollback failed before deploy and left generation state unchanged. A fake validation/deploy failure did invoke a second auto-rollback deployment and commit it; no live result is claimed. | Track the currently active/known-good generation and prevent a rollback operation from reselecting a failed snapshot. |
| D9 | CODE-ONLY | MEDIUM | [ipam.go](../../../controlplane/ipam.go#L427); [openstack.go](../../../controlplane/openstack.go#L435); [provisioning.go](../../../controlplane/provisioning.go#L330) | IP selection scans only the local IPAM DB and begins at network address +2; it does not query Neutron before selection. `CreateSandbox` then requests that fixed IP for the Neutron port. A real collision was not induced because cloud calls were prohibited; source shows create failure enters compensation, marks the sandbox FAILED, and quarantines the local lease. | Make Neutron allocation authoritative or add a collision-safe retry path before committing the local lease. |
| D10 | BROKEN | HIGH | [gateway_sync.go](../../../controlplane/gateway_sync.go#L261); [provisioning.go](../../../controlplane/provisioning.go#L249); [teardown.go](../../../controlplane/teardown.go#L162); [main.go](../../../controlplane/main.go#L352); [api.go](../../../controlplane/api.go#L263) | `failoverMu` protects manual HTTP sync/rollback/failover, but not CLI sync/rollback, `addTeamFlow`/`removeTeamFlow`, or provision/teardown/reaper syncs. Provision/teardown share a separate coalescer, not the manual-operation mutex. These paths can race with a manual gateway deploy/rollback/failover. | Route all config-changing operations through one shared operation lock/coordinator. |
| D12 | BROKEN | HIGH | [unpatched-serve-provision.txt](raw/unpatched-serve-provision.txt); [main.go](../../../controlplane/main.go#L374); [server.go](../../../controlplane/server.go#L754); [openstack.go](../../../controlplane/openstack.go#L228) | The unmodified `serve --port` path calls `startGatewayServer`, which initializes DB/server but does not call `configureProvisioner`; a local `PROVISIONER=mock` provision request returned 502 `provisioner is not configured`. The main mock smoke only passed after a copy-only startup wiring change. | Configure the provisioner before starting the HTTP server and fail startup explicitly if configuration fails. |
| D13 | BROKEN | MEDIUM | [gateway-mutations-validation.txt](raw/gateway-mutations-validation.txt); [server.go](../../../controlplane/server.go#L361); [gateway_failover.go](../../../controlplane/gateway_failover.go#L210) | For each canonical gateway sync/rollback/failover route, valid JSON with an unknown field and a valid body over 2 MiB reached handler processing instead of failing decode/size validation. Sync/rollback returned downstream errors; failover ran to its 3-second 504 path. Other sandbox/reconcile routes use strict JSON and a configured body cap. | Apply the shared strict decoder and `MaxBytesReader` to gateway mutation routes. |

### Phase 4 disposition

| Suspected item | Verdict / measured result |
|---|---|
| D1 generator health frontend missing | BROKEN; copy-only regression test confirms missing loopback endpoint. `haproxy -c` BLOCKED because HAProxy is not installed. |
| D2 direct health probe versus SG ingress | BROKEN by static source/config: only gateway SG is admitted on sandbox TCP 80. No live probe was attempted. |
| D3 regression-window/rollback latency | BROKEN against the sub-second target; source-level lower bound is 45 seconds per successful default health window, before deployment latency. |
| D4 PROVISIONING route | BROKEN; reproduction test produced frontend ACLs and backend for an unready row. |
| D5 DB path split | BROKEN by path resolution and call-site inspection; see source excerpts. |
| D6 double auth and CORS env | BROKEN; localhost requests demonstrate token layering; `DASHBOARD_ORIGIN` alone did not produce ACAO, while `CORS_ORIGIN` did. |
| D7 failover timing/semantics | VERIFIED-TEST for stop→peer response→restart and changed-holder reporting. `START_MS` precedes Ansible stop; downtime starts only on the first failed/unusable probe. If the first probe already sees the new holder, `downtime_ms` remains 0. `failover-test.sh` contains no `FAILURE_MODE`; the API accepts only `stop` then restoration `start`. This is a measurement limitation, not a reproduced transition failure. |
| D8 rollback semantics | BROKEN for repeated manual rollback toggling back to bad history; no-prior failure is non-mutating; failed fake deployment triggers an auto-rollback redeploy. |
| D9 local IP selection versus Neutron | CODE-ONLY; cloud collision was not reproduced. Allocation begins at `.2` for a `/24` and does not consult Neutron. |
| D10 operation lock coverage | BROKEN by call-site inspection; see defect table. |
| D11 docs against code | CONTRADICTED for listed assertions; not all listed text is a defect (see below). |

Raw reproduction results: [phase4-repro.txt](raw/phase4-repro.txt). The original D8-only run is retained as [phase4-tests.txt](raw/phase4-tests.txt).

## Test inventory and execution

The regular test command was run in the throwaway copy with `-count=1 -timeout 180s -v -json`; **211 result events passed, 0 failed, 0 skipped**. The full per-test/subtest inventory records name, source file, status, duration, and the test-code shell-out search result: [go-test-inventory.md](raw/go-test-inventory.md). The slowest 10 are in [go-test-inventory-summary.txt](raw/go-test-inventory-summary.txt); slowest was `TestReclaimExpiredQuarantine` at 2.060 s. No Go test file contains a direct `exec.Command`/`exec.CommandContext` call (the recorded `rg` check found none); test seams inject fake gateway, deployment, health, and provisioner runners where those operations are covered.

| Check | Result |
|---|---|
| `go test -list .` | Completed; names are in [phase2.txt](raw/phase2.txt), with mapped files in the inventory. |
| Go normal suite | PASS, 211 result events; [gotest.json](raw/gotest.json). |
| Standalone Go race command | **BLOCKED/TIMEOUT** at the outer 300-second timeout with no JSON output; [gotest-race.json](raw/gotest-race.json). |
| Race step in `scripts/run-all-tests.sh` | PASS as part of 7/7 runner; this runner’s race result is not a substitute for the timed-out standalone invocation ([run-all-tests.txt](raw/run-all-tests.txt)). |
| `go vet ./...`, `gofmt -l .`, package build | PASS ([phase1.txt](raw/phase1.txt)). |
| Root README build command | FAIL: root module has no `controlplane` package ([phase1.txt](raw/phase1.txt)). |
| `ansible-lint`, `haproxy -c` | BLOCKED: executables missing ([phase0.txt](raw/phase0.txt), [phase1.txt](raw/phase1.txt)). |
| Python compile/unit; Bash syntax | PASS; 4 Python tests ([phase1.txt](raw/phase1.txt)). |
| `scripts/recycle-test.sh` | PASS ([recycle-test.txt](raw/recycle-test.txt)). |
| `scripts/run-all-tests.sh` | PASS, 7/7 ([run-all-tests.txt](raw/run-all-tests.txt)). |
| `scripts/stress-test.sh` (`TEAMS=12`, mock) | 12/12 provisions, 12 unique IPs, 9 sync events; teardown 12/12; all quarantined and RELEASED; **FAIL** because the script did not observe quarantined leases becoming FREE within 120 seconds; VIP and OpenStack cleanup skipped ([stress-test.txt](raw/stress-test.txt)). |

## Tested versus untested requirements

| Requirement | Unit test coverage | Mock run | Live | Verdict / gap |
|---|---|---|---|---|
| 1.1 Provisioning | `TestProvisionFlowAndCompensation`, `TestProvisionIdempotencyAndNameValidation`, `TestConcurrentProvisioningAndSyncCoalescing` | Single/idempotent and 12-way provisions; mock only, with a copy-only startup wiring adjustment | Not run | VERIFIED-MOCK for local lifecycle only; actual `serve` path BROKEN and cloud Nova/Neutron/health remain unverified. |
| 1.2 Dual HA gateways | Config generation, gateway failover fake-runner tests | No successful live gateway sync; one unshimmed sync request returned baseline probe failure | Not run | CODE-ONLY/partial VERIFIED-TEST. Missing generated health frontend; no live HAProxy/VRRP proof. |
| 1.3 IPAM and reclamation | Allocation/CIDR/concurrency/release/reclaim tests, including `TestReclaimExpiredQuarantine` | Recycle test passed; local teardown/reclaim transitioned QUARANTINED→FREE | Not run | VERIFIED-MOCK for local DB only; no Neutron collision/reconciliation against cloud. |
| 1.4 Zero-downtime reload and rollback | Health-window and injected failover/deploy tests; D8 fake-runner reproduction | No successful gateway deployment; initial sync failed before deployment | Not run | BROKEN against sub-second auto-rollback target; 45 s default verification window and real deployment unverified. |
| 2.1 Concurrent stress, 10+ teams | `TestConcurrentProvisioningAndSyncCoalescing` | 12/12 successful, unique IPs; 9 syncs/12 creates; stress reclamation assertion failed | Not run | VERIFIED-MOCK only; no public VIP or live OpenStack verification. |
| 2.2 Teardown and lifecycle | Teardown restart/idempotency/reaper/parallel tests | Teardown, ownership refusal, quarantine, recycle checks; stress free-after-120s assertion failed | Not run | VERIFIED-MOCK for isolated DB/provisioner. |
| 2.3 Isolation and API security | Auth, strict JSON, limiter/CORS tests | Bearer auth, rate limit, CORS, and owner guard exercised | Not run | VERIFIED-MOCK for API middleware. Security-group rules are CODE-ONLY; no Pulumi deployment or packet-level test. |
| Phase 3 dashboard | N/A | N/A | N/A | NOT-IMPLEMENTED: no frontend/dashboard source found. Backend APIs exist (sandbox/lease/events/gateway status+generations, provisioning/extension/teardown, reconciliation, sync/rollback/failover). |
| Phase 4 pitching | N/A | N/A | N/A | CODE-ONLY: README and technical/API docs exist; no pitch deck or demo video found. |

“LIVE-VERIFIED” is unavailable. No passed unit or mock test is live evidence.

### Security-group review (static only)

`sg-sandbox` deletes provider defaults; ingress permits TCP 80 and 8080 only from `sg-gateway`. This supports sandbox-to-sandbox ingress isolation and gateway-only application ingress at source level. Sandbox egress is explicit DNS 53, HTTP 80, HTTPS 443 to `0.0.0.0/0`, plus all protocols to the gateway SG; it is not a blanket egress deny. `sg-management` admits DPA 5555 from `ControlPlaneCIDR`, all protocols from `MgmtCIDR`, and optional SSH from `AdminSSHCIDR`; the separate control-plane SG admits TCP 8081 from `MgmtCIDR`. Thus sandbox-to-management and gateway-management protection are only statically inferred from these source CIDR/SG rules; no deployed rules or packets were tested. See [security.go](../../../infra/security.go#L90) and [source-excerpts.txt](raw/source-excerpts.txt).

## Contradictions between docs and code

| Documentation claim | Code/evidence | Disposition |
|---|---|---|
| README says rollback is a placeholder and does not restore live config. | The canonical router invokes `rollbackConfigWithOptions`, which selects a committed snapshot and deploys it; manual-history selection has a separate D8 defect. ([README.md](../../../README.md#L64), [README.md](../../../README.md#L326), [server.go](../../../controlplane/server.go#L395), [gateway_deploy.go](../../../controlplane/gateway_deploy.go#L305)) | CONTRADICTED |
| README says the test suite benchmarks measured VRRP failover times. | The documented runner checks configuration; the failover script is a separate live test. Existing benchmark report labels itself “MOCK DATA - NOT A BENCHMARK” and gateway failover NOT MEASURED. ([README.md](../../../README.md#L64), [BENCHMARK_REPORT.md](../../benchmarks/BENCHMARK_REPORT.md), [report.json](../../benchmarks/report.json)) | CONTRADICTED as a claim about existing evidence |
| README instructs root `go build -o ... ./controlplane`. | Root build failed; `controlplane/` is a nested module. ([README.md](../../../README.md#L421), [phase1.txt](raw/phase1.txt), [controlplane/go.mod](../../../controlplane/go.mod#L1)) | CONTRADICTED |
| README/examples and `.env.example` commonly use port 8080 as the control-plane default. | `serve` defaults to 8081; explicit README `serve --port 8080` is internally consistent only for that command. `.env.example` `CONTROL_PLANE_BASE_URL` 8080 can diverge from default `serve`. ([main.go](../../../controlplane/main.go#L374), [.env.example](../../../.env.example#L64), [README.md](../../../README.md#L315)) | CONTRADICTED where presented as default; explicit `--port 8080` is not a contradiction |
| API docs name `DASHBOARD_ORIGIN` and `CONTROL_PLANE_TOKEN` for admin auth. | Code configures CORS from `CORS_ORIGIN`, and outer admin middleware requires `ADMIN_TOKEN`; `CONTROL_PLANE_TOKEN` is only an inner legacy/event token. A dashboard-origin-only preflight did not set ACAO. ([api-gateways.md](../../api-gateways.md#L3), [api-gateways.md](../../api-gateways.md#L326), [auth.go](../../../controlplane/auth.go#L45), [double-auth-cors.txt](raw/double-auth-cors.txt)) | CONTRADICTED |
| Events docs imply the events token or subnet fallback independently authenticates gateway notifications. | The outer admin middleware runs first; X-Events-Token-only returned 401 with `ADMIN_TOKEN` configured. ([api-gateways.md](../../api-gateways.md#L154), [server.go](../../../controlplane/server.go#L85), [double-auth-cases.txt](raw/double-auth-cases.txt)) | CONTRADICTED |
| `.env.example` gives `PROVISION_TIMEOUT_SECONDS=600` and later `=180`. | Runtime default and mock configuration are 180 seconds. ([.env.example](../../../.env.example#L45), [.env.example](../../../.env.example#L91), [openstack.go](../../../controlplane/openstack.go#L196)) | CONTRADICTED / internally inconsistent |
| README’s `team1.local` examples describe the default hostname. | Runtime default routing domain is `cstam.felcloud.tn`; `.local` requires an explicit override that the examples do not show. ([README.md](../../../README.md#L531), [main.go](../../../controlplane/main.go#L185)) | CONTRADICTED as a default |
| API docs say rollback failure responds 500. | Canonical route returns 502 for rollback errors. ([api-gateways.md](../../api-gateways.md#L247), [server.go](../../../controlplane/server.go#L395)) | CONTRADICTED |
| “Failover is a stub” / “seven checks versus six in output.” | No “stub” assertion was found in the named docs; rollback/failover handlers are implemented. README says seven local checks and `run-all-tests.sh` reports 7/7. The separate live failover is documented as requiring staging. ([README.md](../../../README.md#L454), [run-all-tests.txt](raw/run-all-tests.txt), [TEST_PLAN.md](../../../TEST_PLAN.md#L4)) | CODE-ONLY for the absent stub claim; VERIFIED-TEST for the 7/7 runner count |
| TEST_PLAN calls remote rollback verification deferred. | The code has rollback implementation and fake/unit tests, while actual remote checksum/live rollback validation was not done. ([TEST_PLAN.md](../../../TEST_PLAN.md#L10), [gateway_deploy.go](../../../controlplane/gateway_deploy.go#L305)) | Not contradictory: this is a live-test deferral, not a claim of missing code. |

## Live-readiness checklist

Do not run these steps until the initialization, health-probe reachability, HAProxy config, DB-path, and operation-lock findings are resolved or explicitly accepted.

| Order | Step and variables/prerequisites | Exact risk carried |
|---:|---|---|
| 1 | Use isolated staging project/network and service accounts. Inventory Nova/Neutron quotas, sandbox CIDR, `ControlPlaneCIDR`, `MgmtCIDR`, gateway addresses, and SG attachment. | A Pulumi change can expose management services, disrupt existing SGs, consume quota, or alter routable networks. |
| 2 | Back up the SQLite DB and select one absolute `IPAM_DB_PATH`; set `ADMIN_TOKEN` (≥24 chars), `AUTH_DISABLED=false`, `LISTEN_ADDR`, `PORT`/`serve --port`, `RATE_LIMIT_PER_MIN`, `ROUTING_DOMAIN`, `IP_QUARANTINE_SECONDS`, and `PROVISION_TIMEOUT_SECONDS`. | DB migration/path changes can split or strand leases, events, and generation history; wrong bind/port/origin can expose or disable the API. |
| 3 | Set OpenStack credentials (`OS_AUTH_URL`, username/password or application credential, project/domain/region), `SANDBOX_NETWORK_ID` or `SANDBOX_NETWORK_NAME`, `SANDBOX_SUBNET_ID`, `SANDBOX_SG_NAME`, image/flavor/keypair and `PROVISIONER=openstack`. | Provisioning creates billable Nova servers and Neutron ports at selected fixed IPs; the current allocator does not reserve against Neutron first. |
| 4 | Choose a reachable `HEALTH_PROBE_MODE`; for `gateway-ssh`, configure `ANSIBLE_INVENTORY`, SSH key, `HEALTH_PROBE_SSH_HOST`/`HEALTH_PROBE_SSH_JUMP` as needed. Configure `BASTION_IP`, `ANSIBLE_VAULT_PASS`, `HEALTH_PROBE_TIMEOUT_SECONDS`, and `HEALTH_REGRESSION_WINDOW_SECONDS`. | A wrong path makes all real provisioning fail health checks; SSH settings enable remote execution from the control plane. |
| 5 | Confirm target HAProxy configuration externally, set `HAPROXY_TRANSACTION_PLAYBOOK`, gateway inventory, vault, and `FAILOVER_PROBE_URL`/optional `FAILOVER_PROBE_HOST`. First run only validation/dry-run, then one canary sandbox. | A sync changes HAProxy on both gateways; the missing health frontend can affect Keepalived checks; auto-rollback may take ≥45 seconds plus deployments. |
| 6 | Only after config checks pass, test one controlled failover with `FAILOVER_THRESHOLD_MS` and API `{"confirm":true}`; monitor gateway logs and traffic. | The API stops Keepalived on the current VIP holder; a failure to restore it or an incorrect probe can cause customer-visible outage. |
| 7 | Test teardown only against a newly created control-plane-owned sandbox; observe quarantine and reclaim. | Teardown deletes Nova/Neutron resources; ownership guard must never be bypassed for externally managed sandboxes. |
| 8 | Only then run concurrency/routing tests and record cloud, image, flavor, test date, gateway identity, and live verifier evidence. | Bulk creation can exhaust quota/address space and route many tenants through an unverified configuration. |

`PROVISIONER=mock` and `HEALTH_PROBE_MODE=skip` are local-only; they are not live readiness evidence. OpenStack values and script defaults are documented in [.env.example](../../../.env.example#L1) and [openstack.go](../../../controlplane/openstack.go#L228).

## Rubric scoring estimate

“Claimed” below is the full rubric maximum, not a claim that the project earns it. Defensible points credit tested local behavior conservatively and give no live-performance points without a live run.

| Requirement | Claimed | Defensible today | Reason for gap |
|---|---:|---:|---|
| 1.1 Provisioning | 15 | 5 | Mock/unit workflow passes, but actual `serve` setup is broken and no Nova/Neutron run occurred. |
| 1.2 Dual HA gateways | 15 | 3 | Config/fake tests exist; generated health endpoint is missing and no live two-gateway operation was verified. |
| 1.3 IPAM and reclamation | 10 | 8 | Local DB tests and mock recycle pass; cloud address collision and live reclaim are unverified. |
| 1.4 Zero-downtime reload and rollback | 10 | 2 | Fake deployment tests only; manual rollback can select bad config and default health window violates sub-second rollback target. |
| 2.1 Concurrent 10+ team stress | 15 | 8 | 12/12 mock provisions with unique IPs, but no routed VIP proof; stress free-after-120s check failed. |
| 2.2 Teardown and lifecycle | 10 | 8 | Unit/mock teardown and guard pass; stress reclaim timing assertion failed and no real resource deletion was tested. |
| 2.3 Isolation and API security | 5 | 3 | Auth/rate/CORS mock checks pass; SG rules are source-only and event-token layering is broken. |
| Phase 3 dashboard | 10 | 0 | No dashboard/frontend source found. Backend endpoints alone do not satisfy UI deliverable. |
| Phase 4 pitching | 10 | 0 | README and technical docs exist, but no pitch deck/demo video was found. |
| **Base total** | **100** | **37** | **Defensible local evidence only; no live-verification points.** |

| Bonus | Maximum | Defensible | Evidence/gap |
|---|---:|---:|---|
| SSL/TLS auto-issuance | 2 | 0 | HAProxy TLS/self-signed certificate setup exists, but no ACME/Let's Encrypt renewal. |
| Rate limiting and gateway-level per-team bandwidth throttling | 2 | 1 | API per-client limiter is tested; no HAProxy per-team bandwidth throttle identified. |
| Custom domain binding | 2 | 0 | Routing domain can be configured, but DNS/custom-domain binding flow is not implemented/verified. |
| Smart health monitoring/maintenance page | 2 | 1 | Health probes and maintenance backend exist in source; health frontend omission and no live check prevent full credit. |
| OpenStack-native (Heat/Designate/eBPF) | 2 | 1 | Pulumi OpenStack resources are present; no Heat/Designate/eBPF flow verified. |
| **Bonus total** | **10** | **3** | |

## Deliverables inventory

| Deliverable | Present? | Evidence |
|---|---|---|
| Source repository | Yes | Go control plane, Ansible and Pulumi source exist. |
| Architecture/data-flow diagram | Partial/Yes | README includes architecture diagrams; there is no separate current data-flow artifact. |
| Benchmark report | Yes, but not live benchmark evidence | Report explicitly says MOCK DATA - NOT A BENCHMARK; all live metrics are NOT MEASURED ([BENCHMARK_REPORT.md](../../benchmarks/BENCHMARK_REPORT.md), [report.json](../../benchmarks/report.json)). Audit stress results are also MOCK ([stress-test.txt](raw/stress-test.txt)). |
| API/security documentation | Partial | [api-gateways.md](../../api-gateways.md), but token/CORS/body-limit documentation disagrees with runtime. |
| Technical report | Partial | README and test plan exist; this audit report is new evidence, not a product technical report. |
| Demo video | No file found | No video artifact in repository inventory. |
| Dashboard source | No | No frontend/dashboard project or page was found. |

## Top 10 next actions, ordered by estimated rubric points/hour

Estimates are prioritization estimates, not measured outcomes.

| Rank | Action | Estimated points | Estimated effort | Points/hour |
|---:|---|---:|---:|---:|
| 1 | Configure the provisioner in the actual `serve` startup path; add a regression test that uses unmodified `serve` in mock mode. | 10 | 1 h | 10.0 |
| 2 | Make real health probing reachable by default and prove it from a staging control-plane host. | 15 | 2 h | 7.5 |
| 3 | Generate and validate the loopback HAProxy health frontend required by Keepalived. | 10 | 2 h | 5.0 |
| 4 | Unify IPAM, generation, and event DB path selection; test env and no-env launch locations. | 6 | 1.5 h | 4.0 |
| 5 | Correct rollback active-generation selection; test repeated rollback and recovery from a bad generation. | 8 | 2.5 h | 3.2 |
| 6 | Serialize provision, teardown, CLI and HTTP gateway operations through one coordinator. | 5 | 2 h | 2.5 |
| 7 | Keep PROVISIONING sandboxes out of active public routes and test concurrent sync behavior. | 4 | 2 h | 2.0 |
| 8 | Add strict decoding/body caps to canonical gateway mutation routes; test valid oversized JSON. | 3 | 1.5 h | 2.0 |
| 9 | Align event/API/CORS token configuration and docs; add middleware integration tests for gateway notification callers. | 3 | 2 h | 1.5 |
| 10 | Run gated live staging tests and publish non-mock route, failover, and provisioning evidence. | 15 | 12 h | 1.25 |

## Evidence index and final state

- Baseline/tool versions: [phase0.txt](raw/phase0.txt)
- Static checks: [phase1.txt](raw/phase1.txt)
- Go test list and commands: [phase2.txt](raw/phase2.txt)
- Full Go JSON output and inventory: [gotest.json](raw/gotest.json), [go-test-inventory.md](raw/go-test-inventory.md)
- Mock smoke/auth/rate limit: [mock-smoke.txt](raw/mock-smoke.txt), [rate-cors-auth.txt](raw/rate-cors-auth.txt), [double-auth-cases.txt](raw/double-auth-cases.txt), [double-auth-cors.txt](raw/double-auth-cors.txt)
- Test scripts: [recycle-test.txt](raw/recycle-test.txt), [run-all-tests.txt](raw/run-all-tests.txt), [stress-test.txt](raw/stress-test.txt)
- Defect reproductions: [phase4-repro.txt](raw/phase4-repro.txt), [teardown-guard.txt](raw/teardown-guard.txt), [unpatched-serve-provision.txt](raw/unpatched-serve-provision.txt), [gateway-mutations-validation.txt](raw/gateway-mutations-validation.txt)
- Line-numbered source evidence: [source-excerpts.txt](raw/source-excerpts.txt)
- Initial Git log: [gitlog.txt](gitlog.txt)

The final status command is saved in [final-status.txt](raw/final-status.txt). No tracked source file was edited by this audit.
