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

The preflight reads the selected flavor's vCPU and RAM values from OpenStack before calculating quota headroom.

The gateway host values are exact SSH destinations allowed for gateway operations. They have no defaults. The control plane and cloud credentials come from environment variables or the OpenStack CLI's normal `clouds.yaml` configuration. Do not put secrets in `config.yaml` or commit them.

`safety_gates.resource_prefix` and `cleanup_name_prefix` must both remain exactly `bench-`. `protected_names` must include `team1` and `team2`. Cleanup must require the `bench-` prefix and reject protected names. A missing or inconsistent gate makes a test print an actionable error and exit without test actions.

## Test commands

Each test accepts `--smoke` (one iteration) and `--dry-run` (prints the planned actions and gates; no network, API, SSH, or OpenStack command is issued):

```bash
python3 bench/tests/t1_provisioning_latency.py --smoke --dry-run
python3 bench/tests/t2_concurrent_stress.py --smoke --dry-run
python3 bench/tests/t3_gateway_failover.py --smoke --dry-run
python3 bench/tests/t4_ip_recycling.py --smoke --dry-run
python3 bench/tests/t5_lease_expiry_teardown.py --smoke --dry-run
python3 bench/tests/t6_reload_rollback.py --smoke --dry-run
python3 bench/tests/t7_isolation_security.py --smoke --dry-run
```

Run one test without `--dry-run` only after reviewing the preflight output and the destructive operations it may require. T3 includes gateway service stops and therefore must only target the verified test gateway configured in the allow-list.

Current implementation status: these entry points implement argument handling, dry-run descriptions, and fail-closed safety gates. **The live scenario bodies are not implemented yet.** A non-dry-run invocation records `NOT EXECUTED` and exits rather than implying that a benchmark ran.

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
