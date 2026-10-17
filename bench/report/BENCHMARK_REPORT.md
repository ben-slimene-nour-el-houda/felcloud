# FelCloud Benchmark Report

## 1. Objective and scope

Assess the CSTAM 3.0 resilient IP optimizer using measured evidence only. No benchmark scenario ran in this report; see per-test records and reasons below.

## 2. Test environment

See [ENVIRONMENT.md](../ENVIRONMENT.md). The control plane was not listening at the configured local address, and read-only OpenStack authentication failed. Live infrastructure values are therefore unobserved.

## 3. Methodology

Timing is to use monotonic `time.perf_counter` deltas, with UTC event timestamps and individual CSV samples. Since the live scenarios were not executed, no latency or success-rate metric is reported.

- **T1 — Provisioning latency:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T2 — Concurrent provisioning stress:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T3 — Gateway failover:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T4 — IP recycling:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T5 — Lease expiry and teardown:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T6 — Reload and rollback:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.
- **T7 — Network isolation:** NOT EXECUTED. Historical preflight network checks were blocked by the agent sandbox; current API and cloud status are undetermined.

## 4. Results summary

| Metric | Target | Measured (min / mean / p95 / max) | Pass/Fail | n |
|---|---:|---:|---|---:|
| T1 provisioning p95 | < 60 s | NOT EXECUTED | NOT EXECUTED | 0 |
| T2 concurrency (10+) | 100% success, 0 IP conflicts, 0 misroutes | NOT EXECUTED | NOT EXECUTED | 0 |
| T3 failover | < 3 s | NOT EXECUTED | NOT EXECUTED | 0 |
| T4 IP recycling | < 5 s | NOT EXECUTED | NOT EXECUTED | 0 |
| T5 lease teardown | No numeric target specified | NOT EXECUTED | NOT EXECUTED | 0 |
| T6 reload / rollback | 0 drops / < 1 s rollback | NOT EXECUTED | NOT EXECUTED | 0 |
| T7 isolation | 0 successful forbidden connections | NOT EXECUTED | NOT EXECUTED | 0 |

## 5. Detailed results

No charts are generated for tests without samples; plotting empty or synthetic data would imply measurements that do not exist.

### T1 — Provisioning latency

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t1_20261017T204842Z_summary.json`

### T2 — Concurrent provisioning stress

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t2_20261017T204843Z_summary.json`

### T3 — Gateway failover

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t3_20261017T204853Z_summary.json`

### T4 — IP recycling

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t4_20261017T204845Z_summary.json`

### T5 — Lease expiry and teardown

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t5_20261017T204847Z_summary.json`

### T6 — Reload and rollback

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t6_20261017T204849Z_summary.json`

### T7 — Network isolation

Status: **NOT EXECUTED**.

Raw record: `../results/raw/t7_20261017T204850Z_summary.json`

## 6. Bugs and anomalies

See [bugs_found.md](../bugs_found.md). Existing entries must be treated as unverified unless reproduced by this benchmark; no live benchmark bug was reproduced.

## 7. Limitations and threats to validity

The earlier preflight was run inside a restricted agent sandbox; its `Operation not permitted` network errors do not establish API health or OpenStack authentication status. No cloud resources, gateway state, DNS, actual pool size, or live package versions were verified. `ENVIRONMENT.md` is now regenerated only by `bench/preflight.py` and awaits a network-enabled run. The requested cold/warm distinction also requires a controllable image-cache mechanism, which has not been established.

## 8. Reproduction

Install pinned dependencies with `python3 -m pip install -r bench/requirements.txt`, provide `ADMIN_TOKEN` and valid OpenStack auth, then run an individual file under `bench/tests/` or `bash bench/run_all.sh`. Scripts fail closed pending verified isolated-target gates.

## 9. Challenge scoring mapping

- Stress test: T2 NOT EXECUTED.
- Teardown: T5 NOT EXECUTED.
- Isolation: T7 NOT EXECUTED.
