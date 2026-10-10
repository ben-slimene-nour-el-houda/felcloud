#!/usr/bin/env python3
"""Measure IP quarantine and reuse through the control-plane API."""
import datetime
import json
import os
import random
import re
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


DNS_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
POLL_SECONDS = 0.25
MAX_WAIT_SECONDS = 120


def fail(message):
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(2)


def api_request(config, method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    headers = {"Authorization": "Bearer " + config["token"], "Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(config["api_url"] + path, data=data, headers=headers, method=method)
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=config["timeout"]) as response:
            raw, status = response.read(), response.status
    except urllib.error.HTTPError as error:
        raw, status = error.read(), error.code
    except (urllib.error.URLError, TimeoutError, OSError) as error:
        raw, status = str(error).encode(), 0
    elapsed = (time.monotonic() - started) * 1000
    text = raw.decode("utf-8", errors="replace")
    try:
        parsed = json.loads(text)
    except (json.JSONDecodeError, UnicodeDecodeError):
        parsed = None
    return status, elapsed, text, parsed


def sandbox_rows(config, team):
    status, _, body, payload = api_request(config, "GET", "/api/sandboxes")
    if status != 200 or not isinstance(payload, dict):
        raise RuntimeError(f"GET /api/sandboxes failed ({status}): {body}")
    return [row for row in payload.get("sandboxes", [])
            if isinstance(row, dict) and row.get("sandbox_id") == team]


def lease_rows(config, team):
    status, _, body, payload = api_request(config, "GET", "/ipam/leases")
    if status != 200 or not isinstance(payload, dict):
        raise RuntimeError(f"GET /ipam/leases failed ({status}): {body}")
    return [row for row in payload.get("leases", [])
            if isinstance(row, dict) and row.get("entity_name") == team]


def wait_for(config, team, predicate, description):
    deadline = time.monotonic() + MAX_WAIT_SECONDS
    while time.monotonic() <= deadline:
        rows = lease_rows(config, team)
        matching = [row for row in rows if predicate(row)]
        if matching:
            return matching[0], time.monotonic()
        time.sleep(POLL_SECONDS)
    raise RuntimeError(f"timed out after {MAX_WAIT_SECONDS}s waiting for {team} {description}")


def lease_cooldown_seconds(lease):
    try:
        fmt = "%Y-%m-%d %H:%M:%S"
        quarantined_at = datetime.datetime.strptime(lease["quarantined_at"], fmt)
        quarantine_until = datetime.datetime.strptime(lease["quarantine_until"], fmt)
    except (KeyError, TypeError, ValueError):
        return None
    return int((quarantine_until - quarantined_at).total_seconds())


def post_provision(config, team):
    status, elapsed, body, payload = api_request(config, "POST", "/api/sandboxes/provision", {"team": team})
    if status != 201 or not isinstance(payload, dict) or not payload.get("fixed_ip"):
        raise RuntimeError(f"provision {team} failed with HTTP {status}: {body}")
    return payload, elapsed


def delete_sandbox(config, team):
    status, elapsed, body, payload = api_request(
        config, "DELETE", "/api/sandboxes/" + urllib.parse.quote(team, safe="")
    )
    if status != 200:
        raise RuntimeError(f"DELETE {team} failed with HTTP {status}: {body}")
    return payload, elapsed


def allocate(config, team, preferred_ip=None):
    payload = {"sandbox_id": team}
    if preferred_ip:
        payload["ip_address"] = preferred_ip
    status, elapsed, body, result = api_request(config, "POST", "/ipam/leases/allocate", payload)
    if status != 201 or not isinstance(result, dict) or not result.get("ip_address"):
        raise RuntimeError(f"allocate {team} failed with HTTP {status}: {body}")
    return result, elapsed


def release(config, team, ip):
    status, _, body, payload = api_request(
        config, "POST", "/ipam/leases/release", {"sandbox_id": team, "ip_address": ip}
    )
    if status != 200 or not isinstance(payload, dict) or payload.get("status") != "QUARANTINED":
        raise RuntimeError(f"release {team} failed with HTTP {status}: {body}")


def make_team(prefix, suffix):
    team = f"{prefix}-rc-{suffix}-{''.join(random.SystemRandom().choice('abcdefghijklmnopqrstuvwxyz0123456789') for _ in range(4))}"
    if len(team) > 63 or not DNS_LABEL.fullmatch(team):
        fail("generated team name exceeds the lowercase DNS-label rule (max 63 characters)")
    return team


def main():
    api_url = os.environ.get("API_URL", "http://127.0.0.1:8080").strip()
    token = os.environ.get("ADMIN_TOKEN", "")
    prefix = os.environ.get("PREFIX", "stress")
    if not token or len(token) < 24 or any(char.isspace() for char in token):
        fail("ADMIN_TOKEN must be at least 24 characters and contain no whitespace")
    if not DNS_LABEL.fullmatch(prefix):
        fail("PREFIX must be a safe lowercase DNS label (1-63 characters)")
    parsed = urllib.parse.urlsplit(api_url)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password or parsed.query or parsed.fragment or any(c.isspace() for c in api_url):
        fail("API_URL must be an http(s) origin/path without credentials, query, fragment, or whitespace")
    try:
        cycles = int(os.environ.get("RECYCLE_CYCLES", "10"), 10)
        timeout = int(os.environ.get("HTTP_TIMEOUT_SECONDS", "300"), 10)
        cooldown = int(os.environ.get("IP_QUARANTINE_SECONDS", "60"), 10)
    except ValueError:
        fail("RECYCLE_CYCLES, HTTP_TIMEOUT_SECONDS, and IP_QUARANTINE_SECONDS must be integers")
    if not 1 <= cycles <= 1000 or not 1 <= timeout <= 3600 or not 1 <= cooldown <= 3600:
        fail("RECYCLE_CYCLES must be 1-1000; timeout and quarantine cooldown must be 1-3600")

    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    out_dir = os.environ.get("OUT_DIR", os.path.join(root, "docs", "benchmarks", f"recycle-{stamp}"))
    os.makedirs(out_dir, exist_ok=True)
    config = {"api_url": api_url.rstrip("/"), "token": token, "timeout": timeout}
    rate_raw = os.environ.get("RATE_LIMIT_PER_MIN", "30")
    try:
        rate_limit = int(rate_raw, 10)
    except ValueError:
        rate_limit = 0
    expected_mutations = cycles * 6
    if rate_limit < expected_mutations:
        print(f"WARNING: RATE_LIMIT_PER_MIN={rate_raw} looks low for up to {expected_mutations} "
              "mutating calls; raise it on the server for this run.")
    result = {
        "schema_version": 1,
        "date_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "git_commit": git_commit(root),
        "mode": "mock" if os.environ.get("PROVISIONER", "") == "mock" else "live",
        "config": {
            "cycles": cycles, "prefix": prefix, "quarantine_cooldown_seconds": cooldown,
            "quarantine_cooldown_source": "IP_QUARANTINE_SECONDS environment; API does not expose this setting",
            "api_url": api_url, "poll_interval_ms": int(POLL_SECONDS * 1000),
            "max_wait_seconds": MAX_WAIT_SECONDS,
        },
        "cycles": [],
    }
    for path in ("/health", "/api/sandboxes"):
        status, _, body, _ = api_request(config, "GET", path)
        if status != 200:
            fail(f"preflight GET {path} returned HTTP {status}: {body}")
    print(f"Preflight PASS: /health and authenticated /api/sandboxes at {api_url}")
    print(f"Configured quarantine cooldown: {cooldown}s (source: IP_QUARANTINE_SECONDS)")

    overall_ok = True
    for number in range(1, cycles + 1):
        original = make_team(prefix, number)
        immediate = make_team(prefix, f"{number}-now")
        reused = make_team(prefix, f"{number}-reuse")
        row = {"cycle": number, "team": original, "assertions": []}
        cleanup_teams = []
        try:
            cleanup_teams.append(original)
            original_data, row["provision_ms"] = post_provision(config, original)
            if str(original_data.get("server_id", "")).startswith("mock-") or \
                    str(original_data.get("port_id", "")).startswith("mock-"):
                result["mode"] = "mock"
            original_ip = original_data["fixed_ip"]
            _, row["delete_ms"] = delete_sandbox(config, original)
            delete_done = time.monotonic()
            quarantined, seen_quarantined = wait_for(
                config, original,
                lambda lease: lease.get("status") in ("QUARANTINED", "FREE"), "QUARANTINED or already FREE"
            )
            if quarantined.get("status") != "QUARANTINED":
                raise RuntimeError("lease reached FREE before QUARANTINED could be observed")
            actual_cooldown = lease_cooldown_seconds(quarantined)
            if actual_cooldown is not None:
                if result["config"]["quarantine_cooldown_source"] != "lease timestamps from GET /ipam/leases":
                    result["config"]["quarantine_cooldown_seconds"] = actual_cooldown
                    result["config"]["quarantine_cooldown_source"] = "lease timestamps from GET /ipam/leases"
                elif result["config"]["quarantine_cooldown_seconds"] != actual_cooldown:
                    raise RuntimeError("server-reported quarantine cooldown changed during the run")
            expected_wait_ms = cooldown * 1000
            if quarantined.get("quarantine_until"):
                try:
                    deadline = datetime.datetime.strptime(
                        quarantined["quarantine_until"], "%Y-%m-%d %H:%M:%S"
                    ).replace(tzinfo=datetime.timezone.utc)
                    expected_wait_ms = max(
                        0,
                        (deadline - datetime.datetime.now(datetime.timezone.utc)).total_seconds() * 1000,
                    )
                except (TypeError, ValueError):
                    pass
            row["ip_address"] = original_ip
            row["delete_to_quarantined_ms"] = round((seen_quarantined - delete_done) * 1000, 3)
            row["quarantine_cooldown_seconds"] = actual_cooldown or cooldown

            cleanup_teams.append(immediate)
            immediate_data, row["immediate_reprovision_ms"] = post_provision(config, immediate)
            row["immediate_ip"] = immediate_data["fixed_ip"]
            immediate_ok = row["immediate_ip"] != original_ip
            row["assertions"].append({"name": "immediate reprovision uses a different IP", "passed": immediate_ok})
            _, row["immediate_delete_ms"] = delete_sandbox(config, immediate)
            if not immediate_ok:
                raise RuntimeError(f"quarantined IP {original_ip} was immediately reused")

            freed, seen_free = wait_for(config, original, lambda lease: lease.get("status") == "FREE", "FREE")
            row["quarantined_to_free_ms"] = round((seen_free - seen_quarantined) * 1000, 3)
            row["observed_cooldown_ms"] = round((seen_free - delete_done) * 1000, 3)
            row["lease_free"] = freed.get("status") == "FREE"
            cooldown_delta = abs(row["quarantined_to_free_ms"] - expected_wait_ms)
            cooldown_ok = cooldown_delta <= 1250
            row["assertions"].append({
                "name": "observed cooldown matches configured cooldown",
                "passed": cooldown_ok,
                "detail": f"observed wait {row['quarantined_to_free_ms']}ms; expected remaining wait "
                          f"{round(expected_wait_ms, 3)}ms; cooldown {row['quarantine_cooldown_seconds']}s",
            })
            if not cooldown_ok:
                raise RuntimeError(
                    f"observed wait {row['quarantined_to_free_ms']}ms differs from expected remaining "
                    f"cooldown {round(expected_wait_ms, 3)}ms"
                )

            cleanup_teams.append(reused)
            allocated, row["same_ip_allocation_ms"] = allocate(config, reused, original_ip)
            row["reused_team"] = reused
            row["reused_ip"] = allocated["ip_address"]
            row["delete_to_same_ip_allocated_ms"] = round((time.monotonic() - delete_done) * 1000, 3)
            reuse_ok = row["reused_ip"] == original_ip
            row["assertions"].append({"name": "same IP can be allocated after quarantine", "passed": reuse_ok})
            if not reuse_ok:
                raise RuntimeError(f"preferred IP {original_ip} was not reallocated; received {row['reused_ip']}")
            release(config, reused, original_ip)

            state_ok = True
            for team in cleanup_teams + [reused]:
                states = sandbox_rows(config, team)
                team_leases = lease_rows(config, team)
                state_ok = state_ok and len(states) == 1 and states[0].get("status") == "RELEASED"
                state_ok = state_ok and not any(item.get("status") == "ALLOCATED" for item in team_leases)
            row["assertions"].append({
                "name": "no allocated lease or unreleased sandbox rows left behind",
                "passed": state_ok,
                "detail": "sandbox rows RELEASED; leases are QUARANTINED or FREE and will expire normally",
            })
            if not state_ok:
                raise RuntimeError("one or more generated teams retained an allocated lease or unreleased sandbox row")
        except Exception as error:
            overall_ok = False
            row["error"] = str(error)
            print(f"FAIL: cycle {number}: {error}", file=sys.stderr)
            print("PREFIX-scoped cleanup commands for generated names only:", file=sys.stderr)
            for team in cleanup_teams:
                cleanup_url = shlex.quote(api_url.rstrip("/") + "/api/sandboxes/" + team)
                print(f'  curl --fail-with-body -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" '
                      f"{cleanup_url}", file=sys.stderr)
        row["passed"] = not row.get("error") and all(assertion["passed"] for assertion in row["assertions"])
        result["cycles"].append(row)
        print(f"{'PASS' if row['passed'] else 'FAIL'}: cycle {number}/{cycles}; "
              f"IP {row.get('ip_address', 'unknown')}; quarantine-to-free "
              f"{row.get('quarantined_to_free_ms', 'n/a')}ms; same-IP reuse "
              f"{row.get('delete_to_same_ip_allocated_ms', 'n/a')}ms")

    result["passed"] = overall_ok and all(cycle["passed"] for cycle in result["cycles"])
    write_results(out_dir, result)
    print(f"Results: {out_dir}/results.json, summary.md")
    if not result["passed"]:
        raise SystemExit(1)


def write_results(out_dir, result):
    with open(os.path.join(out_dir, "results.json"), "w", encoding="utf-8") as stream:
        json.dump(result, stream, indent=2, sort_keys=True)
        stream.write("\n")
    lines = [
        "# IP recycling benchmark", "",
        f"- Date (UTC): {result['date_utc']}",
        f"- Commit: `{result['git_commit']}`",
        f"- Mode: **{'MOCK — NOT a live benchmark' if result['mode'] == 'mock' else 'LIVE'}**",
        f"- Quarantine cooldown: {result['config']['quarantine_cooldown_seconds']}s "
        f"({result['config']['quarantine_cooldown_source']})",
        f"- Cycles passed: {sum(cycle['passed'] for cycle in result['cycles'])}/{len(result['cycles'])}",
        "", "| Cycle | IP | DELETE→QUARANTINED (ms) | QUARANTINED→FREE (ms) | DELETE→same IP allocated (ms) | Result |",
        "|---:|---|---:|---:|---:|---|",
    ]
    for cycle in result["cycles"]:
        lines.append(f"| {cycle['cycle']} | {cycle.get('ip_address', 'n/a')} | "
                     f"{cycle.get('delete_to_quarantined_ms', 'n/a')} | "
                     f"{cycle.get('quarantined_to_free_ms', 'n/a')} | "
                     f"{cycle.get('delete_to_same_ip_allocated_ms', 'n/a')} | "
                     f"{'PASS' if cycle['passed'] else 'FAIL'} |")
    lines.extend(["", "## Assertions", ""])
    for cycle in result["cycles"]:
        for assertion in cycle["assertions"]:
            lines.append(f"- **{'PASS' if assertion['passed'] else 'FAIL'}** — cycle {cycle['cycle']}: {assertion['name']}")
        if cycle.get("error"):
            lines.append(f"- **FAIL** — cycle {cycle['cycle']}: {cycle['error']}")
    lines.extend(["", "Measurements poll at 250 ms intervals, with a 120 second maximum wait. "
                  "The observed quarantine-to-free duration includes polling granularity and API request time.", ""])
    with open(os.path.join(out_dir, "summary.md"), "w", encoding="utf-8") as stream:
        stream.write("\n".join(lines))


def git_commit(root):
    try:
        return subprocess.run(["git", "-C", root, "rev-parse", "HEAD"], check=True,
                              capture_output=True, text=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


if __name__ == "__main__":
    main()
