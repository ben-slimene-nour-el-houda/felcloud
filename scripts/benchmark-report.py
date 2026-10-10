#!/usr/bin/env python3
"""Build an evidence-only Markdown and JSON report from benchmark result files."""
import argparse
import datetime
import json
import math
import os
from pathlib import Path
import statistics
import sys


ROOT = Path(__file__).resolve().parent.parent
BENCHMARKS = ROOT / "docs" / "benchmarks"
TIMINGS = ("allocate", "port", "server_active", "health", "sync", "total")
FAILOVER_MODES = ("haproxy-only", "keepalived", "gateway-reboot")
VERIFIERS = ("responding-gateway-header", "vip-owner")


class ReportError(Exception):
    pass


def fail(message):
    raise ReportError(message)


def read_json(path, label):
    try:
        with open(path, encoding="utf-8") as stream:
            value = json.load(stream)
    except (OSError, json.JSONDecodeError) as error:
        fail(f"{label}: cannot read valid JSON from {path}: {error}")
    if not isinstance(value, dict):
        fail(f"{label}: top-level JSON value must be an object")
    return value


def is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) and value >= 0


def validate_stress(data):
    missing = []
    for field in ("mode", "date_utc", "git_commit", "config", "teams", "aggregates"):
        if field not in data:
            missing.append(field)
    if missing:
        fail("stress results missing required fields: " + ", ".join(missing))
    if data["mode"] not in ("mock", "live"):
        fail("stress mode must be 'mock' or 'live'")
    if not isinstance(data["config"], dict) or not isinstance(data["teams"], list) or not isinstance(data["aggregates"], dict):
        fail("stress config, teams, and aggregates have invalid types")
    for index, row in enumerate(data["teams"]):
        if not isinstance(row, dict) or not isinstance(row.get("provision_status"), int):
            fail(f"stress teams[{index}] must be an object with integer provision_status")
        timings = row.get("timings_ms", row)
        if not isinstance(timings, dict):
            fail(f"stress teams[{index}].timings_ms must be an object")
        for key in TIMINGS:
            if timings.get(key) is not None and not is_number(timings[key]):
                fail(f"stress teams[{index}].{key} must be a non-negative number or null")
    return missing


def validate_recycle(data):
    required = ("mode", "date_utc", "git_commit", "config", "cycles")
    missing = [field for field in required if field not in data]
    if missing:
        fail("recycle results missing required fields: " + ", ".join(missing))
    if data["mode"] not in ("mock", "live") or not isinstance(data["config"], dict) or not isinstance(data["cycles"], list):
        fail("recycle mode, config, or cycles has an invalid type")
    for index, cycle in enumerate(data["cycles"]):
        if not isinstance(cycle, dict):
            fail(f"recycle cycles[{index}] must be an object")
        for key in ("delete_to_quarantined_ms", "quarantined_to_free_ms", "delete_to_same_ip_allocated_ms"):
            if cycle.get(key) is not None and not is_number(cycle[key]):
                fail(f"recycle cycles[{index}].{key} must be a non-negative number or null")


def validate_failover(data):
    if not isinstance(data.get("date"), str) or not isinstance(data.get("commit"), str):
        fail("failover results must include string date and commit fields")
    if not isinstance(data.get("runs"), list):
        fail("failover results must contain a runs array")
    for index, run in enumerate(data["runs"]):
        if not isinstance(run, dict):
            fail(f"failover runs[{index}] must be an object")
        mode = run.get("mode")
        if mode not in FAILOVER_MODES:
            fail(f"failover runs[{index}].mode must be one of {', '.join(FAILOVER_MODES)}")
        for key in ("failover_ms", "failed_requests", "total_requests"):
            if not isinstance(run.get(key), int) or isinstance(run[key], bool) or run[key] < 0:
                fail(f"failover runs[{index}].{key} must be a non-negative integer")
        if run["total_requests"] < 1 or run["failed_requests"] > run["total_requests"]:
            fail(f"failover runs[{index}] must have 1+ total_requests and failed_requests <= total_requests")
        if run.get("serving_gateway_before") == run.get("serving_gateway_after"):
            fail(f"failover runs[{index}] must prove a responding-gateway change")
        if run.get("verified_by") not in VERIFIERS:
            fail(f"failover runs[{index}].verified_by must verify the responding gateway")
        for key in ("serving_gateway_before", "serving_gateway_after"):
            if not isinstance(run.get(key), str) or not run[key]:
                fail(f"failover runs[{index}].{key} must be a non-empty string")


def latest_file(kind):
    candidates = sorted(BENCHMARKS.glob(f"{kind}-*/results.json"))
    return candidates[-1] if candidates else None


def resolve_input(value, kind):
    if value is None or value == "latest":
        return latest_file(kind)
    return Path(value).expanduser().resolve()


def load_input(path, kind, allow_mock):
    if path is None:
        return None
    if not path.is_file():
        fail(f"{kind}: input file does not exist: {path}")
    data = read_json(path, kind)
    {"stress": validate_stress, "recycle": validate_recycle, "failover": validate_failover}[kind](data)
    if kind != "failover" and data.get("mode") == "mock" and not allow_mock:
        fail(f"{kind}: mock results are not benchmark evidence; set ALLOW_MOCK=true to render a labelled dry-run report")
    data["_source_path"] = str(path)
    return data


def percentile(values, fraction):
    ordered = sorted(values)
    if not ordered:
        return None
    index = max(0, min(len(ordered) - 1, math.ceil(fraction * len(ordered)) - 1))
    return ordered[index]


def stats(values):
    if not values:
        return {"n": 0, "p50": None, "p95": None, "max": None, "warning": "NOT MEASURED"}
    n = len(values)
    warning = f"WARNING: sample size n={n} is below 10" if n < 10 else ""
    return {"n": n, "p50": percentile(values, .50),
            "p95": percentile(values, .95) if n >= 20 else None,
            "max": max(values), "warning": warning}


def fmt(value, digits=2):
    return "NOT MEASURED" if value is None else f"{value:.{digits}f}"


def sample_note(n):
    return f"n={n}" + ("; WARNING: n < 10" if 0 < n < 10 else "")


def table_label(mock):
    return "**MOCK DATA - NOT A BENCHMARK**" if mock else ""


def stat_table(items, mock):
    label = table_label(mock)
    lines = []
    if label:
        lines.append(label)
    lines.extend(["| Metric | p50 (sample) | p95 (sample) | max (sample) |",
                  "|---|---:|---:|---:|"])
    for name, summary in items:
        p95 = fmt(summary["p95"]) if summary["p95"] is not None else ("NOT MEASURED" if summary["n"] == 0 else "n<20; see max")
        lines.append(f"| {name} | {fmt(summary['p50'])} ({summary['n']}) | "
                     f"{p95} ({summary['n']}) | {fmt(summary['max'])} ({summary['n']}) |")
    lines.append("")
    warnings = [summary["warning"] for _, summary in items if summary["warning"]]
    if warnings:
        lines.append("; ".join(dict.fromkeys(warnings)))
        lines.append("")
    return lines


def stress_metrics(data):
    result = {}
    for key in TIMINGS:
        values = []
        for row in data["teams"]:
            if row.get("provision_status") != 201:
                continue
            timing = row.get("timings_ms", row)
            value = timing.get(key) if isinstance(timing, dict) else None
            if is_number(value):
                values.append(float(value))
        result[key] = stats(values)
    return result


def failover_metrics(data):
    result = {}
    for mode in FAILOVER_MODES:
        runs = [run for run in data["runs"] if run["mode"] == mode]
        times = [run["failover_ms"] for run in runs]
        total = sum(run["total_requests"] for run in runs)
        failed = sum(run["failed_requests"] for run in runs)
        result[mode] = {
            "n": len(runs), "min_ms": min(times) if times else None,
            "median_ms": statistics.median(times) if times else None,
            "max_ms": max(times) if times else None,
            "failed_requests": failed, "total_requests": total,
            "loss_percent": (100 * failed / total) if total else None,
            "verified_by": sorted({run["verified_by"] for run in runs}),
        }
    return result


def generate_report(stress, recycle, failover, allow_mock):
    sources = [item for item in (stress, recycle) if item is not None]
    mock = any(item.get("mode") == "mock" for item in sources)
    if mock and not allow_mock:
        fail("mock benchmark input rejected; set ALLOW_MOCK=true for a clearly labelled dry-run report")
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    report_sources = sources + ([failover] if failover else [])
    input_dates = [item.get("date_utc", item.get("date")) for item in report_sources
                   if item and item.get("date_utc", item.get("date"))]
    commits = [item.get("git_commit", item.get("commit")) for item in report_sources
               if item and item.get("git_commit", item.get("commit"))]
    commit = next((value for value in commits if value != "unknown"), None)
    stress_config = stress.get("config", {}) if stress else {}
    stress_environment = stress.get("environment", {}) if stress else {}

    def captured_setting(*names):
        for source in (stress_config, stress_environment):
            for name in names:
                value = source.get(name)
                if value not in (None, ""):
                    return value
        return "NOT MEASURED"

    environment = {
        "date_utc": now,
        "git_commit": commit or "NOT MEASURED",
        "cloud": captured_setting("cloud", "OS_CLOUD"),
        "flavor": captured_setting("flavor", "SANDBOX_FLAVOR"),
        "image": captured_setting("image", "SANDBOX_IMAGE"),
        "MAX_CONCURRENT_PROVISIONS": captured_setting("MAX_CONCURRENT_PROVISIONS"),
        "HEALTH_PROBE_MODE": captured_setting("HEALTH_PROBE_MODE"),
        "rate_limit_per_min": captured_setting("rate_limit_per_min", "RATE_LIMIT_PER_MIN"),
        "source_dates": input_dates or ["NOT MEASURED"],
        "mode": "MOCK DATA - NOT A BENCHMARK" if mock else ("LIVE" if sources else "NOT MEASURED"),
    }
    metrics = {
        "provisioning_latency_ms": {key: {"n": 0, "p50": None, "p95": None, "max": None,
                                          "warning": "NOT MEASURED"} for key in TIMINGS},
        "load_test": None, "teardown_ms": {"n": 0, "p50": None, "p95": None, "max": None,
                                           "warning": "NOT MEASURED"},
        "recycling": None, "gateway_failover": None,
    }
    if stress and not mock:
        metrics["provisioning_latency_ms"] = stress_metrics(stress)
        rows = stress["teams"]
        aggregate = stress["aggregates"]
        cfg = stress["config"]
        success_count = sum(row.get("provision_status") == 201 for row in rows)
        metrics["load_test"] = {
            "teams": cfg.get("teams", len(rows)), "concurrency": cfg.get("concurrency", "NOT MEASURED"),
            "success_rate_percent": aggregate.get("success_rate_percent"),
            "wall_time_ms": stress.get("burst_wall_ms"),
            "provisions_per_minute": aggregate.get("provisions_per_minute"),
            "ip_uniqueness": aggregate.get("ip_uniqueness"),
            "sync_coalescing_ratio": aggregate.get("sync_coalescing_ratio"),
            "sync_events": stress.get("gateway_sync_events"), "successful": success_count,
        }
        teardown = [float(row["teardown_ms"]) for row in rows
                    if row.get("teardown_status") == 200 and is_number(row.get("teardown_ms"))]
        metrics["teardown_ms"] = stats(teardown)
    if recycle and not mock:
        metrics["recycling"] = {
            "cycles": len(recycle["cycles"]),
            "cooldown_seconds": recycle["config"].get("quarantine_cooldown_seconds", "NOT MEASURED"),
            "delete_to_quarantined_ms": stats([float(row["delete_to_quarantined_ms"])
                for row in recycle["cycles"] if is_number(row.get("delete_to_quarantined_ms"))]),
            "quarantined_to_free_ms": stats([float(row["quarantined_to_free_ms"])
                for row in recycle["cycles"] if is_number(row.get("quarantined_to_free_ms"))]),
            "delete_to_same_ip_allocated_ms": stats([float(row["delete_to_same_ip_allocated_ms"])
                for row in recycle["cycles"] if is_number(row.get("delete_to_same_ip_allocated_ms"))]),
        }
    if failover:
        metrics["gateway_failover"] = failover_metrics(failover)

    data_suppressed = mock
    lines = ["# FelCloud Benchmark Report", ""]
    if mock:
        lines.extend(["# MOCK DATA - NOT A BENCHMARK", "",
                      "Mock input measurements are excluded from benchmark tables. "
                      "ALLOW_MOCK=true permits this labelled dry-run report only.", ""])
    lines.extend(["## Environment", "",
                  f"- Report date (UTC): {environment['date_utc']}",
                  f"- Git commit: `{environment['git_commit']}`",
                  f"- Cloud: {environment['cloud']}",
                  f"- Flavor: {environment['flavor']}",
                  f"- Image: {environment['image']}",
                  f"- MAX_CONCURRENT_PROVISIONS: {environment['MAX_CONCURRENT_PROVISIONS']}",
                  f"- HEALTH_PROBE_MODE: {environment['HEALTH_PROBE_MODE']}",
                  f"- RATE_LIMIT_PER_MIN: {environment['rate_limit_per_min']}",
                  f"- Input modes: {environment['mode']}", ""])
    lines.append(f"- Input run dates: {', '.join(input_dates) if input_dates else 'NOT MEASURED'}")
    lines.append("")
    lines.extend(["## Provisioning latency (ms)", ""])
    if data_suppressed:
        lines.extend(["MOCK DATA - NOT A BENCHMARK", "", "NOT MEASURED — mock data suppressed.", ""])
    elif stress:
        lines.extend(stat_table([(name, metrics["provisioning_latency_ms"][name]) for name in TIMINGS], False))
    else:
        lines.extend(["NOT MEASURED — no stress results input.", ""])

    lines.extend(["## Load test", ""])
    if data_suppressed:
        lines.extend(["MOCK DATA - NOT A BENCHMARK", "", "NOT MEASURED — mock data suppressed.", ""])
    elif metrics["load_test"]:
        load = metrics["load_test"]
        if mock:
            lines.append("MOCK DATA - NOT A BENCHMARK")
        lines.extend(["| Teams | Concurrency | Success rate | Burst wall (ms) | Provisions/min | IP uniqueness | Sync coalescing ratio |",
                      "|---:|---:|---:|---:|---:|---|---:|",
                      f"| {load['teams']} | {load['concurrency']} | {fmt(load['success_rate_percent'])}% | "
                      f"{fmt(load['wall_time_ms'])} | {fmt(load['provisions_per_minute'])} | "
                      f"{load['ip_uniqueness'] if load['ip_uniqueness'] is not None else 'NOT MEASURED'} | "
                      f"{fmt(load['sync_coalescing_ratio'])} |", ""])
    else:
        lines.extend(["NOT MEASURED — no stress results input.", ""])

    lines.extend(["## Teardown time (ms)", ""])
    if data_suppressed:
        lines.extend(["MOCK DATA - NOT A BENCHMARK", "", "NOT MEASURED — mock data suppressed.", ""])
    elif stress:
        lines.extend(stat_table([("teardown", metrics["teardown_ms"])], False))
    else:
        lines.extend(["NOT MEASURED — no stress results input.", ""])

    lines.extend(["## IP recycling speed", ""])
    if data_suppressed:
        lines.extend(["MOCK DATA - NOT A BENCHMARK", "", "NOT MEASURED — mock data suppressed.", ""])
    elif metrics["recycling"]:
        recycling = metrics["recycling"]
        lines.append(f"Configured quarantine cooldown: {recycling['cooldown_seconds']} seconds.")
        lines.append("")
        lines.extend(stat_table([
            ("DELETE to QUARANTINED (ms)", recycling["delete_to_quarantined_ms"]),
            ("QUARANTINED to FREE (ms)", recycling["quarantined_to_free_ms"]),
            ("DELETE to same IP allocated (ms)", recycling["delete_to_same_ip_allocated_ms"]),
        ], False))
    else:
        lines.extend(["NOT MEASURED — no live recycle results input.", ""])

    lines.extend(["## Gateway failover", ""])
    if metrics["gateway_failover"] and any(row["n"] for row in metrics["gateway_failover"].values()):
        label = table_label(mock)
        if label:
            lines.extend([label, ""])
        lines.extend(["| Mode | Runs (n) | Failover min / median / max (ms) | Request loss | Responding gateway verification |",
                      "|---|---:|---:|---:|---|"])
        for mode, item in metrics["gateway_failover"].items():
            if not item["n"]:
                lines.append(f"| {mode} | 0 | NOT MEASURED | NOT MEASURED | NOT MEASURED |")
            else:
                lines.append(f"| {mode} | {item['n']} | {item['min_ms']} / {item['median_ms']} / {item['max_ms']} "
                             f"(n={item['n']}) | {fmt(item['loss_percent'])}% "
                             f"({item['failed_requests']}/{item['total_requests']}) | "
                             f"{', '.join(item['verified_by'])} |")
        lines.append("")
        small_samples = [f"{mode} n={item['n']}" for mode, item in metrics["gateway_failover"].items()
                         if 0 < item["n"] < 10]
        if small_samples:
            lines.extend(["WARNING: sample size below 10 for " + ", ".join(small_samples) + ".", ""])
    else:
        lines.extend(["NOT MEASURED — no failover results input.", ""])

    lines.extend(["## Assertions and limitations", "",
                  "- Percentiles use nearest-rank; p95 is reported only for n >= 20. For smaller samples, the maximum is shown instead.",
                  "- Every latency statistic includes its sample size. Samples below 10 are explicitly warned.",
                  "- Missing datasets are marked NOT MEASURED; no values are inferred.",
                  "- Failover inputs are accepted only when each run records a changed serving gateway and an approved responding-gateway verifier.",
                  "- The API's quarantine cooldown setting is inferred from lease timestamps where present; otherwise the recycle input's configured fallback is used.",
                  "- Mock results are excluded from measurement summaries even when ALLOW_MOCK=true.",
                  "- Benchmark inputs are sourced from: " +
                  "; ".join(str(item["_source_path"]) for item in (stress, recycle, failover) if item) + ".",
                  "- Mock measurements are never represented as live benchmark data.", ""])
    section_status = {}
    for key, source, available in (
        ("provisioning_latency", stress, bool(stress)),
        ("load_test", stress, bool(stress)),
        ("teardown", stress, bool(stress)),
        ("ip_recycling", recycle, bool(recycle)),
        ("gateway_failover", failover, bool(failover and metrics["gateway_failover"]
            and any(item["n"] for item in metrics["gateway_failover"].values()))),
    ):
        section_status[key] = (
            "MOCK DATA - NOT A BENCHMARK" if data_suppressed and source is not None
            else "MEASURED" if available else "NOT MEASURED"
        )
    return "\n".join(lines), {"schema_version": 1, "environment": environment, "mock_data_suppressed": data_suppressed,
                              "section_status": section_status,
                              "limitations": [
                                  "Percentiles use nearest-rank; p95 requires n >= 20.",
                                  "Samples below 10 are warned; missing inputs remain NOT MEASURED.",
                                  "Mock values are suppressed from measurements.",
                                  "Failover results require a responding-gateway verification marker and changed gateway identity.",
                              ],
                              "metrics": metrics, "inputs": {
                                  name: (data.get("_source_path") if data else None)
                                  for name, data in (("stress", stress), ("recycle", recycle), ("failover", failover))}}


def check_inputs(stress_path, recycle_path, failover_path, allow_mock):
    missing = []
    valid = True
    for name, path in (("stress", stress_path), ("recycle", recycle_path), ("failover", failover_path)):
        if path is None:
            missing.append(name)
            print(f"MISSING: {name} results")
        else:
            try:
                data = load_input(path, name, True)
                if name != "failover" and data.get("mode") == "mock" and not allow_mock:
                    print(f"INVALID: {name} results use mock mode; set ALLOW_MOCK=true to allow a labelled dry run")
                    valid = False
                else:
                    print(f"VALID: {name} results ({path})")
            except ReportError as error:
                print(f"INVALID: {error}")
                valid = False
    if missing:
        print("Missing datasets: " + ", ".join(missing))
    return valid and not missing


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stress", default="latest", help="stress results.json path or 'latest'")
    parser.add_argument("--recycle", default="latest", help="recycle results.json path or 'latest'")
    parser.add_argument("--failover", default=os.environ.get("FAILOVER_RESULTS"),
                        help="optional failover results.json (default: FAILOVER_RESULTS)")
    parser.add_argument("--check", action="store_true", help="validate inputs and report missing data")
    args = parser.parse_args(argv)
    allow_mock = os.environ.get("ALLOW_MOCK", "").lower() == "true"
    try:
        stress_path = resolve_input(args.stress, "stress")
        recycle_path = resolve_input(args.recycle, "recycle")
        failover_path = Path(args.failover).expanduser().resolve() if args.failover else None
        if args.check:
            return 0 if check_inputs(stress_path, recycle_path, failover_path, allow_mock) else 1
        stress = load_input(stress_path, "stress", True)
        recycle = load_input(recycle_path, "recycle", True)
        failover = load_input(failover_path, "failover", allow_mock)
        mock_input = any(data and data.get("mode") == "mock" for data in (stress, recycle))
        report, output = generate_report(stress, recycle, failover, allow_mock or mock_input)
        BENCHMARKS.mkdir(parents=True, exist_ok=True)
        (BENCHMARKS / "BENCHMARK_REPORT.md").write_text(report, encoding="utf-8")
        (BENCHMARKS / "report.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(report)
        print(f"Report files: {BENCHMARKS / 'BENCHMARK_REPORT.md'} and {BENCHMARKS / 'report.json'}")
        if mock_input and not allow_mock:
            print("ERROR: mock data report written with measurements suppressed; set ALLOW_MOCK=true to accept this dry run.",
                  file=sys.stderr)
            return 2
        return 0
    except ReportError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
