"""Safe benchmark entry points and evidence recording.

The cloud mutation suite is intentionally blocked unless the control plane is
healthy and authenticated cloud discovery succeeds. A blocked run is recorded
as NOT EXECUTED; it is never converted into a zero-valued measurement.
"""
import csv
import json
import os
import socket
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RAW = ROOT / "results" / "raw"
TESTS = {
    "t1": "Provisioning latency",
    "t2": "Concurrent provisioning stress and pool exhaustion",
    "t3": "Gateway failover (destructive) and manual failover",
    "t4": "IP recycling",
    "t5": "Lease expiry and teardown worker recovery",
    "t6": "Config reload and rollback",
    "t7": "Network isolation and access safeguards",
}


def utc_now():
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def preflight():
    reasons = []
    base = os.environ.get("CONTROL_PLANE_URL", "http://127.0.0.1:8081")
    # Non-mutating reachability check only.
    try:
        import httpx
        response = httpx.get(base.rstrip("/") + "/health", timeout=3.0)
        if response.status_code != 200:
            reasons.append(f"Control plane health returned HTTP {response.status_code}")
    except Exception as exc:
        reasons.append(f"Control plane unavailable at {base}: {type(exc).__name__}: {exc}")
    if not os.environ.get("ADMIN_TOKEN"):
        reasons.append("ADMIN_TOKEN is unset; authenticated API mutations are unavailable")
    cloud = os.environ.get("OS_CLOUD", "felcloud")
    try:
        result = subprocess.run(
            ["openstack", "--os-cloud", cloud, "token", "issue", "-f", "value", "-c", "expires_at"],
            capture_output=True, text=True, timeout=10, check=False,
        )
        if result.returncode:
            msg = (result.stderr or result.stdout).strip().splitlines()
            reasons.append(f"OpenStack read-only auth failed for cloud {cloud}: " + (msg[-1] if msg else "unknown error"))
    except Exception as exc:
        reasons.append(f"OpenStack CLI preflight failed: {type(exc).__name__}: {exc}")
    return reasons


def record(test_id, status, reasons):
    RAW.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    base = RAW / f"{test_id}_{stamp}"
    row = {"timestamp_utc": utc_now(), "test": test_id, "status": status,
           "reason": "; ".join(reasons), "sample_n": 0}
    with base.with_suffix(".csv").open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=row.keys())
        writer.writeheader()
        writer.writerow(row)
    base.with_name(base.name + "_summary.json").write_text(
        json.dumps({"test": test_id, "status": status, "reason": reasons,
                    "timestamp_utc": row["timestamp_utc"], "sample_n": 0}, indent=2) + "\n",
        encoding="utf-8",
    )
    print(f"{test_id}: {status}")
    for reason in reasons:
        print(f"  - {reason}")


def run_test(test_id):
    reasons = preflight()
    if reasons:
        record(test_id, "NOT EXECUTED", reasons)
        return 2
    # Current checkout does not contain a fully implemented/tested harness for
    # the live mutation scenarios. Fail closed instead of issuing unsafe ops.
    reason = "Live scenario implementation is gated pending isolated bench environment target verification."
    record(test_id, "NOT EXECUTED", [reason])
    return 2


def main():
    test_id = sys.argv[1]
    if test_id not in TESTS:
        raise SystemExit(f"unknown test id: {test_id}")
    raise SystemExit(run_test(test_id))
