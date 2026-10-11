# FelCloud final handoff

## 1) Task status summary

| Task | Status | Bug IDs / scope | Evidence |
|---|---|---|---|
| Y1 | DONE-TESTED | D12, D14, D5 | `TestGatewayStoreUsesIPAMDatabase`, `TestRunServerRejectsInvalidSecurityAtStartup`, `TestServeConfiguresMockProvisioner`, `TestReaperRunsInServer`, `TestServerHealthIncludesReaperMetrics` |
| Y2 | DONE-TESTED | auth, CORS, strict JSON | `TestAPIBearerAuthenticationAndOpenHealth`, `TestAPIStrictJSONRejectsOversizedAndUnknownFields`, `TestAPIRateLimitAndCORSPreflight`, `TestAuthMatrixAndCORS` |
| Y3 | DONE-TESTED | D15, D1, D16, D4 | `TestGatewaySyncDoesNotModifyTrackedVarsFile`, `TestGatewayConfigForHostAddsGatewayIdentityToPublicFrontends`, `TestParseBackendStatesKeepsGatewayIdentity`, `TestHealthWindowDetectsBackendBecomingDown`, `TestValidateHAProxySyntaxRejectsMissingFrontendAndBackend` |
| Y3b | DONE-TESTED | D10, D8, D21, D3, D17 | `TestGatewayOpsOnlyAllowsOneActiveOperation`, `TestGatewayOpsLeaseExpiresForNewHolder`, `TestWaitGatewayOpTimesOutWhenBusy`, `TestGatewayFailoverStopsRestartsAndRecordsRealTransition`, `TestGatewayFailoverTimeoutRestartsAndReturnsReason`, `TestGatewayFailoverConcurrentRequestGetsConflict`, `TestGatewayFailoverRestartsAfterStopCommandError` |
| Y4 | NOT-STARTED | N1, N3 status unknown | No matching test names or branch evidence in the repo snapshot |
| Y5 | DONE-TESTED | D7, D22 | `TestGatewayFailoverStopsRestartsAndRecordsRealTransition`, `TestGatewayFailoverTimeoutRestartsAndReturnsReason`, `TestGatewayFailoverConcurrentRequestGetsConflict`, `TestGatewayFailoverRestartsAfterStopCommandError` |
| Y6 | NOT-STARTED | not present in branch | No matching commit or test evidence |
| Y7 | NOT-STARTED | not present in branch | No matching commit or test evidence |

## 2) Audit findings from Step 0

### Git state
- Branch: `y3b-deploy-engine`
- Remote tracking: `origin/y3b-deploy-engine`
- Relevant task-branch history present: Y1, Y3, Y3b, Y5 have code and commits in this branch history.
- Y4/Y6/Y7 are not represented as committed task work in the local branch history we inspected.

### Go audit result
The repo was audited with this command:

```bash
cd /home/yassmine/felcloud_final/dev/felcloud/controlplane
go vet ./... && gofmt -l . && go test ./... -race -count=1
```

Observed failure summary:
- `TestGatewayFailoverStopsRestartsAndRecordsRealTransition` failed
- `TestGatewayFailoverRestartsAfterStopCommandError` failed
- `TestServeConfiguresMockProvisioner` failed
- `WARNING: DATA RACE` reported in `gateway_failover_test.go`
- overall result: `FAIL` with exit code 1

The audit does not mark these as passing and does not treat them as live-verified.

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
- `GATEWAY_DEPLOY_TIMEOUT_SECONDS` — expected in deploy timeout logic, default `120` if implemented in the target branch
- `ANSIBLE_KNOWN_HOSTS` — expected default path `../ansible/known_hosts` for SSH trust policy
- `ANSIBLE_INVENTORY` — default `../ansible/inventory/hosts.yml`
- `ANSIBLE_VAULT_PASS` — default `../ansible/.vault_pass`
- `BASTION_IP` — default `203.0.113.119`

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
| N1 | NO | gateway / provisioning / teardown regression path | Y4 (not present in repo) |
| N2 | NO | live Keepalived and gateway health check on real nodes | Y3 + Y5 partial |
| N3 | NO | resource teardown / ownership / drift scenarios | Y4 (not present in repo) |
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

## 8) Known gaps and open decisions

- Go tests are not all green in the current branch: the failover and one provisioner test still fail under the race run.
- Shellcheck is unavailable in this environment; it is not silently treated as passed.
- Y4/Y6/Y7 are explicitly not evidenced by matching commits or test names in this repo snapshot and remain not-started from the audit.
- The repo bundle is the handoff artifact; a live PR was never created here, and no remote push was possible.
- More deploy-engine and failover work remains in the branch, but it is not claimed as live-verified.

## 9) Deliverables produced

- Git bundle path: `../felcloud-handoff.bundle`
- Patch export folder: `../felcloud-patches/`
- Handoff document: `HANDOFF.md`
- Repository status: `.dashboard_status` is ignored via `.gitignore` and is not part of the commit set
