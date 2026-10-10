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

## IP recycling benchmark

Run `scripts/recycle-benchmark.sh` against an already-running control plane.
It requires `API_URL` and an `ADMIN_TOKEN` of at least 24 non-whitespace
characters, and validates `PREFIX` as a lowercase DNS label (maximum 63
characters). `RECYCLE_CYCLES` defaults to 10 (range 1-1000),
`IP_QUARANTINE_SECONDS` defaults to 60 (range 1-3600), and
`HTTP_TIMEOUT_SECONDS` defaults to 300. `OUT_DIR` overrides the default
`docs/benchmarks/recycle-<UTC timestamp>/`. The script does not start or stop
the server. It polls lease status every 250 ms, with a 120-second maximum
wait. Set `PROVISIONER=mock` in the runner environment for an explicit mock
label; mock-prefixed resource IDs also force the output mode to mock.

Each cycle provisions and deletes one sandbox, checks its quarantine/free
transitions, provisions a second sandbox while the first IP is quarantined,
then asks IPAM to allocate the original preferred IP after it becomes free.
It releases the final allocation and verifies all run-owned sandbox rows are
RELEASED with no ALLOCATED lease remaining. Cleanup leases may remain
QUARANTINED and return to FREE through the normal cooldown. Results include
the measured durations, effective cooldown, assertions, and cleanup commands
on failure.
The runner derives the actual cooldown from `quarantined_at` and
`quarantine_until` in the lease API when available; otherwise it records the
runner's `IP_QUARANTINE_SECONDS` as a fallback and labels the source. The
observed time to FREE is checked against the remaining time in that lease
window. Never point this script at production/live OpenStack unless that
environment is intentionally reserved for destructive benchmarking.

## Combined benchmark report

Run `python3 scripts/benchmark-report.py` to use the latest `stress-*/results.json`
and `recycle-*/results.json` under this directory and optional
`FAILOVER_RESULTS=/path/to/results.json`. Specify `--stress PATH|latest` or
`--recycle PATH|latest` to choose inputs; `--check` validates the selected
schemas and reports missing datasets. `--failover PATH` overrides
`FAILOVER_RESULTS`. Reports are written to `BENCHMARK_REPORT.md` and
`report.json` here. Missing sections say **NOT MEASURED**.

Mock stress/recycle input is rejected by default. For a dry-run rendering set
`ALLOW_MOCK=true`; it displays a **MOCK DATA - NOT A BENCHMARK** banner, excludes
mock measurements from benchmark tables, and labels every table it does render.
Without that override, a mock input is an error. This is not a way to turn mock
measurements into benchmark evidence.

Example dry run using the recorded Task 7 mock result (with no recycle or
failover measurements supplied):

```sh
ALLOW_MOCK=true python3 scripts/benchmark-report.py \
  --stress docs/benchmarks/stress-20261010T112958Z/results.json \
  --recycle latest
```

Example report output:

```text
# FelCloud Benchmark Report

# MOCK DATA - NOT A BENCHMARK

Mock input measurements are excluded from benchmark tables. ALLOW_MOCK=true permits this labelled dry-run report only.

## Environment
- Input modes: MOCK DATA - NOT A BENCHMARK

## Provisioning latency (ms)
MOCK DATA - NOT A BENCHMARK
NOT MEASURED — mock data suppressed.

## Load test
MOCK DATA - NOT A BENCHMARK
NOT MEASURED — mock data suppressed.

## Gateway failover
NOT MEASURED — no failover results input.
```

The report accepts stress results with `mode`, `date_utc`, `git_commit`,
`config`, `teams`, and `aggregates`; recycle results with `schema_version`,
`mode`, `date_utc`, `git_commit`, `config`, and `cycles`. Timing samples are
read from successful stress team rows and the corresponding timing fields in
each recycle cycle. Non-negative numeric values are required.

### Failover input schema

Provide failover results in `FAILOVER_RESULTS` (or `--failover`) as JSON:
the numeric values below are illustrative schema placeholders, not measured
benchmark data.

```json
{
  "runs": [
    {
      "mode": "haproxy-only",
      "failover_ms": 120,
      "failed_requests": 2,
      "total_requests": 100,
      "serving_gateway_before": "gw1",
      "serving_gateway_after": "gw2",
      "verified_by": "responding-gateway-header"
    }
  ],
  "date": "2026-10-10T12:00:00Z",
  "commit": "0123456789abcdef"
}
```

Allowed modes are `haproxy-only`, `keepalived`, and `gateway-reboot`.
`verified_by` must be `responding-gateway-header` or `vip-owner`; the before
and after gateway values must differ. A failover input must come from an
actual run that verifies which gateway responded (for example, by reading a
response header or checking the VIP owner). Merely printing a constant gateway
name is not verification and must not be recorded in this schema.

Percentiles use nearest-rank. Every latency statistic carries its sample size;
n < 10 emits a warning. p95 is omitted for n < 20, where the report shows the
maximum instead. The environment section reads cloud/flavor/image and server
settings from the report-generation environment when available; absent values
remain **NOT MEASURED**.

Run the report-generator unit tests from the repository root with
`python -m unittest`; they use inline fixture JSON and do not contact a server.
