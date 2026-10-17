# FelCloud benchmark harness

## Readiness and safety

Start with the read-only preflight:

```bash
python3 bench/preflight.py
```

It checks the control plane `/health`, confirms `ADMIN_TOKEN` is set without displaying it, runs `openstack token issue`, SSHes through the configured bastion to both configured gateway destinations, checks Nova and Neutron quota headroom for the planned concurrency, and verifies that cleanup excludes `team1` and `team2`. It overwrites `ENVIRONMENT.md` with observations from that invocation. It does not provision, delete, stop, reload, or alter resources.

Set the required environment values before preflight. The precise set depends on your SSH setup and sandbox flavor:

```bash
export CONTROL_PLANE_URL=https://control-plane.example
export OS_CLOUD=your-cloud
export BENCH_SSH_USER=ansible
export BENCH_BASTION_HOST=bastion.example
export SSH_KEY_PATH=/path/to/private-key
export BENCH_PRIMARY_HOST=gateway-primary.example
export BENCH_STANDBY_HOST=gateway-standby.example
export BENCH_SANDBOX_FLAVOR=your-sandbox-flavor
```

Provide `ADMIN_TOKEN` through your secret manager or a private shell environment; do not copy a placeholder or the token itself into this document.

Install the pinned tools and run the local mocked tests with:

```bash
python3 -m venv bench/.venv
bench/.venv/bin/pip install -r bench/requirements.txt
bench/.venv/bin/python -m pytest -q bench/unit_tests
```

The preflight reads the selected flavor's vCPU and RAM values from OpenStack before calculating quota headroom.

The gateway host values are exact SSH destinations allowed for gateway operations. They have no defaults. The control plane and cloud credentials come from environment variables or the OpenStack CLI's normal `clouds.yaml` configuration. Do not put secrets in `config.yaml` or commit them.

`safety_gates.resource_prefix` and `cleanup_name_prefix` must both remain exactly `bench-`. `protected_names` must include `team1` and `team2`. Cleanup must require the `bench-` prefix and reject protected names. A missing or inconsistent gate makes a test print an actionable error and exit without test actions.

## Test commands

Each test accepts `--smoke` (one iteration) and `--dry-run` (prints the planned actions and gates; no network, API, SSH, or OpenStack command is issued):

```bash
bench/.venv/bin/python bench/tests/t1_provisioning_latency.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t2_concurrent_stress.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t3_gateway_failover.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t4_ip_recycling.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t5_lease_expiry_teardown.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t6_reload_rollback.py --smoke --dry-run
bench/.venv/bin/python bench/tests/t7_isolation_security.py --smoke --dry-run
```

Run one test without `--dry-run` only after reviewing the preflight output and the destructive operations it may require. T3 includes gateway service stops and therefore must only target the verified test gateway configured in the allow-list.

Current implementation status: T1 has a live scenario runner, while T2–T7 currently implement argument handling, dry-run descriptions, and fail-closed safety gates only. Invoking an unimplemented scenario records `NOT EXECUTED` and exits rather than implying that a benchmark ran.

### T1 — provisioning latency

After a passing preflight, run the 30 sequential samples with:

```bash
bench/.venv/bin/python bench/tests/t1_provisioning_latency.py
```

Run one sample with `--smoke`:

```bash
bench/.venv/bin/python bench/tests/t1_provisioning_latency.py --smoke
```

Each run provisions a unique `bench-t1-*` sandbox, records the API's `timings_ms`, performs a DNS lookup, polls the configured VIP with the sandbox hostname in `Host` until HTTP 200 or timeout, then deletes that sandbox in `finally` cleanup. CSV and summary JSON are written under `bench/results/raw/`; a stacked stage chart is written under `bench/results/charts/` when at least one run reaches HTTP 200. If no sample reaches HTTP 200, no chart is produced. The first ten and remaining twenty runs are marked as cold/warm **candidates**, but cache state is explicitly unverified because this suite does not flush or inspect image caches. The API currently returns stage durations rather than exact stage event timestamps, so those absolute event timestamp fields remain empty and the report states this limitation.

**Scoring evidence:** T1 provides baseline latency and provisioning-path evidence, but it does not map directly to the named 15-point stress, 10-point teardown, or 5-point isolation criteria.

Expected files from a run are `bench/results/raw/t1_provisioning_<UTC timestamp>.csv`, `bench/results/raw/t1_provisioning_<UTC timestamp>_summary.json`, and, when at least one run reaches HTTP 200, `bench/results/charts/t1_provisioning_stages_<UTC timestamp>.png`. A run that cannot start writes a NOT EXECUTED CSV and summary with its reason.

## Scoring evidence by test

- **T1:** Provisioning latency baseline; no direct named 15/10/5-point criterion.
- **T2:** Concurrency, success rate, duplicate IP, and route isolation evidence for the 15-point stress criterion.
- **T3:** Gateway HA failover behavior; no named 15/10/5-point criterion.
- **T4:** IP reclamation/reuse evidence supporting the 10-point teardown criterion.
- **T5:** Lease expiry and complete resource cleanup evidence for the 10-point teardown criterion.
- **T6:** Reload/rollback reliability; no named 15/10/5-point criterion.
- **T7:** Cross-sandbox and management-plane isolation evidence for the 5-point isolation criterion.

Run the suite entry point in dry-run mode:

```bash
bash bench/run_all.sh --smoke --dry-run
```

`bash bench/run_all.sh` orders T1, T2, T4, T5, T6, T7, then T3. It generates the report from available results. Do not use it until live scenario implementations and preflight gates are reviewed.

## Outputs

- Preflight output is printed to the terminal; the latest environment snapshot is written to [ENVIRONMENT.md](ENVIRONMENT.md).
- Test attempt records are written to `bench/results/raw/<test>_<UTC timestamp>.csv` and matching `_summary.json` files.
- The report generator writes `bench/report/BENCHMARK_REPORT.md`.
- Charts belong in `bench/results/charts/`; no chart is produced without real samples.

Never treat a dry-run, preflight, or `NOT EXECUTED` record as a benchmark measurement.
