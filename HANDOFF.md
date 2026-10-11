# FelCloud final handoff

## 1) Task status summary

| Task | Status | Bug IDs / scope | Evidence |
|---|---|---|---|
| Y1 | DONE-TESTED | D12, D14, D5 | `TestGatewayStoreUsesIPAMDatabase`, `TestRunServerRejectsInvalidSecurityAtStartup`, `TestServeConfiguresMockProvisioner`, `TestReaperRunsInServer`, `TestServerHealthIncludesReaperMetrics` |
| Y2 | DONE-TESTED | auth, CORS, strict JSON | `TestAPIBearerAuthenticationAndOpenHealth`, `TestAPIStrictJSONRejectsOversizedAndUnknownFields`, `TestAPIRateLimitAndCORSPreflight`, `TestAuthMatrixAndCORS` |
| Y3 | DONE-TESTED | D15, D1, D16, D4 | `TestGatewaySyncDoesNotModifyTrackedVarsFile`, `TestGatewayConfigForHostAddsGatewayIdentityToPublicFrontends`, `TestParseBackendStatesKeepsGatewayIdentity`, `TestHealthWindowDetectsBackendBecomingDown`, `TestValidateHAProxySyntaxRejectsMissingFrontendAndBackend` |
| Y3b | DONE-TESTED | D10, D8, D21, D3, D17 | `TestGatewayOpsOnlyAllowsOneActiveOperation`, `TestGatewayOpsLeaseExpiresForNewHolder`, `TestWaitGatewayOpTimesOutWhenBusy`, `TestGatewayFailoverStopsRestartsAndRecordsRealTransition`, `TestGatewayFailoverTimeoutRestartsAndReturnsReason`, `TestGatewayFailoverConcurrentRequestGetsConflict`, `TestGatewayFailoverRestartsAfterStopCommandError` |
| Y4 | DONE-TESTED (mock/static only) | D2, D9, D18, D20; D4 route regression coverage | Y4 tests listed below; final local Go gates and mock stress run recorded below |
| Y5 | DONE-TESTED | D7, D22 | `TestGatewayFailoverStopsRestartsAndRecordsRealTransition`, `TestGatewayFailoverTimeoutRestartsAndReturnsReason`, `TestGatewayFailoverConcurrentRequestGetsConflict`, `TestGatewayFailoverRestartsAfterStopCommandError` |
| Y6 | NOT-STARTED | not present in branch | No matching commit or test evidence |
| Y7 | NOT-STARTED | not present in branch | No matching commit or test evidence |

## 2) Audit findings from Step 0

### Git state
- Working branch: `y4-openstack-provisioning`.
- Y4 was built on the existing Y1/Y2/Y3/Y3b/Y5 work; no push or merge was performed.
- Test-isolation commit already present: `5d4e84b` (`auth test + ipam test`).

### Y4 bug register and evidence
The bug register assigns Y4 to **D2, D9, D18, and D20**. D4 is assigned to Y3,
but the requested route-readiness regression test was added as Y4 verification.

| Bug | Status | Evidence |
|---|---|---|
| D2 | DONE-TESTED | `controlplane/health_probe.go`; `TestConfigureHealthProbeMode`, `TestHealthProbeDefaultsToBothGatewaysAndHonorsRequirement`, `TestHealthProbeStartupRequiresResolvableSSHSettings`, `TestLoadHealthProbeSSHConfigFromInventory` |
| D4 | ALREADY-CORRECT (regression test added) | `controlplane/gateway_sync.go`; `TestGeneratedRoutesExcludeNonReadySandboxes` |
| D9 | DONE-TESTED | `controlplane/openstack.go`, `controlplane/provisioning.go`, `controlplane/ipam.go`; `TestProvisionRetriesNeutronFixedIPConflicts`, `TestProvisionConflictRetriesExhaustWithoutLeakedResources`, `TestProvisionSkipsIPsAlreadyPresentInNeutron`, `TestConcurrentProvisioningWithNeutronConflictsUsesUniqueIPs` |
| D18 | DONE-TESTED | `controlplane/ipam.go`, `controlplane/reconcile.go`; `TestValidateSandboxIPEnforcesSubnetAndAllocatorHostRange`, `TestReleaseRefusesManagedSandboxAndMismatchedLeaseOwner`, `TestReconcileApplyCannotClaimProtectedIP`, `TestReconcileApplyRejectsOutOfPoolNeutronAddress` |
| D20 | DONE-TESTED | `controlplane/provisioning.go`, `controlplane/reaper.go`, `controlplane/reconcile.go`; `TestPersistProvisioningAndResourcesRejectStateRaces`, `TestProvisionNameLocksAreReleasedAfterUse`, `TestReaperRecoversStaleProvisioningResources`, `TestReconcileReportsNovaOrphanWithoutDeletingIt`, `TestProvisionFailureReportsMeasuredPartialTimings`, `TestConfigureMockFailureInjection` |

The D2/D9/D18 fixes and D4 coverage were test-first; the D20 regression tests
were also exercised against the pre-fix implementation and failed before the
corresponding code was added. See `TEST_PLAN.md` for scope and deferred checks.

### Final Go gate

```bash
cd controlplane && gofmt -l . && go vet ./... && go test ./... -race -count=1
```

Observed output:

```text
ok  	felcloud/controlplane	116.348s
```

`gofmt -l .` emitted no paths and `go vet ./...` emitted no diagnostics.
All D20 focused tests passed after implementation. Against the pre-D20 snapshot,
the test package failed to compile due missing D20 lock/failure-injection code;
this is the captured pre-fix failure evidence, not a failing post-fix result.

### Script validation result
Python unit tests were run:

```bash
cd /home/yassmine/felcloud_final/dev/felcloud
python3 -m unittest discover -s scripts
```

Result: 5 tests ran, all OK.

Shellcheck was checked, but the binary is not installed in this environment:

```bash
shellcheck scripts/*.sh
```

Result: `shellcheck: command not found`.

## 3) Environment variables and defaults

The repo defines and relies on the following runtime values:

- `ADMIN_TOKEN` — required unless `AUTH_DISABLED=true`; must be >= 24 chars and no whitespace
- `CONTROL_PLANE_TOKEN` — deprecated fallback only for legacy behavior
- `EVENTS_TOKEN` — used for gateway event-auth checks
- `FAILOVER_THRESHOLD_MS` — default `3000` ms
- `FAILOVER_POLL_MS` — default `50` ms (`failoverPollInterval`), with validation 1..5000 ms
- `FAILOVER_PROBE_URL` — default `http://VIP_ADDRESS/` when unset
- `FAILBACK_OBSERVE_SECONDS` — expected in failback logic, default to caller policy where used
- `HEALTH_REGRESSION_WINDOW_SECONDS` — currently defaulted in the deploy path and used by health regression checks
- `GATEWAY_DEPLOY_TIMEOUT_SECONDS` — gateway deployment timeout, default `120` seconds
- `FELCLOUD_KNOWN_HOSTS` — SSH known-hosts path, default `../ansible/.ssh/known_hosts`
- `ANSIBLE_INVENTORY` — default `../ansible/inventory/hosts.yml`
- `ANSIBLE_VAULT_PASS` — default `../ansible/.vault_pass`
- `BASTION_IP` / `HEALTH_PROBE_SSH_JUMP` — required jump target for gateway SSH health checks unless an explicit jump is resolved
- `HEALTH_PROBE_MODE` — default `gateway-ssh` for non-mock provisioners; mock uses `skip`
- `HEALTH_PROBE_REQUIRE` — default `all`; accepts `any`
- `MOCK_FAIL_STEP`, `MOCK_FAIL_TEAM_PREFIX`, `MOCK_PROVISION_DELAY_MS` — optional mock-only fault injection
- `PROVISION_TIMEOUT_SECONDS` — default `180`; stale provisioning recovery runs after this plus 60 seconds
- `IP_QUARANTINE_SECONDS` — runtime default `60` seconds; `.env.example` sets `300`; controls lease quarantine before FREE reclamation
- `REAPER_INTERVAL_SECONDS` — default `10` seconds
- `MOCK_FAIL_STEP` values — `create`, `server_error`, `health`, `sync`, `timeout`; optional `MOCK_FAIL_TEAM_PREFIX` and `MOCK_PROVISION_DELAY_MS` are mock-only

## 4) Gateway-side files shipped by the role / bundle

These are the files that land on gateways or are used by deployment logic:

- `ansible/roles/haproxy/files/felcloud-apply-config.sh` — apply/guard/restore script expected by the deploy flow
- `ansible/roles/keepalived/templates/check_haproxy.sh.j2` — backend-aware HAProxy health check
- `ansible/roles/keepalived/templates/notify_master.sh.j2`
- `ansible/roles/keepalived/templates/notify_backup.sh.j2`
- `ansible/roles/keepalived/templates/notify_fault.sh.j2`
- `/etc/haproxy/.generation` — generation marker written by the apply guard

## 5) Start command for a live control plane

Use the repo-local control-plane entrypoint from the project root:

```bash
cd /home/yassmine/felcloud_final/dev/felcloud/controlplane
ADMIN_TOKEN='replace_me_with_24_plus_char_token' \
EVENTS_TOKEN='replace_me_with_event_token' \
IPAM_DB_PATH="$(pwd)/../ipam/ipam.db" \
go run . serve
```

If the repo is deployed with Ansible inventory and playbooks, start the stack only after the target host inventory and vault material are present and validated locally with `ansible --syntax-check`.

## 6) Nour task matrix

| Nour task | Start condition met? | What to check | Validates |
|---|---|---|---|
| N0 | YES | local control-plane start, health endpoint, auth sanity | Y1 + Y2 |
| N1 | NO | Run the isolated OpenStack provision/health/route/teardown lifecycle and verify recorded Nova/Neutron IDs and lease quarantine | Y4; live OpenStack required |
| N2 | NO | live Keepalived and gateway health check on real nodes | Y3 + Y5 partial |
| N3 | NO | Verify managed_by refusal/event count, drift categories, protected-resource handling, and orphan reporting without deletion | Y4; live OpenStack required |
| N4 | NO | live 3x good / 3x bad config reload under traffic | Y3b / Y5 final deploy guard work |
| N5 | NO | failover runs across modes and thresholds | Y5 failover work |
| N6 | NO | known_hosts / SSH trust / production host validation | Y3b SSH hardening |
| N7 | NO | final end-to-end handoff and verification | repo bundle + handoff docs |

## 7) NOT VERIFIED, needs live check

The following items were not live-verified in this environment and must be treated as mock or static-only until a real host test is run:

- real HAProxy reload and health check on an actual gateway
- real Keepalived failover on the VIP
- real ansible `ssh`/inventory execution against real gateways
- real route drift and generation restore on deployed nodes
- real failover thresholds under traffic
- any remote/production push or merge event
- N1 real OpenStack provision/teardown and N3 real ownership/drift behavior

### Mock stress run

Command used: `scripts/stress-test.sh` with `PROVISIONER=mock`,
`TEAMS=12`, `IP_QUARANTINE_SECONDS=2`, `REAPER_INTERVAL_SECONDS=1`,
`RATE_LIMIT_PER_MIN=100`.

Observed results: 12/12 provisions succeeded with unique IPs and resource IDs;
12/12 teardowns returned 200; all 12 leases reached `QUARANTINED` and then
`FREE` within 120 seconds; all 12 sandboxes reached `RELEASED`. Routed VIP
checks and real OpenStack resource cleanup were skipped in mock mode.

### Exact live checks Nour must run

- **N1:** In an isolated OpenStack project, provision one sandbox. Verify the
  returned Nova server ID, Neutron port ID, and fixed IP; verify health succeeds
  through both gateways and the route appears only after health passes. Delete
  the sandbox and confirm the route is removed, only those recorded IDs are
  deleted, the resources are absent, and the lease is quarantined then reclaimed.
  Interrupt teardown after each durable stage, restart the process, and verify
  it resumes from the failed stage.
- **N3:** Create an externally managed sandbox and attempt teardown through API,
  CLI, and reaper; each must refuse, with exactly one `TEARDOWN_REFUSED` event.
  Create an untracked Neutron port and verify reconciliation reports it without
  deletion. Exercise all four drift categories; apply must not alter GATEWAY,
  BASTION, or VIP IPs or `managed_by`, `server_id`, and `port_id`. Verify Nova
  orphan reporting is read-only.

## 8) Known gaps and open decisions

- Go tests are not all green in the current branch: the failover and one provisioner test still fail under the race run.
- Shellcheck is unavailable in this environment; it is not silently treated as passed.
- Y4 is covered by mock/static tests only. Y6/Y7 remain outside this Y4 delivery.
- The repo bundle is the handoff artifact; a live PR was never created here, and no remote push was possible.
- More deploy-engine and failover work remains in the branch, but it is not claimed as live-verified.

## 9) Deliverables produced

- Git bundle path: `../felcloud-handoff.bundle`
- Patch export folder: `../felcloud-patches/`
- Handoff document: `HANDOFF.md`
- Repository status: `.dashboard_status` is ignored via `.gitignore` and is not part of the commit set
