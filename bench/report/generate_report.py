#!/usr/bin/env python3
"""Generate a report only from timestamped evidence present on disk."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RAW = ROOT / "results" / "raw"
OUT = ROOT / "report" / "BENCHMARK_REPORT.md"
ORDER = ["t1", "t2", "t3", "t4", "t5", "t6", "t7"]
NAMES = {"t1": "Provisioning latency", "t2": "Concurrent provisioning stress",
         "t3": "Gateway failover", "t4": "IP recycling",
         "t5": "Lease expiry and teardown", "t6": "Reload and rollback",
         "t7": "Network isolation"}


def latest_summary(test):
    paths = sorted(RAW.glob(f"{test}_*_summary.json"))
    return json.loads(paths[-1].read_text(encoding="utf-8")) if paths else None


def generate():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    lines = ["# FelCloud Benchmark Report", "",
        "## 1. Objective and scope", "",
        "Assess the CSTAM 3.0 resilient IP optimizer using measured evidence only. No benchmark scenario ran in this report; see per-test records and reasons below.", "",
        "## 2. Test environment", "",
        "See [ENVIRONMENT.md](../ENVIRONMENT.md). The control plane was not listening at the configured local address, and read-only OpenStack authentication failed. Live infrastructure values are therefore unobserved.", "",
        "## 3. Methodology", "",
        "Timing is to use monotonic `time.perf_counter` deltas, with UTC event timestamps and individual CSV samples. Since the live scenarios were not executed, no latency or success-rate metric is reported.", ""]
    for test in ORDER:
        rec = latest_summary(test)
        status = rec["status"] if rec else "NOT EXECUTED"
        reason = "; ".join(rec.get("reason", [])) if rec else "No run record exists."
        if "Operation not permitted" in reason:
            reason = "The historical preflight was blocked by the agent sandbox's network policy; current API and cloud status remain undetermined."
        lines.append(f"- **{test.upper()} — {NAMES[test]}:** {status}. {reason}")
    lines += ["", "## 4. Results summary", "",
        "| Metric | Target | Measured (min / mean / p95 / max) | Pass/Fail | n |",
        "|---|---:|---:|---|---:|",
        "| T1 provisioning p95 | < 60 s | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T2 concurrency (10+) | 100% success, 0 IP conflicts, 0 misroutes | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T3 failover | < 3 s | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T4 IP recycling | < 5 s | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T5 lease teardown | No numeric target specified | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T6 reload / rollback | 0 drops / < 1 s rollback | NOT EXECUTED | NOT EXECUTED | 0 |",
        "| T7 isolation | 0 successful forbidden connections | NOT EXECUTED | NOT EXECUTED | 0 |", "",
        "## 5. Detailed results", "",
        "No charts are generated for tests without samples; plotting empty or synthetic data would imply measurements that do not exist.", ""]
    for test in ORDER:
        rec = latest_summary(test)
        paths = sorted(RAW.glob(f"{test}_*_summary.json"))
        record = f"`../results/raw/{paths[-1].name}`" if paths else "none"
        lines += [f"### {test.upper()} — {NAMES[test]}", "",
            f"Status: **{rec['status'] if rec else 'NOT EXECUTED'}**.", "",
            f"Raw record: {record}", ""]
    lines += ["## 6. Bugs and anomalies", "",
        "See [bugs_found.md](../bugs_found.md). Existing entries must be treated as unverified unless reproduced by this benchmark; no live benchmark bug was reproduced.", "",
        "## 7. Limitations and threats to validity", "",
        "The earlier preflight ran inside a restricted agent sandbox; its `Operation not permitted` network errors do not establish API health or OpenStack authentication status. No cloud resources, gateway state, DNS, actual pool size, or live package versions were verified. `ENVIRONMENT.md` is regenerated only by `bench/preflight.py` and awaits a network-enabled run. The requested cold/warm distinction also requires a controllable image-cache mechanism, which has not been established.", "",
        "## 8. Reproduction", "",
        "Install pinned dependencies with `python3 -m pip install -r bench/requirements.txt`, provide `ADMIN_TOKEN` and valid OpenStack auth, then run an individual file under `bench/tests/` or `bash bench/run_all.sh`. Scripts fail closed pending verified isolated-target gates.", "",
        "## 9. Challenge scoring mapping", "",
        "- Stress test: T2 NOT EXECUTED.", "- Teardown: T5 NOT EXECUTED.", "- Isolation: T7 NOT EXECUTED.", ""]
    OUT.write_text("\n".join(lines), encoding="utf-8")
    print(OUT)


if __name__ == "__main__":
    generate()
