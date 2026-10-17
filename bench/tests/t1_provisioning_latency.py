#!/usr/bin/env python3
"""
T1: Single Sandbox Provisioning Latency Benchmark.
Sequential execution of sandbox provisioning runs with stage timing extraction,
gateway HTTP 200 resolution polling, and stacked bar chart generation.

Per user instruction:
- Skip cold/warm distinction; report the first run separately.
- Strict cleanup of all bench- resources even on failure.
"""

import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List

import matplotlib.pyplot as plt
import requests

# Ensure bench package is in sys.path
BENCH_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(BENCH_DIR.parent))

from bench.lib.config import load_config
from bench.lib.api_client import ControlPlaneClient
from bench.lib.openstack_client import OpenStackClient
from bench.lib.cleanup import cleanup_all_benchmark_resources
from bench.lib.stats import compute_stats, save_raw_csv, save_summary_json, utc_iso_now

def run_t1():
    cfg = load_config()
    cp_cfg = cfg["control_plane"]
    net_cfg = cfg["network"]
    bench_cfg = cfg["benchmark"]

    api = ControlPlaneClient(base_url=cp_cfg["base_url"], admin_token=cp_cfg["admin_token"], timeout=cp_cfg.get("request_timeout_seconds", 180.0))
    os_client = OpenStackClient(cloud=cfg.get("openstack", {}).get("cloud", "felcloud"))

    # Initial safety check
    status, health = api.health()
    if status != 200:
        print(f"FATAL: Control plane is not healthy at {cp_cfg['base_url']} (HTTP {status})")
        sys.exit(1)

    # Initial cleanup of any stale bench- sandboxes
    cleanup_all_benchmark_resources(api, os_client, verbose=False)

    total_runs = bench_cfg.get("t1_runs", 30)
    vip_public = net_cfg.get("edge_vip_public", "197.5.133.119")
    domain = net_cfg.get("domain", "cstam.felcloud.tn")

    print(f"======================================================================")
    print(f"  T1: Provisioning Latency Benchmark ({total_runs} Sequential Runs)")
    print(f"  Target VIP: {vip_public} | Domain: {domain}")
    print(f"======================================================================")

    raw_samples: List[Dict[str, Any]] = []

    for run_idx in range(1, total_runs + 1):
        team_id = f"bench-t1-{run_idx:02d}"
        subdomain = f"{team_id}.{domain}"
        run_type = "first_run" if run_idx == 1 else "steady_state"

        print(f"\n--> [Run {run_idx:02d}/{total_runs:02d}] Provisioning {team_id} ({run_type})...")

        t_start = time.perf_counter()
        ts_start = utc_iso_now()

        row: Dict[str, Any] = {
            "run_index": run_idx,
            "run_type": run_type,
            "team": team_id,
            "subdomain": subdomain,
            "start_time": ts_start,
            "http_status": 0,
            "success": False,
            "error": "",
            "stage_allocate_ms": 0.0,
            "stage_port_ms": 0.0,
            "stage_server_active_ms": 0.0,
            "stage_health_ms": 0.0,
            "stage_sync_ms": 0.0,
            "total_api_ms": 0.0,
            "gateway_first_200_ms": 0.0,
            "total_latency_ms": 0.0,
        }

        try:
            # 1. Trigger Provisioning API
            code, resp, api_sec = api.provision_sandbox(team=team_id, ttl=3600)
            row["http_status"] = code
            row["total_api_ms"] = round(api_sec * 1000.0, 2)

            if code in (200, 201):
                timings = resp.get("timings_ms") or resp.get("timings") or {}
                row["stage_allocate_ms"] = float(timings.get("allocate", 0))
                row["stage_port_ms"] = float(timings.get("port", 0))
                row["stage_server_active_ms"] = float(timings.get("server_active", 0))
                row["stage_health_ms"] = float(timings.get("health", 0))
                row["stage_sync_ms"] = float(timings.get("sync", 0))
                print(f"    ✓ API Provisioned in {row['total_api_ms']}ms (Nova ACTIVE: {row['stage_server_active_ms']}ms)")

                # 2. Poll until Subdomain returns HTTP 200 through Edge VIP
                poll_start = time.perf_counter()
                poll_timeout = 60.0
                first_200 = False
                gateway_lat = 0.0

                headers = {"Host": subdomain, "User-Agent": "FelCloud-T1-Prober/1.0"}
                target_url = f"http://{vip_public}/"

                while (time.perf_counter() - poll_start) < poll_timeout:
                    try:
                        r = requests.get(target_url, headers=headers, timeout=2.0)
                        if r.status_code == 200:
                            gateway_lat = (time.perf_counter() - poll_start) * 1000.0
                            first_200 = True
                            break
                    except Exception:
                        pass
                    time.sleep(0.5)

                t_total_ms = (time.perf_counter() - t_start) * 1000.0
                row["total_latency_ms"] = round(t_total_ms, 2)
                row["gateway_first_200_ms"] = round(gateway_lat, 2)

                if first_200:
                    row["success"] = True
                    print(f"    ✓ First HTTP 200 through Gateway in {row['gateway_first_200_ms']}ms | Total: {row['total_latency_ms']}ms")
                else:
                    row["error"] = "Gateway route did not return HTTP 200 within timeout"
                    print(f"    ⚠️  {row['error']}")
            else:
                row["error"] = resp.get("error", resp.get("detail", f"HTTP {code}"))
                row["total_latency_ms"] = round((time.perf_counter() - t_start) * 1000.0, 2)
                print(f"    ✗ API Provision Failed: {row['error']}")

        except Exception as e:
            row["error"] = str(e)
            row["total_latency_ms"] = round((time.perf_counter() - t_start) * 1000.0, 2)
            print(f"    ✗ Exception during run: {e}")

        finally:
            # Clean up after each sequential run
            try:
                api.delete_sandbox(team_id)
            except Exception as e:
                print(f"    ⚠️  Cleanup delete failed for {team_id}: {e}")

            raw_samples.append(row)

    # Final cleanup
    cleanup_all_benchmark_resources(api, os_client, verbose=False)

    # ── Outputs & Aggregations ─────────────────────────────────────────────────
    ts_now = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    raw_csv_path = BENCH_DIR / "results" / "raw" / f"t1_provisioning_{ts_now}.csv"
    summary_json_path = BENCH_DIR / "results" / "raw" / f"t1_provisioning_{ts_now}_summary.json"
    chart_path = BENCH_DIR / "results" / "charts" / "t1_provisioning_stages.png"

    save_raw_csv(raw_csv_path, raw_samples)
    print(f"\n✓ Saved raw sample data to {raw_csv_path}")

    first_run = raw_samples[0] if raw_samples else {}
    subsequent_runs = raw_samples[1:] if len(raw_samples) > 1 else []

    all_totals = [r["total_latency_ms"] for r in raw_samples if r["success"]]
    subseq_totals = [r["total_latency_ms"] for r in subsequent_runs if r["success"]]

    summary = {
        "test": "T1_provisioning_latency",
        "timestamp": utc_iso_now(),
        "total_runs": total_runs,
        "successful_runs": len(all_totals),
        "first_run": first_run,
        "all_runs_stats": compute_stats(all_totals),
        "steady_state_stats": compute_stats(subseq_totals),
        "stages": {
            "allocate": compute_stats([r["stage_allocate_ms"] for r in raw_samples if r["stage_allocate_ms"] > 0]),
            "port": compute_stats([r["stage_port_ms"] for r in raw_samples if r["stage_port_ms"] > 0]),
            "server_active": compute_stats([r["stage_server_active_ms"] for r in raw_samples if r["stage_server_active_ms"] > 0]),
            "health": compute_stats([r["stage_health_ms"] for r in raw_samples if r["stage_health_ms"] > 0]),
            "sync": compute_stats([r["stage_sync_ms"] for r in raw_samples if r["stage_sync_ms"] > 0]),
            "gateway_resolution": compute_stats([r["gateway_first_200_ms"] for r in raw_samples if r["gateway_first_200_ms"] > 0]),
        }
    }
    save_summary_json(summary_json_path, summary)
    print(f"✓ Saved summary JSON to {summary_json_path}")

    # ── Generate Stacked Bar Chart ─────────────────────────────────────────────
    try:
        plot_stacked_stages(raw_samples, chart_path)
        print(f"✓ Generated stacked bar chart: {chart_path}")
    except Exception as e:
        print(f"⚠️  Chart generation error: {e}")

    # Print summary table
    st = summary["steady_state_stats"]
    print(f"\n======================================================================")
    print(f"  T1 Provisioning Latency Summary (Target: p95 < 60s)")
    print(f"======================================================================")
    print(f"  First Run:      {first_run.get('total_latency_ms', 0)} ms")
    print(f"  Success Rate:   {len(all_totals)}/{total_runs} ({len(all_totals)/total_runs*100:.1f}%)")
    print(f"  Steady State:   min={st.get('min')}ms | mean={st.get('mean')}ms | p50={st.get('p50')}ms | p95={st.get('p95')}ms | max={st.get('max')}ms")
    print(f"======================================================================\n")

def plot_stacked_stages(samples: List[Dict[str, Any]], output_path: Path):
    output_path.parent.mkdir(parents=True, exist_ok=True)
    valid_runs = [r for r in samples if r["success"]]
    if not valid_runs:
        # Plot all attempted runs with total latency
        runs = [r["run_index"] for r in samples]
        totals = [r["total_latency_ms"] / 1000.0 for r in samples]
        plt.figure(figsize=(10, 5))
        plt.bar(runs, totals, color="salmon")
        plt.xlabel("Run Index")
        plt.ylabel("Total Latency (s)")
        plt.title(f"T1 Provisioning Latency (n={len(samples)})")
        plt.tight_layout()
        plt.savefig(output_path, dpi=150)
        plt.close()
        return

    runs = [f"R{r['run_index']}" for r in valid_runs]
    alloc = [r["stage_allocate_ms"] / 1000.0 for r in valid_runs]
    port = [r["stage_port_ms"] / 1000.0 for r in valid_runs]
    server = [r["stage_server_active_ms"] / 1000.0 for r in valid_runs]
    health = [r["stage_health_ms"] / 1000.0 for r in valid_runs]
    sync = [r["stage_sync_ms"] / 1000.0 for r in valid_runs]
    gw_poll = [r["gateway_first_200_ms"] / 1000.0 for r in valid_runs]

    fig, ax = plt.subplots(figsize=(12, 6))

    bottom = [0.0] * len(valid_runs)
    ax.bar(runs, alloc, label="IPAM Allocate", bottom=bottom, color="#4e79a7")
    bottom = [b + v for b, v in zip(bottom, alloc)]

    ax.bar(runs, port, label="Neutron Port", bottom=bottom, color="#f28e2b")
    bottom = [b + v for b, v in zip(bottom, port)]

    ax.bar(runs, server, label="Nova Server ACTIVE", bottom=bottom, color="#e15759")
    bottom = [b + v for b, v in zip(bottom, server)]

    ax.bar(runs, health, label="Pre-flight Health", bottom=bottom, color="#76b7b2")
    bottom = [b + v for b, v in zip(bottom, health)]

    ax.bar(runs, sync, label="Gateway Route Sync", bottom=bottom, color="#59a14f")
    bottom = [b + v for b, v in zip(bottom, sync)]

    ax.bar(runs, gw_poll, label="Gateway First 200", bottom=bottom, color="#edc948")

    ax.axhline(60.0, color="red", linestyle="--", linewidth=1.5, label="Target SLA (60s)")
    ax.set_xlabel("Run Index (R1: First Run, R2-R30: Steady State)")
    ax.set_ylabel("Duration (Seconds)")
    ax.set_title(f"T1: Sandbox Provisioning Stage Breakdown (n={len(valid_runs)})")
    ax.legend(loc="upper right", frameon=True)
    plt.xticks(rotation=45)
    plt.tight_layout()
    plt.savefig(output_path, dpi=150)
    plt.close()

if __name__ == "__main__":
    run_t1()
