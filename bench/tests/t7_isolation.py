#!/usr/bin/env python3
"""T7 network-isolation harness with a read-only capability/preflight gate."""
import argparse
import asyncio
import csv
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bench.lib.api_client import AsyncControlPlaneClient
from bench.lib.config import load_config
from bench.tests import t2_concurrent_stress as t2
from bench.tests import t5_lease_expiry as t5

BASE = Path(__file__).resolve().parents[1]
RAW = BASE / "results" / "raw"
CHARTS = BASE / "results" / "charts"
POOL_URL = "http://127.0.0.1:8081"
NOT_EXECUTED_REASON = (
    "provision API does not accept custom user-data; the provisioner always uses cloudInitHTTP()"
)
ALTERNATIVE = (
    "Add an optional, size-limited user_data field for bench-* provision requests and pass it to Nova; "
    "keep the existing cloudInitHTTP() as the default. This control-plane change is not included."
)
SETTINGS = ("MAX_CONCURRENT_PROVISIONS", "HEALTH_REGRESSION_WINDOW_SECONDS",
            "GATEWAY_PROBE_TIMEOUT_SECONDS", "RATE_LIMIT_PER_MIN", "PROVISION_TIMEOUT_SECONDS")
GATEWAY_SANDBOX_IPS = {"gw1": "10.20.30.86", "gw2": "10.20.30.224"}
ALL_PROBES = [
    {"target": target, "port": port, "kind": kind}
    for target, ports, kind in (
        ("gw1", (22, 80, 443, 5555), "gateway_sandbox"),
        ("gw2", (22, 80, 443, 5555), "gateway_sandbox"),
        ("sandbox_b", (22, 80, 8080), "peer_sandbox"),
        ("team1", (22, 80, 8080), "permanent_sandbox"),
        ("team2", (22, 80, 8080), "permanent_sandbox"),
        ("bastion_edge", (22,), "management"),
        ("bastion_mgmt", (22,), "management"),
        ("metadata", (80,), "metadata"),
        ("gw1_mgmt", (22, 5555), "gateway_management"),
        ("gw2_mgmt", (22, 5555), "gateway_management"),
    ) for port in ports
]
SMOKE_PROBES = [probe for probe in ALL_PROBES if probe["kind"] == "gateway_sandbox"]
FIXED_TARGETS = {"gw1": "10.20.30.86", "gw2": "10.20.30.224", "team1": "10.20.30.37",
                 "team2": "10.20.30.15", "bastion_edge": "10.20.20.177",
                 "bastion_mgmt": "10.20.10.246", "metadata": "169.254.169.254",
                 "gw1_mgmt": "10.20.10.24", "gw2_mgmt": "10.20.10.164"}


def classify_probe(probe):
    if probe["kind"] == "metadata":
        return "record_only"
    if probe["kind"] == "gateway_sandbox" and probe["port"] in (80, 443):
        return "allowed"
    return "forbidden"


def utc_now():
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def process_settings():
    return {key: os.environ.get(key, "unset") for key in SETTINGS}


async def read_security_groups(cloud):
    rules = {}
    errors = {}
    for group in ("sg-sandbox", "sg-gateway"):
        try:
            rules[group] = await t2.openstack_json(cloud, "security", "group", "rule", "list", group)
        except Exception as exc:
            errors[group] = t2.redact_process_error(f"{type(exc).__name__}: {exc}")
    return rules, errors


async def run_preflight(cfg, api, db_path):
    observation = await t5.preflight(cfg, api, db_path)
    rules, errors = await read_security_groups(cfg["openstack"]["cloud"])
    observation["security_group_rules"] = rules
    observation["security_group_rule_errors"] = errors
    observation["security_group_rules_read"] = set(rules) == {"sg-sandbox", "sg-gateway"}
    observation["passed"] = bool(observation.get("passed")) and observation["security_group_rules_read"]
    return observation


async def execute(cfg, smoke):
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    result = {"test": "T7_network_isolation", "timestamp_utc": utc_now(),
              "status": "NOT EXECUTED", "mode": "smoke" if smoke else "full",
              "probe_scope": "gateway sandbox-side IPs only" if smoke else "all specified targets",
              "process_environment": process_settings(), "preflight": {},
              "provisioning_capability": {"custom_user_data_supported": False,
                  "reason": NOT_EXECUTED_REASON, "proposed_least_invasive_alternative": ALTERNATIVE},
              "probe_results": [], "forbidden_open_connections": [],
              "final_sanity": {"status": "NOT EXECUTED", "reason": "no sandboxes were provisioned"}}
    token = os.environ.get("ADMIN_TOKEN", "")
    if not token:
        result["error"] = "ADMIN_TOKEN is unset"
        return await write_results(result, [], stamp)
    try:
        if os.environ.get("DPA_PASS", "") == "":
            raise RuntimeError("DPA_PASS is unset")
        if os.environ.get("ROUTING_DOMAIN") != "cloudpulse.cstam.felcloud.tn":
            raise RuntimeError("ROUTING_DOMAIN must be cloudpulse.cstam.felcloud.tn")
        if cfg.get("control_plane", {}).get("base_url", "").rstrip("/") != POOL_URL:
            raise RuntimeError(f"T7 requires the live control plane at {POOL_URL}")
        db_path = t2.resolve_live_db_path()
        result["live_db_path"] = db_path
        async with AsyncControlPlaneClient(POOL_URL, token, timeout=30) as api:
            status, health = await t2.api_json(api, "GET", "/health")
            if status != 200:
                raise RuntimeError(f"control plane /health returned HTTP {status}: {health}")
            preflight = await run_preflight(cfg, api, db_path)
        result["preflight"] = preflight
        if not preflight.get("passed"):
            result["status"] = "NOT EXECUTED"
            result["error"] = "T7 preflight failed; no sandbox was provisioned"
        else:
            result["status"] = "NOT EXECUTED"
            result["error"] = NOT_EXECUTED_REASON
            result["proposed_least_invasive_alternative"] = ALTERNATIVE
        return await write_results(result, [], stamp)
    except Exception as exc:
        result["status"] = "NOT EXECUTED"
        result["error"] = t2.redact_process_error(f"{type(exc).__name__}: {exc}")
        return await write_results(result, [], stamp)


async def write_results(result, rows, stamp):
    RAW.mkdir(parents=True, exist_ok=True)
    CHARTS.mkdir(parents=True, exist_ok=True)
    csv_path = RAW / f"t7_isolation_{stamp}.csv"
    if not rows:
        rows = [{"status": result.get("status", "NOT EXECUTED"),
                 "reason": result.get("error", NOT_EXECUTED_REASON),
                 "result": "NOT EXECUTED", "classification": "not observed"}]
    fields = sorted({key for row in rows for key in row}) or ["status", "reason", "target", "port", "result", "classification"]
    with csv_path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields, extrasaction="ignore")
        writer.writeheader()
        for row in rows:
            writer.writerow({key: json.dumps(value, sort_keys=True) if isinstance(value, (dict, list)) else value
                             for key, value in row.items()})
    chart_path = CHARTS / f"t7_isolation_{stamp}.png"
    fig, axis = plt.subplots(figsize=(9, 4))
    axis.text(0.5, 0.58, result.get("status", "NOT EXECUTED"), ha="center", va="center",
              fontsize=18, transform=axis.transAxes)
    axis.text(0.5, 0.38, result.get("error", NOT_EXECUTED_REASON), ha="center", va="center",
              wrap=True, transform=axis.transAxes)
    axis.set_axis_off()
    fig.suptitle("T7 network isolation")
    fig.tight_layout()
    fig.savefig(chart_path, dpi=150)
    plt.close(fig)
    summary_path = RAW / f"t7_isolation_{stamp}_summary.json"
    result["artifacts"] = {"raw_csv": str(csv_path), "summary_json": str(summary_path), "chart": str(chart_path)}
    summary_path.write_text(json.dumps(result, indent=2, sort_keys=True, default=str) + "\n", encoding="utf-8")
    return result


def main():
    parser = argparse.ArgumentParser(description="T7 network-isolation test")
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--dry-run", action="store_true")
    modes.add_argument("--smoke", action="store_true")
    args = parser.parse_args()
    try:
        cfg = load_config(BASE / "config.yaml")
        probes = SMOKE_PROBES if args.smoke else ALL_PROBES
        if args.dry_run:
            print(json.dumps({"test": "T7_network_isolation", "mode": "DRY RUN", "network_calls": False,
                "status": "NOT EXECUTED", "smoke": args.smoke,
                "prober_sandbox": "bench-t7-<timestamp>-a", "target_sandbox": "bench-t7-<timestamp>-b",
                "provision_request_user_data": "unsupported by current API; provisioning will stop after read-only preflight",
                "proposed_least_invasive_alternative": ALTERNATIVE,
                "probe_scope": "gateway sandbox-side IPs only" if args.smoke else "all specified targets",
                "planned_probe_count": len(probes),
                "planned_probes": [{"target": FIXED_TARGETS.get(item["target"], item["target"]),
                                    "target_name": item["target"], "port": item["port"],
                                    "classification": classify_probe(item)} for item in probes],
                "process_environment": process_settings(),
                "security_groups_to_record": ["sg-sandbox", "sg-gateway"],
                "classification": {"allowed": ["gateway sandbox-side TCP 80/443", "metadata (record only)"],
                                   "pass_condition": "zero forbidden connections open"},
                "actions": ["read-only T5 preflight: five permanent VMs, exact gateway baselines, no unexpected non-RELEASED bench rows",
                    "read and store sg-sandbox and sg-gateway rules using OpenStack list calls",
                    "report NOT EXECUTED before provisioning because the API has no custom user-data field",
                    "if user-data support is added separately, provision only the two generated bench-t7-* IDs, probe with 3-second connect timeouts, then DELETE one at a time",
                    "write CSV, summary JSON, and chart"]}, indent=2))
            return 0
        result = asyncio.run(execute(cfg, args.smoke))
        print(json.dumps({"test": result["test"], "status": result["status"], "error": result.get("error"),
                          "preflight": result.get("preflight"), "alternative": result.get("proposed_least_invasive_alternative"),
                          "artifacts": result.get("artifacts")}, indent=2))
        return 0 if result["status"] == "PASSED" else 2
    except Exception as exc:
        print(f"T7 NOT EXECUTED: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
