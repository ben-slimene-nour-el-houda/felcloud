#!/usr/bin/env python3
import concurrent.futures
import csv
import datetime
import ipaddress
import json
import os
import queue
import random
import re
import shutil
import shlex
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


DNS_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
TIMING_KEYS = ("allocate", "port", "server_active", "health", "sync", "total")
SERVER_TIMING_KEYS = (*TIMING_KEYS, "mark_tearing_down", "remove_routes", "delete_resources", "verify_deleted", "release_lease")
DEFAULT_EXCLUDED_IPS = ("10.20.30.1", "10.20.30.86", "10.20.30.224", "203.0.113.119")


def fail(message):
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(2)


def positive_int(name, default, minimum=1, maximum=100000):
    raw = os.environ.get(name, str(default))
    try:
        value = int(raw, 10)
    except ValueError:
        fail(f"{name} must be an integer")
    if value < minimum or value > maximum:
        fail(f"{name} must be between {minimum} and {maximum}")
    return value


def env_bool(name, default):
    value = os.environ.get(name, default).lower()
    if value not in ("true", "false"):
        fail(f"{name} must be true or false")
    return value == "true"


def api_request(config, method, path, payload=None):
    url = config["api_url"] + path
    body = None if payload is None else json.dumps(payload).encode()
    headers = {"Authorization": "Bearer " + config["admin_token"], "Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=body, headers=headers, method=method)
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=config["http_timeout_seconds"]) as response:
            raw = response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        raw = error.read()
        status = error.code
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raw = str(error).encode()
        status = 0
    elapsed = (time.monotonic() - started) * 1000
    text = raw.decode("utf-8", errors="replace")
    try:
        parsed = json.loads(text)
    except (json.JSONDecodeError, UnicodeDecodeError):
        parsed = None
    return status, elapsed, text, parsed


def assert_result(assertions, name, passed, detail, skipped=False):
    assertions.append({"name": name, "passed": bool(passed), "detail": str(detail), "skipped": skipped})
    print(f"{'SKIPPED' if skipped else 'PASS' if passed else 'FAIL'}: {name}: {detail}")


def cleanup_commands(teams, api_url):
    print("Cleanup commands for this run's PREFIX-scoped leftovers:")
    for team in teams:
        url = shlex.quote(api_url + "/api/sandboxes/" + team)
        print(f'  curl --fail-with-body -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" {url}')


def parallel_requests(config, teams, method, path_for, payload_for=None):
    pending = queue.Queue()
    for team in teams:
        pending.put(team)
    ready = threading.Barrier(config["concurrency"] + 1)
    results = {}
    result_lock = threading.Lock()

    def worker():
        ready.wait()
        while True:
            try:
                team = pending.get_nowait()
            except queue.Empty:
                return
            payload = payload_for(team) if payload_for else None
            status, elapsed, body, parsed = api_request(config, method, path_for(team), payload)
            with result_lock:
                results[team] = (status, elapsed, body, parsed)
            pending.task_done()

    executor = concurrent.futures.ThreadPoolExecutor(max_workers=config["concurrency"])
    futures = [executor.submit(worker) for _ in range(config["concurrency"])]
    ready.wait()
    start = time.monotonic()
    for future in futures:
        future.result()
    wall_ms = (time.monotonic() - start) * 1000
    executor.shutdown(wait=True)
    return results, wall_ms


def json_body(parsed):
    return parsed if isinstance(parsed, dict) else {}


def summarize_percentiles(values):
    ordered = sorted(values)
    if not ordered:
        return {"p50": None, "p95": None, "max": None}

    def percentile(fraction):
        rank = max(0, min(len(ordered) - 1, int((len(ordered) * fraction) + 0.999999) - 1))
        return round(ordered[rank], 3)

    return {"p50": percentile(0.50), "p95": percentile(0.95), "max": round(ordered[-1], 3)}


def save_outputs(config, teams, rows, assertions, aggregates, burst_wall_ms, sync_count, success_count, exit_code):
    os.makedirs(config["out_dir"], exist_ok=True)
    output = {
        "date_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "git_commit": git_commit(),
        "mode": "mock" if config["mock"] else "live",
        "config": {key: value for key, value in config.items() if key != "admin_token"},
        "teams": rows,
        "assertions": assertions,
        "aggregates": aggregates,
        "burst_wall_ms": round(burst_wall_ms, 3),
        "gateway_sync_events": sync_count,
        "provisions_succeeded": success_count,
        "exit_code": exit_code,
    }
    with open(os.path.join(config["out_dir"], "results.json"), "w", encoding="utf-8") as stream:
        json.dump(output, stream, indent=2, sort_keys=True)
        stream.write("\n")

    columns = ("team", "provision_status", "provision_wall_ms", "step", "fixed_ip", "server_id", "port_id",
               "hostname", "response_body", *SERVER_TIMING_KEYS, "teardown_status", "teardown_wall_ms",
               "teardown_timings_json", "teardown_body")
    with open(os.path.join(config["out_dir"], "results.csv"), "w", encoding="utf-8", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=columns, lineterminator="\n")
        writer.writeheader()
        for row in rows:
            output_row = {key: row.get(key, "") for key in columns}
            output_row["response_body"] = json.dumps(row.get("response_body"), sort_keys=True)
            output_row["teardown_timings_json"] = json.dumps(row.get("teardown_timings_ms", {}), sort_keys=True)
            output_row["teardown_body"] = json.dumps(row.get("teardown_body"), sort_keys=True)
            writer.writerow(output_row)

    report = [
        "# Concurrent provisioning stress run",
        "",
        f"- Date (UTC): {output['date_utc']}",
        f"- Commit: `{output['git_commit']}`",
        f"- Mode: **{'MOCK — NOT a live benchmark' if config['mock'] else 'LIVE'}**",
        f"- Teams: {config['teams']}; concurrency: {config['concurrency']}",
        f"- Successes: {success_count}/{config['teams']} ({aggregates['success_rate_percent']:.1f}%)",
        f"- Provision burst wall time: {burst_wall_ms:.1f} ms",
        f"- Successful provisions/minute: {aggregates['provisions_per_minute']:.2f}",
        f"- Gateway sync events: {sync_count}; successful provisions: {success_count}; coalescing ratio: {aggregates['sync_coalescing_ratio']}",
        "",
        "## Assertions",
        "",
    ]
    report.extend(f"- **{'SKIPPED' if item['skipped'] else 'PASS' if item['passed'] else 'FAIL'}** — {item['name']}: {item['detail']}" for item in assertions)
    report.extend(["", "## Timing aggregates (ms)", "", "| Timing | p50 | p95 | max |", "|---|---:|---:|---:|"])
    for name, values in aggregates["timings_ms"].items():
        report.append(f"| {name} | {values['p50'] if values['p50'] is not None else 'n/a'} | {values['p95'] if values['p95'] is not None else 'n/a'} | {values['max'] if values['max'] is not None else 'n/a'} |")
    report.extend(["", f"Results saved in `{config['out_dir']}`.", ""])
    with open(os.path.join(config["out_dir"], "summary.md"), "w", encoding="utf-8") as stream:
        stream.write("\n".join(report))
    print(f"Results: {config['out_dir']}/results.json, results.csv, summary.md")


def git_commit():
    try:
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        return subprocess.run(["git", "-C", root, "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


def main():
    api_url = os.environ.get("API_URL", "http://127.0.0.1:8080").rstrip("/")
    parsed_url = urllib.parse.urlparse(api_url)
    if parsed_url.scheme not in ("http", "https") or not parsed_url.netloc or parsed_url.username or parsed_url.password or parsed_url.query or parsed_url.fragment or any(char.isspace() for char in api_url):
        fail("API_URL must be an http(s) URL without embedded credentials")
    token = os.environ.get("ADMIN_TOKEN", "")
    if len(token) < 24 or any(char.isspace() for char in token):
        fail("ADMIN_TOKEN is required and must be at least 24 characters without whitespace")
    teams_count = positive_int("TEAMS", 12, 10, 10000)
    prefix = os.environ.get("PREFIX", "stress")
    if not DNS_LABEL.fullmatch(prefix):
        fail("PREFIX must be a safe lowercase DNS label of 1-63 characters")
    if len(prefix) + len(str(teams_count)) + 6 > 63:
        fail("PREFIX is too long for generated <PREFIX>-<n>-<random4> DNS labels")
    concurrency = positive_int("CONCURRENCY", teams_count, 1, teams_count)
    timeout = positive_int("HTTP_TIMEOUT_SECONDS", 300, 1, 86400)
    teardown = env_bool("TEARDOWN", "true")
    threshold_raw = os.environ.get("SUCCESS_THRESHOLD", "100")
    try:
        threshold = float(threshold_raw)
    except ValueError:
        fail("SUCCESS_THRESHOLD must be a percentage from 0 to 100")
    if not 0 <= threshold <= 100:
        fail("SUCCESS_THRESHOLD must be a percentage from 0 to 100")

    timestamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    out_dir = os.environ.get("OUT_DIR", f"docs/benchmarks/stress-{timestamp}")
    routing_domain = os.environ.get("ROUTING_DOMAIN", "cstam.felcloud.tn")
    if any(not DNS_LABEL.fullmatch(label) for label in routing_domain.split(".")):
        fail("ROUTING_DOMAIN must contain only lowercase DNS labels")
    sandbox_cidr = os.environ.get("SANDBOX_CIDR", "10.20.30.0/24")
    try:
        network = ipaddress.ip_network(sandbox_cidr, strict=True)
    except ValueError as error:
        fail(f"SANDBOX_CIDR is invalid: {error}")
    excluded = set(DEFAULT_EXCLUDED_IPS)
    for variable in ("GATEWAY_IPS", "BASTION_IP", "VIP_IP", "PUBLIC_IP"):
        values = os.environ.get(variable, "")
        excluded.update(item.strip() for item in values.split(",") if item.strip())
    invalid_excluded = [value for value in excluded if not valid_ip(value)]
    if invalid_excluded:
        fail(f"invalid IP in gateway/bastion/VIP exclusion config: {invalid_excluded[0]}")

    rate_raw = os.environ.get("RATE_LIMIT_PER_MIN", "")
    mutating_calls = teams_count * (2 if teardown else 1)
    if rate_raw:
        try:
            rate_limit = int(rate_raw)
        except ValueError:
            rate_limit = 0
        if rate_limit < mutating_calls:
            print(f"WARNING: RATE_LIMIT_PER_MIN={rate_raw} looks too low for {mutating_calls} mutating calls; raise it on the server.")
    elif mutating_calls > 30:
        print(f"WARNING: default RATE_LIMIT_PER_MIN=30 is below {mutating_calls} mutating calls; raise it on the server.")

    config = {
        "api_url": api_url,
        "admin_token": token,
        "teams": teams_count,
        "prefix": prefix,
        "concurrency": concurrency,
        "http_timeout_seconds": timeout,
        "teardown": teardown,
        "success_threshold_percent": threshold,
        "out_dir": out_dir,
        "routing_domain": routing_domain,
        "sandbox_cidr": str(network),
        "rate_limit_per_min": os.environ.get("RATE_LIMIT_PER_MIN", "30"),
        "vip_ip": os.environ.get("VIP_IP", ""),
        "public_ip": os.environ.get("PUBLIC_IP", ""),
        "mock": os.environ.get("PROVISIONER", "") == "mock",
    }
    status, _, body, parsed = api_request(config, "GET", "/health")
    if status != 200:
        fail(f"preflight GET /health returned HTTP {status}: {body}")
    status, _, body, parsed = api_request(config, "GET", "/api/sandboxes")
    if status != 200:
        fail(f"preflight authenticated GET /api/sandboxes returned HTTP {status}; check ADMIN_TOKEN and API_URL: {body}")
    print(f"Preflight PASS: /health and authenticated /api/sandboxes at {api_url}")

    teams = []
    for number in range(1, teams_count + 1):
        suffix = "".join(random.SystemRandom().choice("abcdefghijklmnopqrstuvwxyz0123456789") for _ in range(4))
        team = f"{prefix}-{number}-{suffix}"
        if len(team) > 63 or not DNS_LABEL.fullmatch(team):
            fail(f"generated team name is not a DNS label: {team}")
        teams.append(team)

    print(f"Starting {teams_count} simultaneous provisions (concurrency={concurrency})...")
    provision_results, burst_wall_ms = parallel_requests(
        config, teams, "POST", lambda _team: "/api/sandboxes/provision", lambda team: {"team": team}
    )
    rows = []
    for team in teams:
        status, elapsed, response_body, parsed = provision_results[team]
        payload = json_body(parsed)
        timings = payload.get("timings_ms") if isinstance(payload.get("timings_ms"), dict) else {}
        row = {
            "team": team,
            "provision_status": status,
            "provision_wall_ms": round(elapsed, 3),
            "step": payload.get("step", ""),
            "fixed_ip": payload.get("fixed_ip", ""),
            "server_id": payload.get("server_id", ""),
            "port_id": payload.get("port_id", ""),
            "hostname": payload.get("hostname", ""),
            "response_body": payload if parsed is not None else response_body,
            "timings_ms": {key: timings.get(key) for key in TIMING_KEYS},
            "teardown_status": "",
            "teardown_wall_ms": "",
            "teardown_body": None,
        }
        row.update({key: timings.get(key, "") for key in TIMING_KEYS})
        rows.append(row)

    successful = [row for row in rows if row["provision_status"] == 201]
    by_team = {row["team"]: row for row in rows}
    assertions = []
    errors = {}
    for row in rows:
        if row["provision_status"] != 201:
            key = f"step={row['step'] or 'unknown'} status={row['provision_status']}"
            errors[key] = errors.get(key, 0) + 1
    assert_result(assertions, "success count", len(successful) == teams_count,
                  f"{len(successful)}/{teams_count}; failures by step/status: {errors or 'none'}")

    ips = [row["fixed_ip"] for row in successful]
    ip_valid = all(valid_ip(ip) and ipaddress.ip_address(ip) in network and ip not in excluded for ip in ips)
    assert_result(assertions, "unique valid sandbox IPs", len(set(ips)) == len(ips) and ip_valid,
                  f"{len(set(ips))}/{len(ips)} unique; subnet={network}; excluded addresses checked")
    server_ids = [row["server_id"] for row in successful]
    port_ids = [row["port_id"] for row in successful]
    resource_ids_valid = all(server_ids) and all(port_ids) and len(set(server_ids)) == len(server_ids) and len(set(port_ids)) == len(port_ids)
    assert_result(assertions, "unique server_id and port_id values", resource_ids_valid,
                  f"servers={len(set(server_ids))}/{len(server_ids)}, ports={len(set(port_ids))}/{len(port_ids)}")

    status, _, lease_text, lease_data = api_request(config, "GET", "/ipam/leases")
    leases = lease_data.get("leases", []) if isinstance(lease_data, dict) else []
    lease_map = {}
    for lease in leases if isinstance(leases, list) else []:
        lease_map.setdefault(lease.get("entity_name"), []).append(lease)
    lease_ok = status == 200
    for row in successful:
        owned = lease_map.get(row["team"], [])
        if len(owned) != 1 or owned[0].get("status") != "ALLOCATED" or owned[0].get("ip_address") != row["fixed_ip"]:
            lease_ok = False
    allocated_ips = [lease.get("ip_address") for lease in leases if lease.get("status") == "ALLOCATED"]
    lease_ok = lease_ok and len(allocated_ips) == len(set(allocated_ips))
    assert_result(assertions, "one allocated lease per successful team", lease_ok,
                  f"lease endpoint HTTP {status}; checked {len(successful)} successful teams and duplicate allocated IPs")

    if os.environ.get("VIP_IP") or os.environ.get("PUBLIC_IP"):
        target_ip = os.environ.get("VIP_IP") or os.environ.get("PUBLIC_IP")
        routable = valid_ip(target_ip)
        curl = shutil.which("curl")
        if not routable or not curl:
            assert_result(assertions, "successful hostnames respond via VIP/PUBLIC IP", False,
                          "VIP_IP/PUBLIC_IP is invalid or curl is unavailable")
        else:
            probe_failures = []
            for row in successful:
                hostname = f"{row['team']}.{routing_domain}"
                command = [curl, "--silent", "--show-error", "--output", "/dev/null", "--write-out", "%{http_code}",
                           "--max-time", str(timeout), "--resolve", f"{hostname}:80:{target_ip}", f"http://{hostname}/"]
                try:
                    response = subprocess.run(command, check=False, capture_output=True, text=True)
                    code = response.stdout.strip()
                    if response.returncode != 0 or not code[:1] in ("2", "3"):
                        probe_failures.append(f"{hostname}={code or response.stderr.strip()}")
                except OSError as error:
                    probe_failures.append(f"{hostname}={error}")
            assert_result(assertions, "successful hostnames respond via VIP/PUBLIC IP", not probe_failures,
                          f"probed={len(successful)}; failures={probe_failures or 'none'}")
    else:
        assert_result(assertions, "successful hostnames respond via VIP/PUBLIC IP", True,
                      "Set VIP_IP or PUBLIC_IP to enable routed HTTP verification", skipped=True)

    status, _, sandbox_text, sandbox_data = api_request(config, "GET", "/api/sandboxes")
    sandboxes = sandbox_data.get("sandboxes", []) if isinstance(sandbox_data, dict) else []
    provisioning = [box.get("sandbox_id") for box in sandboxes if box.get("sandbox_id") in teams and box.get("status") == "PROVISIONING"]
    assert_result(assertions, "no sandbox remains PROVISIONING", status == 200 and not provisioning,
                  f"endpoint HTTP {status}; stuck={provisioning}")

    status, _, event_text, event_data = api_request(config, "GET", "/api/events")
    events = event_data if isinstance(event_data, list) else []
    run_team_set = set(teams)
    sync_count = sum(1 for event in events if str(event.get("type", "")).upper() == "SYNC" and event.get("sandbox_id") in run_team_set)
    assert_result(assertions, "gateway sync events observed", status == 200,
                  f"{sync_count} SYNC events versus {len(successful)} successful provisions (HTTP {status})")

    if teardown and successful:
        print(f"Starting parallel teardown of {len(successful)} successful provisions...")
        teardown_results, _ = parallel_requests(
            config, [row["team"] for row in successful], "DELETE",
            lambda team: "/api/sandboxes/" + urllib.parse.quote(team, safe=""),
        )
        for team, result in teardown_results.items():
            status, elapsed, body, parsed = result
            row = by_team[team]
            row["teardown_status"] = status
            row["teardown_wall_ms"] = round(elapsed, 3)
            row["teardown_body"] = json_body(parsed) if parsed is not None else body
            teardown_timings = json_body(parsed).get("timings_ms", {})
            row["teardown_ms"] = elapsed
            row["teardown_timings_ms"] = teardown_timings

        teardown_status_ok = all(by_team[team]["teardown_status"] == 200 for team in by_team if by_team[team] in successful)
        assert_result(assertions, "teardown requests succeeded", teardown_status_ok,
                      f"{sum(1 for row in successful if row['teardown_status'] == 200)}/{len(successful)} returned 200")

        deadline = time.monotonic() + 120
        quarantined = False
        status = 0
        while time.monotonic() < deadline:
            status, _, _, lease_data = api_request(config, "GET", "/ipam/leases")
            leases = lease_data.get("leases", []) if isinstance(lease_data, dict) else []
            lease_map = {}
            for lease in leases if isinstance(leases, list) else []:
                lease_map.setdefault(lease.get("entity_name"), []).append(lease)
            quarantined = status == 200 and all(
                len(lease_map.get(row["team"], [])) == 1 and lease_map[row["team"]][0].get("status") == "QUARANTINED"
                for row in successful
            )
            if quarantined:
                break
            time.sleep(0.2)
        assert_result(assertions, "leases enter QUARANTINED", quarantined,
                      f"endpoint HTTP {status}; verified {len(successful)} leases")

        free = False
        released = False
        while time.monotonic() < deadline:
            status, _, _, lease_data = api_request(config, "GET", "/ipam/leases")
            leases = lease_data.get("leases", []) if isinstance(lease_data, dict) else []
            lease_map = {}
            for lease in leases if isinstance(leases, list) else []:
                lease_map.setdefault(lease.get("entity_name"), []).append(lease)
            free = status == 200 and all(
                len(lease_map.get(row["team"], [])) == 1 and lease_map[row["team"]][0].get("status") == "FREE"
                for row in successful
            )
            status, _, _, sandbox_data = api_request(config, "GET", "/api/sandboxes")
            sandboxes = sandbox_data.get("sandboxes", []) if isinstance(sandbox_data, dict) else []
            box_map = {box.get("sandbox_id"): box for box in sandboxes}
            released = status == 200 and all(box_map.get(row["team"], {}).get("status") == "RELEASED" for row in successful)
            if free and released:
                break
            time.sleep(2)
        assert_result(assertions, "quarantined leases become FREE", free,
                      f"completed within 120 seconds={free}")
        assert_result(assertions, "torn-down sandboxes are RELEASED", released,
                      f"all {len(successful)} states RELEASED={released}")

        if not config["mock"] and shutil.which("openstack") and os.environ.get("OS_CLOUD"):
            for object_type in ("server", "port"):
                command = ["openstack", object_type, "list", "--name", "sandbox-" + prefix, "-f", "value", "-c", "ID"]
                try:
                    result = subprocess.run(command, check=False, capture_output=True, text=True, timeout=timeout)
                    assert_result(assertions, f"OpenStack {object_type} cleanup", result.returncode == 0 and not result.stdout.strip(),
                                  f"remaining matching rows: {result.stdout.strip() or 'none'}; stderr={result.stderr.strip()}")
                except (OSError, subprocess.TimeoutExpired) as error:
                    assert_result(assertions, f"OpenStack {object_type} cleanup", False, str(error))
        else:
            reason = "mock mode does not query OpenStack" if config["mock"] else "openstack CLI or OS_CLOUD unavailable"
            assert_result(assertions, "OpenStack server/port cleanup", True, reason, skipped=True)
    else:
        assert_result(assertions, "teardown phase", not teardown or not successful,
                      "TEARDOWN=false or no provisions succeeded", skipped=True)

    for row in rows:
        if row["provision_status"] != 201 or (teardown and row["provision_status"] == 201 and row["teardown_status"] != 200):
            cleanup_commands([row["team"]], api_url)

    success_rate = 100 * len(successful) / teams_count
    timing_values = {}
    for key in (*TIMING_KEYS, "teardown_ms"):
        values = []
        for row in rows:
            value = row.get(key)
            if isinstance(value, (int, float)):
                values.append(float(value))
        timing_values[key] = summarize_percentiles(values)
    sync_ratio = round(len(successful) / sync_count, 3) if sync_count else None
    aggregates = {
        "success_rate_percent": success_rate,
        "timings_ms": timing_values,
        "provisions_per_minute": round(len(successful) * 60000 / burst_wall_ms, 3) if burst_wall_ms > 0 else 0,
        "ip_uniqueness": len(ips) == len(set(ips)),
        "sync_coalescing_ratio": sync_ratio,
        "sync_coalescing_savings_percent": round(100 * (1 - sync_count / len(successful)), 2) if successful else None,
    }
    threshold_passed = success_rate >= threshold
    assert_result(assertions, "success rate threshold", threshold_passed,
                  f"{success_rate:.1f}% >= {threshold:.1f}%")
    failures = [item for item in assertions if not item["passed"]]
    exit_code = 1 if failures else 0
    save_outputs(config, teams, rows, assertions, aggregates, burst_wall_ms, sync_count, len(successful), exit_code)
    return exit_code


def valid_ip(value):
    try:
        return isinstance(value, str) and ipaddress.ip_address(value).version == 4
    except ValueError:
        return False


if __name__ == "__main__":
    raise SystemExit(main())
