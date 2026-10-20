#!/usr/bin/env python3
"""Launch a small live batch of sandboxes and check gateway routing."""
import argparse
import asyncio
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import time

import httpx


HERE = Path(__file__).resolve().parent
STATE_FILE = HERE / ".last_run.json"
API_PROVISION = "/api/sandboxes/provision"
API_LIST = "/api/sandboxes"


def utc_now():
    return datetime.now(timezone.utc)


def stamp():
    return utc_now().strftime("%Y%m%dT%H%M%SZ")


def fail_config(names):
    for name in names:
        if not os.environ.get(name):
            print(name)
            return 2
    return 0


def response_data(response):
    try:
        data = response.json()
    except ValueError:
        data = {"raw": response.text}
    return data if isinstance(data, dict) else {"data": data}


async def run(args, token, vip, domain, base_url):
    run_stamp = stamp()
    ids = [f"{args.prefix}-{run_stamp}-{i:02d}" for i in range(1, args.count + 1)]
    if args.dry_run:
        print("Planned sandbox IDs:")
        for sandbox_id in ids:
            print(f"  {sandbox_id}")
        print(f"Request: POST {API_PROVISION}")
        print("Headers: Authorization: Bearer <from ADMIN_TOKEN>")
        print(json.dumps({"team": ids[0], "ttl": 3600,
                          "custom_domain": f"{ids[0]}.{domain}"}, indent=2))
        print(f"Probe URL: http://{vip}/")
        print(f"Host: <sandbox_id>.{domain}")
        print("Limits: max 15 sandboxes; probes globally at most 1 per second;")
        print("         no API polling; provision timeout from --timeout; probe limit 60 s.")
        return 0

    if args.cleanup:
        if not STATE_FILE.is_file():
            print(f"FAILED cleanup: state file missing: {STATE_FILE}")
            return 1
        try:
            entries = json.loads(STATE_FILE.read_text(encoding="utf-8"))
            ids = [str(item["sandbox_id"]) for item in entries["sandboxes"]]
        except (OSError, ValueError, KeyError, TypeError) as exc:
            print(f"FAILED cleanup state: {type(exc).__name__}: {exc}")
            return 1
        if any(not value.startswith("bench-") for value in ids):
            print("FAILED cleanup: state contains an ID without bench- prefix")
            return 1
        async with httpx.AsyncClient(base_url=base_url, timeout=args.timeout,
                                     headers={"Authorization": f"Bearer {token}"}) as client:
            failures = 0
            for sandbox_id in ids:
                try:
                    response = await client.delete(f"/api/sandboxes/{sandbox_id}")
                    print(f"Cleanup {sandbox_id}: HTTP {response.status_code}")
                    if response.status_code not in (200, 202, 204, 404):
                        failures += 1
                        print(f"FAILED {sandbox_id}: {response.text}")
                except httpx.RequestError as exc:
                    failures += 1
                    print(f"FAILED {sandbox_id}: {type(exc).__name__}: {exc}")
        return 1 if failures else 0

    provision_timeout = httpx.Timeout(args.timeout)
    async with httpx.AsyncClient(base_url=base_url, timeout=provision_timeout,
                                 headers={"Authorization": f"Bearer {token}"}) as api, \
            httpx.AsyncClient(timeout=5.0, follow_redirects=True) as probe_client:
        start_at = time.perf_counter()
        print(f"Launching {args.count} simultaneous registrations at "
              f"{utc_now().strftime('%H:%M:%S')}")
        barrier = asyncio.Event()
        records = {sandbox_id: {"sandbox_id": sandbox_id, "private_ip": "",
                                "provisioned_at_utc": "", "provision_status": 0,
                                "elapsed": 0.0, "reachable": False, "gateway": ""}
                   for sandbox_id in ids}
        errors = []
        rate_counts = {"429": 0, "connection": 0}
        probe_lock = asyncio.Lock()
        next_probe = 0.0
        listing_lock = asyncio.Lock()
        listing_task = None

        async def sandbox_listing():
            nonlocal listing_task
            async with listing_lock:
                if listing_task is None:
                    listing_task = asyncio.create_task(api.get(API_LIST))
            return await listing_task

        async def provision(sandbox_id):
            await barrier.wait()
            began = time.perf_counter()
            try:
                response = await api.post(API_PROVISION, json={
                    "team": sandbox_id, "ttl": 3600,
                    "custom_domain": f"{sandbox_id}.{domain}",
                })
            except httpx.ConnectError as exc:
                rate_counts["connection"] += 1
                errors.append(f"FAILED {sandbox_id}: {type(exc).__name__}: {exc}")
                print(f"{sandbox_id:<38} -- CONNECTION ERROR")
                return
            except httpx.RequestError as exc:
                errors.append(f"FAILED {sandbox_id}: {type(exc).__name__}: {exc}")
                print(f"{sandbox_id:<38} -- REQUEST ERROR: {exc}")
                return
            elapsed = time.perf_counter() - began
            data = response_data(response)
            record = records[sandbox_id]
            record["provision_status"] = response.status_code
            record["elapsed"] = elapsed
            if response.status_code == 429:
                rate_counts["429"] += 1
            if response.status_code not in (200, 201):
                errors.append(f"FAILED {sandbox_id}: HTTP {response.status_code}: "
                              f"{data.get('error') or data.get('detail') or data}")
                print(f"{sandbox_id:<38} -- HTTP {response.status_code:3d} "
                      f"{elapsed:7.1f} s")
                return
            record["provisioned_at_utc"] = utc_now().isoformat().replace("+00:00", "Z")
            record["private_ip"] = str(data.get("fixed_ip", ""))
            if not record["private_ip"]:
                listing = await sandbox_listing()
                if listing.status_code == 429:
                    rate_counts["429"] += 1
                if listing.status_code != 200:
                    errors.append(f"FAILED {sandbox_id}: IP missing in provision response; "
                                  f"GET {API_LIST} HTTP {listing.status_code}: "
                                  f"{response_data(listing)}")
                    return
                rows = response_data(listing).get("sandboxes", [])
                match = next((row for row in rows if row.get("sandbox_id") == sandbox_id), {})
                record["private_ip"] = str(match.get("private_ip", ""))
            print(f"{sandbox_id:<38} {record['private_ip'] or '--':<15} "
                  f"HTTP {response.status_code:3d} {elapsed:7.1f} s")
            if not record["private_ip"]:
                errors.append(f"FAILED {sandbox_id}: no private IP in provision or listing")
                return
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                nonlocal next_probe
                async with probe_lock:
                    delay = max(0.0, next_probe - time.monotonic())
                    if delay:
                        await asyncio.sleep(delay)
                    # Reserve room for the initial provision burst under
                    # the 60-request rolling-minute ceiling.
                    next_probe = time.monotonic() + 1.4
                host = f"{sandbox_id}.{domain}"
                try:
                    probe_response = await probe_client.get(
                        f"http://{vip}/", headers={"Host": host})
                    if probe_response.status_code == 429:
                        rate_counts["429"] += 1
                    gateway = probe_response.headers.get("X-Gateway-ID", "")
                    print(f"  Probe {sandbox_id:<32} HTTP {probe_response.status_code:3d} "
                          f"X-Gateway-ID={gateway or '--'}")
                    if probe_response.status_code == 200 and gateway:
                        record["reachable"] = True
                        record["gateway"] = gateway
                        return
                except httpx.ConnectError as exc:
                    rate_counts["connection"] += 1
                    last_error = f"{type(exc).__name__}: {exc}"
                except httpx.RequestError as exc:
                    last_error = f"{type(exc).__name__}: {exc}"
                await asyncio.sleep(max(0.0, min(1.0, deadline - time.monotonic())))
            errors.append(f"FAILED {sandbox_id}: gateway probe timed out after 60 s" +
                          (f"; last error: {last_error}" if "last_error" in locals() else ""))

        tasks = [asyncio.create_task(provision(sandbox_id)) for sandbox_id in ids]
        barrier.set()
        await asyncio.gather(*tasks)

    ip_values = [row["private_ip"] for row in records.values() if row["private_ip"]]
    duplicates = sorted(ip for ip in set(ip_values) if ip_values.count(ip) > 1)
    provisioned = sum(row["provision_status"] in (200, 201) for row in records.values())
    reachable = sum(row["reachable"] for row in records.values())
    gateways = sorted({row["gateway"] for row in records.values() if row["gateway"]})
    longest = max((row["elapsed"] for row in records.values()), default=0.0)
    print(f"Result: {provisioned}/{args.count} provisioned, "
          f"{reachable}/{args.count} reachable via gateway")
    print(f"Distinct private IPs: {len(set(ip_values))} (duplicates: "
          f"{', '.join(duplicates) if duplicates else 'none'})")
    print(f"Gateways seen: {', '.join(gateways) if gateways else 'none'}")
    print(f"Longest provision: {longest:.1f} s")
    print(f"HTTP 429 responses: {rate_counts['429']}; connection errors: "
          f"{rate_counts['connection']}")
    for error in errors:
        print(error)
    STATE_FILE.write_text(json.dumps({"sandboxes": list(records.values()),
                                     "run_started_at_utc": utc_now().isoformat().replace("+00:00", "Z")},
                                    indent=2) + "\n", encoding="utf-8")
    success = (provisioned == args.count and reachable == args.count and
               len(ip_values) == args.count and not duplicates and time.perf_counter() >= start_at)
    return 0 if success else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--count", type=int, default=10)
    parser.add_argument("--prefix", default="bench-demo")
    parser.add_argument("--timeout", type=float, default=420)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    parser.add_argument("--color", action="store_true")
    args = parser.parse_args()
    if args.count < 1 or args.count > 15:
        print("--count must be between 1 and 15")
        return 2
    if not args.prefix.startswith("bench-"):
        print('--prefix must start with "bench-"')
        return 2
    if args.timeout <= 0:
        print("--timeout must be greater than zero")
        return 2
    needed = ["ADMIN_TOKEN"] if args.cleanup else [
        "ADMIN_TOKEN", "PUBLIC_VIP_IP", "ROUTING_DOMAIN"]
    missing_status = fail_config(needed)
    if missing_status:
        return missing_status
    token = os.environ["ADMIN_TOKEN"]
    vip = os.environ.get("PUBLIC_VIP_IP", "<PUBLIC_VIP_IP>")
    domain = os.environ.get("ROUTING_DOMAIN", "<ROUTING_DOMAIN>")
    base_url = os.environ.get("CONTROL_PLANE_URL", "http://127.0.0.1:8081")
    try:
        return asyncio.run(run(args, token, vip, domain, base_url))
    except KeyboardInterrupt:
        print("FAILED: interrupted")
        return 1


if __name__ == "__main__":
    sys.exit(main())
