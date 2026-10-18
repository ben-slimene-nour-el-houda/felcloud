#!/usr/bin/env python3
"""Live concurrent provisioning stress test. No simulated responses are used."""
import argparse
import asyncio
import csv
import ipaddress
import json
import os
import re
import sqlite3
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import httpx
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bench.lib.api_client import AsyncControlPlaneClient
from bench.lib.config import load_config

BASE = Path(__file__).resolve().parents[1]
RAW = BASE / "results" / "raw"
CHARTS = BASE / "results" / "charts"
LEVELS = [3, 5, 10, 15]
PROTECTED = {"team1", "team2", "jury-demo", "gateways", "bastion"}
TIMEOUT = 420.0
WINDOW = "5"


def utc():
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


class RateGate:
    """Serialize VIP GET starts to at most five per rolling second."""
    def __init__(self):
        self.lock = asyncio.Lock()
        self.next_at = 0.0

    async def wait(self):
        async with self.lock:
            now = time.monotonic()
            delay = max(0.0, self.next_at - now)
            if delay:
                await asyncio.sleep(delay)
            self.next_at = max(time.monotonic(), self.next_at) + 0.2


def error_text(data):
    if isinstance(data, dict):
        return str(data.get("error") or data.get("detail") or data)
    return str(data)


async def api_json(client, method, path, **kwargs):
    response = await client.request(method, path, **kwargs)
    try:
        data = response.json()
    except Exception:
        data = {"raw": response.text}
    return response.status_code, data


async def probe(client, gate, url, host, timeout=4):
    await gate.wait()
    started = time.perf_counter()
    stamp = utc()
    try:
        r = await client.get(url, headers={"Host": host, "User-Agent": "FelCloud-T2"}, timeout=timeout)
        return {"timestamp_utc": stamp, "status": r.status_code,
                "latency_ms": (time.perf_counter()-started)*1000,
                "body": r.text[:4000], "gateway_id": r.headers.get("X-Gateway-ID", ""), "error": ""}
    except Exception as exc:
        return {"timestamp_utc": stamp, "status": 0,
                "latency_ms": (time.perf_counter()-started)*1000,
                "body": "", "gateway_id": "", "error": f"{type(exc).__name__}: {exc}"}


def identity_matches(body, sandbox_id):
    return sandbox_id.lower() in body.lower()


async def background_probe(client, gate, url, stop, samples):
    target = f"team1.{os.environ.get('ROUTING_DOMAIN', 'cloudpulse.cstam.felcloud.tn')}"
    while not stop.is_set():
        due = time.monotonic()
        samples.append(await probe(client, gate, url, target))
        delay = max(0, 0.2 - (time.monotonic() - due))
        try:
            await asyncio.wait_for(stop.wait(), delay)
        except asyncio.TimeoutError:
            pass


async def fetch_dpa_raw(host, password):
    url = f"http://{host}:5555/v3/services/haproxy/configuration/raw"
    async with httpx.AsyncClient(timeout=15, auth=("admin", password)) as c:
        r = await c.get(url)
        r.raise_for_status()
        return r.text


def backend_names(config):
    return set(re.findall(r"(?m)^backend\s+([^\s#]+)", config))


def read_pool_size():
    """Read the live subnet CIDR from the control-plane SQLite DB without writes."""
    candidates = [os.environ.get("IPAM_DB_PATH", ""),
                  str(Path(__file__).resolve().parents[2] / "controlplane" / "ipam" / "ipam.db"),
                  str(Path(__file__).resolve().parents[2] / "controlplane" / "ipam.db")]
    errors = []
    for candidate in candidates:
        if not candidate or not Path(candidate).is_file():
            continue
        try:
            uri = Path(candidate).resolve().as_uri() + "?mode=ro"
            with sqlite3.connect(uri, uri=True, timeout=5) as db:
                row = db.execute("SELECT cidr FROM subnets WHERE name='sandbox'").fetchone()
            if not row:
                raise RuntimeError("subnets table has no sandbox CIDR row")
            network = ipaddress.ip_network(row[0], strict=False)
            # Mirror the allocator's real dynamic range: network+2 through
            # broadcast-2 inclusive. Existing reservations are still handled
            # by allocating until the control plane returns its real error.
            return max(0, network.num_addresses - 4), str(network), candidate
        except Exception as exc:
            errors.append(f"{candidate}: {type(exc).__name__}: {exc}")
    raise RuntimeError("cannot read real sandbox pool size from control-plane DB read-only; " +
                       ("; ".join(errors) if errors else "no database file found"))


def read_generation_count(db_path):
    uri = Path(db_path).resolve().as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True, timeout=5) as db:
        row = db.execute("SELECT COUNT(*) FROM config_generations").fetchone()
    if not row:
        raise RuntimeError("config_generations count query returned no row")
    return int(row[0])


async def ssh_remote(cfg, host, remote):
    import subprocess
    ssh = ["ssh", "-i", cfg["openstack"]["ssh_key_path"], "-o", "BatchMode=yes",
           "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
           "-o", "IPQoS=none", "-o", "ConnectTimeout=10", "-J",
           f"{cfg['openstack']['ssh_user']}@{cfg['openstack']['bastion_host']}",
           f"{cfg['openstack']['ssh_user']}@{host}", remote]
    p = await asyncio.to_thread(subprocess.run, ssh, capture_output=True, text=True, timeout=30)
    if p.returncode:
        raise RuntimeError((p.stderr or p.stdout).strip() or f"ssh exited {p.returncode}")
    return p.stdout


async def openstack_json(cloud, *args):
    import subprocess
    cmd = ["openstack", "--os-cloud", cloud, *args, "-f", "json"]
    p = await asyncio.to_thread(subprocess.run, cmd, capture_output=True, text=True, timeout=60)
    if p.returncode:
        raise RuntimeError(f"{' '.join(cmd)} failed: {(p.stderr or p.stdout).strip()}")
    return json.loads(p.stdout)


async def run(cfg, smoke=False):
    cp = cfg["control_plane"]
    net = cfg["network"]
    token = os.environ.get("ADMIN_TOKEN", "")
    vip = str(net.get("public_vip", ""))
    domain = str(net.get("domain", ""))
    if not token or not vip or not domain:
        missing = [name for name, value in (("ADMIN_TOKEN", token), ("PUBLIC_VIP_IP", vip), ("ROUTING_DOMAIN", domain)) if not value]
        raise RuntimeError("missing required configuration: " + ", ".join(missing))
    if domain != "cloudpulse.cstam.felcloud.tn":
        raise RuntimeError(f"ROUTING_DOMAIN must be cloudpulse.cstam.felcloud.tn (got {domain!r})")
    if os.environ.get("DPA_PASS", "") == "":
        raise RuntimeError("DPA_PASS is unset")
    gates = cfg.get("safety_gates", {})
    hosts = gates.get("allowed_gateway_hosts", {})
    primary, standby = hosts.get("primary", ""), hosts.get("standby", "")
    ssh_cfg = cfg.get("openstack", {})
    if not primary or not standby or primary == standby:
        raise RuntimeError("BENCH_PRIMARY_HOST and BENCH_STANDBY_HOST must be distinct configured SSH destinations")
    host_pattern = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")
    if not host_pattern.fullmatch(primary) or not host_pattern.fullmatch(standby) or not host_pattern.fullmatch(ssh_cfg.get("bastion_host", "")):
        raise RuntimeError("gateway and bastion SSH destinations contain unsupported characters")
    if not ssh_cfg.get("bastion_host") or not ssh_cfg.get("ssh_key_path") or not Path(ssh_cfg["ssh_key_path"]).is_file():
        raise RuntimeError("BENCH_BASTION_HOST and a readable SSH_KEY_PATH are required for gateway sanity checks")
    if gates.get("resource_prefix") != "bench-" or gates.get("cleanup_name_prefix") != "bench-":
        raise RuntimeError("benchmark cleanup prefixes must both be exactly bench-")
    cap = int(os.environ.get("BENCH_MAX_CONCURRENCY", "15"))
    if cap < 1 or cap > 15:
        raise RuntimeError(f"BENCH_MAX_CONCURRENCY must be 1..15 (got {cap})")
    levels = [2] if smoke else LEVELS
    if max(levels) > cap:
        raise RuntimeError(f"T2 requires concurrency 15, but BENCH_MAX_CONCURRENCY={cap}")
    os.environ["HEALTH_REGRESSION_WINDOW_SECONDS"] = WINDOW
    probe_url = f"http://{vip.strip().removeprefix('http://').removesuffix('/')}"
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    RAW.mkdir(parents=True, exist_ok=True); CHARTS.mkdir(parents=True, exist_ok=True)
    result = {"test": "T2_concurrent_stress", "status": "EXECUTED", "timestamp_utc": utc(),
              "health_regression_window_seconds": WINDOW, "max_concurrency": cap,
              "probe_rate_limit_per_second": 5, "bursts": [], "pool_exhaustion": {},
              "final_sanity": {}}
    all_rows = []
    gate = RateGate()
    async with AsyncControlPlaneClient(cp["base_url"], token, timeout=TIMEOUT) as api, \
               httpx.AsyncClient(timeout=8, follow_redirects=False) as vip_client:
        status, health = await api_json(api, "GET", "/health")
        if status != 200:
            raise RuntimeError(f"control plane /health returned HTTP {status}: {health}")
        pool_capacity, pool_cidr, pool_db = read_pool_size()

        for burst_n in levels:
            burst_id = f"bench-t2-{stamp.lower()}-{burst_n}"
            ids = [f"{burst_id}-{i:02d}" for i in range(1, burst_n+1)]
            rows = {name: {"sandbox_id": name, "burst_size": burst_n, "status": "PENDING",
                           "success": False, "post_started_utc": "", "post_first_http_200_ms": None,
                           "stages_ms": {}, "errors": [], "http_statuses": []} for name in ids}
            created = []
            gen_before = None
            gen_after = None
            burst_start_ms = int(time.time()*1000)
            try:
                try:
                    gen_before = read_generation_count(pool_db)
                except Exception as exc:
                    gen_before = f"NOT EXECUTED: {type(exc).__name__}: {exc}"
                barrier = asyncio.Event()
                async def provision(name):
                    row = rows[name]
                    await barrier.wait()
                    row["post_started_utc"] = utc()
                    started = time.perf_counter()
                    try:
                        r = await api.provision_sandbox(name, ttl=3600, custom_domain=f"{name}.{domain}")
                        created.append(name)
                        row["provision_http_status"] = r.status_code
                        row["provision_response"] = r.data
                        row["provision_elapsed_ms"] = r.elapsed_seconds*1000
                        timings = r.data.get("timings_ms", {})
                        row["stages_ms"] = timings if isinstance(timings, dict) else {}
                        if r.status_code not in (200, 201):
                            row["status"] = "PROVISION_FAILED"
                            row["errors"].append(error_text(r.data))
                            return
                        row["fixed_ip"] = r.data.get("fixed_ip", "")
                        row["status"] = "WAITING_FOR_HTTP_200"
                        deadline = time.monotonic()+TIMEOUT
                        while time.monotonic() < deadline:
                            p = await probe(vip_client, gate, probe_url, f"{name}.{domain}")
                            row["http_statuses"].append(p["status"])
                            if p["status"] == 200:
                                row["post_first_http_200_ms"] = (time.perf_counter()-started)*1000
                                row["first_http_200_utc"] = p["timestamp_utc"]
                                row["identity_ok"] = identity_matches(p["body"], name)
                                row["gateway_id"] = p["gateway_id"]
                                row["success"] = row["identity_ok"]
                                row["status"] = "SUCCESS" if row["success"] else "MISROUTED_CONTENT"
                                if not row["success"]:
                                    row["errors"].append("HTTP 200 body did not contain its sandbox ID")
                                return
                        row["status"] = "HTTP_200_TIMEOUT"
                        row["errors"].append("HTTP 200 not observed before 420-second deadline")
                    except Exception as exc:
                        row["status"] = "ERROR"
                        row["errors"].append(f"{type(exc).__name__}: {exc}")
                tasks = [asyncio.create_task(provision(name)) for name in ids]
                barrier.set()
                stop = asyncio.Event(); team_samples = []
                prober = asyncio.create_task(background_probe(vip_client, gate, probe_url, stop, team_samples))
                await asyncio.gather(*tasks)
                stop.set(); await prober

                # Real read-only observations from both DPA raw configs.
                dpa = {}
                for label, host in (("gw1", "127.0.0.11"), ("gw2", "127.0.0.12")):
                    try:
                        raw = await fetch_dpa_raw(host, os.environ["DPA_PASS"])
                        names = backend_names(raw)
                        dpa[label] = {"status": "READ", "backend_names": sorted(names),
                                      "sandbox_backend_present": {name: f"{name}_backend" in names for name in ids}}
                    except Exception as exc:
                        dpa[label] = {"status": "NOT EXECUTED", "error": f"{type(exc).__name__}: {exc}"}

                # Lease/IPAM and Neutron port observations.
                leases_status, lease_data = await api_json(api, "GET", "/api/leases")
                lease_rows = lease_data.get("leases", []) if leases_status == 200 else []
                bench_ipam = {x.get("entity_name"): x.get("ip_address") for x in lease_rows
                              if x.get("entity_name") in ids and x.get("status") == "ALLOCATED"}
                try:
                    ports = await openstack_json(cfg["openstack"]["cloud"], "port", "list")
                    port_ips = {}
                    for p in ports:
                        pname = str(p.get("Name", ""))
                        if pname in ids:
                            ips = re.findall(r"10\.20\.30\.\d+", str(p))
                            if ips: port_ips[pname] = ips[0]
                    ports_observation = {"status": "READ", "ips": port_ips}
                except Exception as exc:
                    port_ips = {}
                    ports_observation = {"status": "NOT EXECUTED", "error": f"{type(exc).__name__}: {exc}"}
                ip_values = [v for k, v in bench_ipam.items() if k not in {"team1", "team2"}]
                duplicates = sorted({ip for ip in ip_values if ip_values.count(ip) > 1})
                port_ip_values = [v for k, v in port_ips.items() if k in ids]
                duplicate_port_ips = sorted({ip for ip in port_ip_values if port_ip_values.count(ip) > 1})

                # Events are the control plane's gateway sync error log when available.
                ev_status, ev_data = await api_json(api, "GET", f"/api/gateways/events?since={burst_start_ms}&limit=500")
                events = ev_data.get("events", []) if ev_status == 200 else []
                sync_events = [e for e in events if e.get("type") == "sync"]
                sync_failures = [e for e in sync_events if re.search(r"fail|error|409|busy|DOWN", str(e.get("detail", "")), re.I)]
                try:
                    gen_after = read_generation_count(pool_db)
                except Exception as exc:
                    gen_after = f"NOT EXECUTED: {type(exc).__name__}: {exc}"

                for name, row in rows.items():
                    row["dpa_backend_both"] = all(dpa.get(g, {}).get("sandbox_backend_present", {}).get(name, False) for g in ("gw1", "gw2"))
                    row["ipam_ip"] = bench_ipam.get(name, "")
                    row["neutron_ip"] = port_ips.get(name, "")
                    row["duplicate_ip"] = bool(row["ipam_ip"] and row["ipam_ip"] in duplicates)
                    row["ipam_neutron_match"] = bool(row["ipam_ip"] and row["ipam_ip"] == row["neutron_ip"])
                    if name in created and row["status"] == "SUCCESS" and not row["dpa_backend_both"]:
                        row["errors"].append("backend absent from at least one DPA raw config")
                    all_rows.append(row)

                # Cleanup only names generated in this burst; never global cleanup.
                cleanup = []
                for name in ids:
                    if not name.startswith("bench-") or name in PROTECTED:
                        continue
                    try:
                        s, data = await api_json(api, "DELETE", f"/api/sandboxes/{name}")
                        cleanup.append({"sandbox_id": name, "status_code": s, "error": "" if s in (200, 202, 204, 404) else error_text(data)})
                    except Exception as exc:
                        cleanup.append({"sandbox_id": name, "error": f"{type(exc).__name__}: {exc}"})
                await asyncio.sleep(12)
                chk_s, chk_data = await api_json(api, "GET", "/api/sandboxes")
                remaining = [x for x in chk_data.get("sandboxes", []) if x.get("team", x.get("name", "")) in ids] if chk_s == 200 else None
                result["bursts"].append({"burst_size": burst_n, "sandbox_ids": ids,
                    "successes": sum(bool(x["success"]) for x in rows.values()), "failures": sum(not x["success"] for x in rows.values()),
                    "success_rate": sum(bool(x["success"]) for x in rows.values())/burst_n,
                    "p50_provisioning_seconds": percentile([x["post_first_http_200_ms"]/1000 for x in rows.values() if x["post_first_http_200_ms"] is not None], 50),
                    "p95_provisioning_seconds": percentile([x["post_first_http_200_ms"]/1000 for x in rows.values() if x["post_first_http_200_ms"] is not None], 95),
                    "gateway_configs": dpa,
                    "ipam_neutron": {"ipam": bench_ipam, "neutron_ports": ports_observation,
                                     "duplicate_ip_addresses_in_ipam": duplicates,
                                     "duplicate_ip_addresses_in_neutron_ports": duplicate_port_ips,
                                     "team1_team2_drift_excluded": True},
                    "gateway_sync": {"events_status": ev_status, "failures": sync_failures,
                                     "failure_count": len(sync_failures), "config_generations_count_before": gen_before,
                                     "config_generations_count_after": gen_after,
                                     "push_count_delta": (gen_after-gen_before if isinstance(gen_before, int) and isinstance(gen_after, int) else "NOT EXECUTED")},
                    "team1_prober": {"samples": team_samples, "non_200_count": sum(x["status"] not in (200, 429) for x in team_samples),
                                     "http_429_count": sum(x["status"] == 429 for x in team_samples),
                                     "dropped_count": sum(x["status"] == 0 for x in team_samples)},
                    "cleanup": cleanup, "cleanup_remaining_sandboxes": remaining,
                    "sandbox_rows": list(rows.values())})
            except Exception as exc:
                # Even a failed observation must not strand this burst's VMs.
                cleanup_errors = []
                for name in ids:
                    if not name.startswith("bench-") or name in PROTECTED:
                        continue
                    try:
                        code, data = await api_json(api, "DELETE", f"/api/sandboxes/{name}")
                        cleanup_errors.append({"sandbox_id": name, "status_code": code,
                                               "error": "" if code in (200, 202, 204, 404) else error_text(data)})
                    except Exception as cleanup_exc:
                        cleanup_errors.append({"sandbox_id": name,
                            "error": f"{type(cleanup_exc).__name__}: {cleanup_exc}"})
                for row in rows.values():
                    if row["status"] == "PENDING":
                        row["status"] = "NOT EXECUTED"
                        row["errors"].append(f"burst observation failed: {type(exc).__name__}: {exc}")
                    all_rows.append(row)
                result["bursts"].append({"burst_size": burst_n, "status": "PARTIAL",
                    "error": f"{type(exc).__name__}: {exc}", "cleanup": cleanup_errors,
                    "sandbox_rows": list(rows.values()), "team1_prober": {"status": "NOT EXECUTED"}})

        # Pool exhaustion uses API leases only, with unique bench names, then releases every obtained lease.
        start_s, start_data = await api_json(api, "GET", "/api/leases")
        start_leases = start_data.get("leases", []) if start_s == 200 else []
        start_sig = sorted((x.get("ip_address"), x.get("status"), x.get("entity_name")) for x in start_leases
                           if x.get("status") in ("ALLOCATED", "QUARANTINED"))
        allocated = []
        exhausted = None
        pool_start = time.monotonic()
        for index in range(1, pool_capacity + 2):
            name = f"bench-t2-pool-{stamp.lower()}-{index:03d}"
            s, data = await api_json(api, "POST", "/api/leases/allocate", json_body={"sandbox_id": name, "ttl": 3600})
            if s not in (200, 201):
                exhausted = {"http_status": s, "error": error_text(data), "response": data}
                break
            lease = data.get("lease", data)
            ip = lease.get("ip_address", lease.get("fixed_ip", ""))
            if not ip:
                exhausted = {"http_status": s, "error": "allocation response omitted IP address", "response": data}
                break
            allocated.append((name, ip))
        releases = []
        for name, ip in allocated:
            s, data = await api_json(api, "POST", "/api/leases/release", json_body={"sandbox_id": name, "ip_address": ip, "cooldown_seconds": 1})
            releases.append({"sandbox_id": name, "ip_address": ip, "http_status": s, "error": "" if s in (200, 204) else error_text(data)})
        await asyncio.sleep(2)
        await api_json(api, "POST", "/api/leases/reclaim")
        end_s, end_data = await api_json(api, "GET", "/api/leases")
        end_leases = end_data.get("leases", []) if end_s == 200 else []
        end_sig = sorted((x.get("ip_address"), x.get("status"), x.get("entity_name")) for x in end_leases
                         if x.get("status") in ("ALLOCATED", "QUARANTINED"))
        result["pool_exhaustion"] = {"pool_size_from_control_plane_db": pool_capacity, "pool_cidr": pool_cidr,
            "database_path": pool_db, "allocations_until_exhausted": len(allocated),
            "elapsed_seconds": time.monotonic()-pool_start, "exhaustion_response": exhausted or "NOT EXECUTED: no exhaustion response before safety bound 299",
            "released_all_allocated_leases": len(releases) == len(allocated) and all(not x["error"] for x in releases),
            "release_results": releases, "starting_ipam_signature": start_sig, "ending_ipam_signature": end_sig,
            "ipam_restored_to_starting_state": start_s == end_s == 200 and start_sig == end_sig}

        # Final checks use real OpenStack/API/SSH observations. Each failed observation records its exact error.
        final = result["final_sanity"]
        try:
            servers = await openstack_json(cfg["openstack"]["cloud"], "server", "list")
            final["openstack_server_list"] = {"status": "READ", "count": len(servers), "servers": servers,
                                              "only_five_permanent_vms": len(servers) == 5 and not any(str(x.get("Name", "")).startswith("bench-") for x in servers)}
        except Exception as exc:
            final["openstack_server_list"] = {"status": "NOT EXECUTED", "error": f"{type(exc).__name__}: {exc}"}
        try:
            ports = await openstack_json(cfg["openstack"]["cloud"], "port", "list")
            final["bench_ports"] = [x for x in ports if str(x.get("Name", "")).startswith("bench-")]
        except Exception as exc:
            final["bench_ports"] = {"status": "NOT EXECUTED", "error": f"{type(exc).__name__}: {exc}"}
        ss, sd = await api_json(api, "GET", "/api/sandboxes")
        final["active_bench_sandboxes"] = ([x for x in sd.get("sandboxes", []) if str(x.get("sandbox_id", x.get("team", x.get("name", "")))).startswith("bench-") and x.get("status", x.get("state")) in ("ACTIVE", "PROVISIONING", "TEARING_DOWN")] if ss == 200 else {"status": "NOT EXECUTED", "http_status": ss, "error": sd})
        for label, host in (("gw1", "127.0.0.11"), ("gw2", "127.0.0.12")):
            try:
                command = ("sudo haproxy -c -f /etc/haproxy/haproxy.cfg && "
                           "printf 'BASELINE_BACKENDS=' && "
                           "grep -Ec '^backend (team1|team2|jury-demo)_backend$' /etc/haproxy/haproxy.cfg")
                observed = await ssh_remote(cfg, cfg["safety_gates"]["allowed_gateway_hosts"]["primary" if label == "gw1" else "standby"], command)
                match = re.search(r"BASELINE_BACKENDS=(\d+)", observed)
                final.setdefault("gateway_validation", {})[label] = {"output": observed.strip(),
                    "haproxy_config_valid": "Configuration file is valid" in observed,
                    "baseline_backend_count": int(match.group(1)) if match else None,
                    "three_baseline_backends": bool(match and int(match.group(1)) == 3)}
            except Exception as exc:
                final.setdefault("gateway_validation", {})[label] = {"status": "NOT EXECUTED", "error": f"{type(exc).__name__}: {exc}"}

    raw_path = RAW / f"t2_concurrent_{stamp}.csv"
    fields = sorted({k for row in all_rows for k in row})
    with raw_path.open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=fields, extrasaction="ignore"); w.writeheader()
        for row in all_rows: w.writerow({k: json.dumps(v, sort_keys=True) if isinstance(v, (dict,list)) else v for k,v in row.items()})
    summary_path = RAW / f"t2_concurrent_{stamp}_summary.json"
    summary_path.write_text(json.dumps(result, indent=2, sort_keys=True, default=str)+"\n", encoding="utf-8")
    chart_path = CHARTS / f"t2_concurrent_{stamp}.png"
    fig, axes = plt.subplots(2, 1, figsize=(9, 7), sharex=True)
    burst_rows = result["bursts"]
    axes[0].plot([x["burst_size"] for x in burst_rows], [x["success_rate"]*100 for x in burst_rows], marker="o")
    axes[0].set_ylabel("Success rate (%)"); axes[0].set_ylim(0, 100); axes[0].grid(True, alpha=.3)
    axes[1].plot([x["burst_size"] for x in burst_rows], [x["p50_provisioning_seconds"] for x in burst_rows], marker="o", label="p50")
    axes[1].plot([x["burst_size"] for x in burst_rows], [x["p95_provisioning_seconds"] for x in burst_rows], marker="o", label="p95")
    axes[1].set_xlabel("Concurrent requests"); axes[1].set_ylabel("Provisioning time (s)"); axes[1].grid(True, alpha=.3); axes[1].legend()
    fig.suptitle("T2 concurrent stress: observed results"); fig.tight_layout(); fig.savefig(chart_path, dpi=150); plt.close(fig)
    result["artifacts"] = {"raw_csv": str(raw_path), "summary_json": str(summary_path), "chart": str(chart_path)}
    summary_path.write_text(json.dumps(result, indent=2, sort_keys=True, default=str)+"\n", encoding="utf-8")
    print(json.dumps({"status": result["status"], "bursts": [{"n": x["burst_size"], "success": x["successes"], "failed": x["failures"]} for x in result["bursts"]],
                      "health_regression_window_seconds": WINDOW, "artifacts": result["artifacts"]}, indent=2))
    return result


def percentile(values, p):
    if not values: return None
    v = sorted(values)
    if len(v) == 1: return v[0]
    rank = (len(v)-1)*p/100
    lo = int(rank); hi = min(lo+1, len(v)-1)
    return v[lo] + (v[hi]-v[lo])*(rank-lo)


def main():
    p = argparse.ArgumentParser(description="T2 concurrent provisioning stress")
    modes = p.add_mutually_exclusive_group()
    modes.add_argument("--dry-run", action="store_true")
    modes.add_argument("--smoke", action="store_true")
    args = p.parse_args()
    try:
        cfg = load_config(BASE / "config.yaml")
        cap = int(os.environ.get("BENCH_MAX_CONCURRENCY", "15"))
        if cap < 1 or cap > 15: raise ValueError("BENCH_MAX_CONCURRENCY must be 1..15")
        if args.dry_run:
            required = {"ADMIN_TOKEN": bool(os.environ.get("ADMIN_TOKEN")),
                        "PUBLIC_VIP_IP": bool(cfg["network"].get("public_vip")),
                        "ROUTING_DOMAIN": cfg["network"].get("domain") == "cloudpulse.cstam.felcloud.tn",
                        "DPA_PASS": bool(os.environ.get("DPA_PASS")),
                        "BENCH_PRIMARY_HOST": bool(cfg.get("safety_gates", {}).get("allowed_gateway_hosts", {}).get("primary")),
                        "BENCH_STANDBY_HOST": bool(cfg.get("safety_gates", {}).get("allowed_gateway_hosts", {}).get("standby"))}
            print(json.dumps({"test":"T2_concurrent_stress", "mode":"DRY RUN", "bursts":[2] if args.smoke else LEVELS,
                "hard_concurrency_cap":15, "configured_cap":cap, "health_regression_window_seconds":WINDOW,
                "probe_limit_per_second":5, "routing_domain":"cloudpulse.cstam.felcloud.tn",
                "vip":cfg["network"].get("public_vip") or "NOT CONFIGURED", "control_plane":cfg["control_plane"]["base_url"],
                "configuration_gates":required,
                "actions":["barrier-start provision API requests", "poll sandbox HTTP routes and team1 every 200ms", "read DPA configs/IPAM/Neutron/events/generations", "delete this burst's bench-* IDs", "exhaust and restore leases", "final OpenStack and HAProxy checks"],
                "network_calls":False}, indent=2)); return 0
        result = asyncio.run(run(cfg, smoke=args.smoke))
        return 0 if result.get("status") == "EXECUTED" else 2
    except Exception as exc:
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        RAW.mkdir(parents=True, exist_ok=True)
        detail = f"{type(exc).__name__}: {exc}"
        out = {"test":"T2_concurrent_stress", "status":"NOT EXECUTED", "error":detail,
               "health_regression_window_seconds":WINDOW, "timestamp_utc":utc(),
               "chart":"NOT EXECUTED: no live burst data; " + detail,
               "per_sandbox_results":"NOT EXECUTED: provisioning did not start; " + detail}
        (RAW / f"t2_concurrent_{stamp}_summary.json").write_text(json.dumps(out, indent=2)+"\n", encoding="utf-8")
        with (RAW / f"t2_concurrent_{stamp}.csv").open("w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=["timestamp_utc", "test", "status", "error"])
            writer.writeheader(); writer.writerow({"timestamp_utc": out["timestamp_utc"], "test": out["test"],
                                                   "status": "NOT EXECUTED", "error": detail})
        print(f"T2 NOT EXECUTED: {detail}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
