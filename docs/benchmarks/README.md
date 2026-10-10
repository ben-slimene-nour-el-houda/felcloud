# Provisioning stress benchmarks

Run `scripts/stress-test.sh` with a reachable control-plane API and an `ADMIN_TOKEN`
of at least 24 characters. The harness checks `/health` and authenticated
`/api/sandboxes` before starting, then stores `results.json`, `results.csv`, and
`summary.md` under `docs/benchmarks/stress-<UTC timestamp>/` by default.

## Local mock run

From `controlplane/`, start the API with a disposable DB and no OpenStack
credentials:

```sh
ADMIN_TOKEN='local-stress-token-at-least-24-chars' \
RATE_LIMIT_PER_MIN=1000 \
IPAM_DB_PATH=/tmp/stress.db \
IP_QUARANTINE_SECONDS=3 \
PROVISIONER=mock \
go run . serve --port 8080
```

In another terminal from the repository root:

```sh
ADMIN_TOKEN='local-stress-token-at-least-24-chars' \
RATE_LIMIT_PER_MIN=1000 \
TEAMS=12 \
PROVISIONER=mock \
scripts/stress-test.sh
```

Example successful mock output:

```text
Preflight PASS: /health and authenticated /api/sandboxes at http://127.0.0.1:8080
Starting 12 simultaneous provisions (concurrency=12)...
PASS: success count: 12/12; failures by step/status: none
PASS: unique valid sandbox IPs: 12/12 unique; subnet=10.20.30.0/24; excluded addresses checked
PASS: unique server_id and port_id values: servers=12/12, ports=12/12
PASS: one allocated lease per successful team: lease endpoint HTTP 200; checked 12 successful teams and duplicate allocated IPs
SKIPPED: successful hostnames respond via VIP/PUBLIC IP: set VIP_IP or PUBLIC_IP to enable routed HTTP verification
PASS: no sandbox remains PROVISIONING: endpoint HTTP 200; stuck=[]
PASS: gateway sync events observed: 9 SYNC events versus 12 successful provisions (HTTP 200)
Starting parallel teardown of 12 successful provisions...
PASS: teardown requests succeeded: 12/12 returned 200
PASS: leases enter QUARANTINED: endpoint HTTP 200; verified 12 leases
PASS: quarantined leases become FREE: completed within 120 seconds=True
PASS: torn-down sandboxes are RELEASED: all 12 states RELEASED=True
SKIPPED: OpenStack server/port cleanup: mock mode does not query OpenStack
PASS: success rate threshold: 100.0% >= 100.0%
Results: docs/benchmarks/stress-20261010T112958Z/results.json, results.csv, summary.md
```

Mock mode skips backend health probes and gateway changes; its provisioning,
resource IDs, timings, and sync results are simulated. **Mock results are NOT
live benchmarks** and must not be compared to OpenStack provisioning latency.
Do not configure `HEALTH_PROBE_MODE=skip` with a real provisioner; startup
rejects that combination.

## Live benchmark

Live runs require the regular OpenStack provisioner configuration, valid API
credentials, a deployed routing domain, and an explicit reachable `VIP_IP` or
`PUBLIC_IP` to enable hostname routing verification. Set `RATE_LIMIT_PER_MIN`
on the server high enough for at least `TEAMS` calls, or twice `TEAMS` when
teardown is enabled. Configure `SANDBOX_CIDR`, `GATEWAY_IPS`, `BASTION_IP`,
`VIP_IP`, and `PUBLIC_IP` when the environment differs from defaults.

The harness uses unique run-specific names `<PREFIX>-<n>-<random4>`, never
retries failed requests, and prints DELETE cleanup commands only for those
generated names. Review failures and generated `summary.md` before rerunning;
OpenStack server/port list checks run only when the harness is in live mode
and both the `openstack` CLI and `OS_CLOUD` are available.

## Inputs

`API_URL` (default `http://127.0.0.1:8080`), `ADMIN_TOKEN` (required), `TEAMS`
(default 12, minimum 10), `PREFIX` (default `stress`), `CONCURRENCY` (default
`TEAMS`), `HTTP_TIMEOUT_SECONDS` (default 300), `TEARDOWN` (default `true`),
`OUT_DIR`, and `SUCCESS_THRESHOLD` (default 100 percent) control the run.
