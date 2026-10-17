#!/usr/bin/env bash
set -u
cd "$(dirname "$0")/.."
python=bench/.venv/bin/python
if [[ ! -x "$python" ]]; then python=python3; fi

# Ordered as requested; each test records preflight failures without mutations.
status=0
for test in \
  bench/tests/t1_provisioning_latency.py \
  bench/tests/t2_concurrent_stress.py \
  bench/tests/t4_ip_recycling.py \
  bench/tests/t5_lease_expiry_teardown.py \
  bench/tests/t6_reload_rollback.py \
  bench/tests/t7_isolation_security.py \
  bench/tests/t3_gateway_failover.py; do
  "$python" "$test" || status=1
done

"$python" bench/report/generate_report.py
exit "$status"
