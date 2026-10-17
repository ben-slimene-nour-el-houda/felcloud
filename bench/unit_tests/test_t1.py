import asyncio
import csv
import json

import httpx

from bench.lib.t1 import run_t1


def configuration(runs=2):
    return {
        "control_plane": {"base_url": "https://mock.invalid", "request_timeout_seconds": 2},
        "network": {"public_vip": "192.0.2.1", "domain": "bench.invalid"},
        "benchmark": {"t1_runs": runs, "t1_cold_runs": 1,
                       "t1_lease_ttl_seconds": 45, "t1_poll_timeout_seconds": 1,
                       "t1_poll_interval_seconds": 0, "t1_http_timeout_seconds": 1},
    }


def test_t1_smoke_records_one_mocked_run_and_cleans(tmp_path, monkeypatch):
    monkeypatch.setenv("ADMIN_TOKEN", "unit-test-token-never-printed")
    deleted = []

    def handler(request):
        if request.url.path == "/health":
            return httpx.Response(200, json={"status": "ok"})
        if request.url.path == "/api/sandboxes/provision":
            payload = json.loads(request.content)
            assert payload["team"].startswith("bench-")
            assert payload["ttl"] == 45
            return httpx.Response(201, json={
                "sandbox_id": payload["team"], "team": payload["team"],
                "hostname": payload["team"] + ".bench.invalid", "fixed_ip": "10.0.0.10",
                "server_id": "mock-server", "port_id": "mock-port", "state": "ACTIVE",
                "timings_ms": {"allocate": 2, "port": 3, "server_active": 10,
                               "health": 4, "sync": 1, "total": 20},
            })
        if request.method == "DELETE":
            deleted.append(request.url.path)
            return httpx.Response(204)
        if request.url.host == "192.0.2.1":
            assert request.headers["host"].endswith(".bench.invalid")
            return httpx.Response(200, headers={"X-Gateway-ID": "mock-gateway"})
        return httpx.Response(404)

    async def resolver(_hostname):
        return [(None, None, None, None, ("192.0.2.9", 80))]

    result = asyncio.run(run_t1(configuration(), smoke=True,
                                transport=httpx.MockTransport(handler), resolver=resolver,
                                output_root=tmp_path))
    assert result["status"] == "EXECUTED"
    assert result["n"] == result["successful_runs"] == 1
    assert result["chart_generated"] is True
    raw = list((tmp_path / "results" / "raw").glob("t1_provisioning_*.csv"))
    summary = list((tmp_path / "results" / "raw").glob("t1_provisioning_*_summary.json"))
    chart = list((tmp_path / "results" / "charts").glob("t1_provisioning_stages_*.png"))
    assert len(raw) == len(summary) == len(chart) == 1
    with raw[0].open(newline="", encoding="utf-8") as stream:
        row = next(csv.DictReader(stream))
    assert row["status"] == "SUCCESS"
    assert row["cache_state_verified"] == "False"
    assert row["stage_nova_active_timestamp_utc"] == ""
    assert row["first_http_200_utc"].endswith("Z")
    assert len(deleted) == 1 and deleted[0].startswith("/api/sandboxes/bench-t1-")
    saved = json.loads(summary[0].read_text(encoding="utf-8"))
    assert saved["metrics_ms"]["nova_active"]["mean"] == 10


def test_t1_provision_failure_is_sampled_and_cleaned(tmp_path, monkeypatch):
    monkeypatch.setenv("ADMIN_TOKEN", "unit-test-token-never-printed")
    deleted = []

    def handler(request):
        if request.url.path == "/health":
            return httpx.Response(200, json={})
        if request.url.path == "/api/sandboxes/provision":
            return httpx.Response(409, json={"error": "sandbox already exists"})
        if request.method == "DELETE":
            deleted.append(request.url.path)
            return httpx.Response(404, json={"error": "not found"})
        return httpx.Response(500)

    result = asyncio.run(run_t1(configuration(runs=1),
                                transport=httpx.MockTransport(handler),
                                resolver=lambda _host: asyncio.sleep(0),
                                output_root=tmp_path))
    assert result["status"] == "EXECUTED"
    assert result["successful_runs"] == 0 and result["failed_runs"] == 1
    assert len(deleted) == 1 and deleted[0].startswith("/api/sandboxes/bench-t1-")


def test_t1_missing_vip_is_not_executed_and_emits_reason(tmp_path, monkeypatch):
    monkeypatch.setenv("ADMIN_TOKEN", "unit-test-token-never-printed")
    cfg = configuration(runs=1)
    cfg["network"]["public_vip"] = ""
    result = asyncio.run(run_t1(cfg, output_root=tmp_path))
    assert result["status"] == "NOT EXECUTED"
    assert "PUBLIC_VIP_IP is unset" in result["reason"][0]
