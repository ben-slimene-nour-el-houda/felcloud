import asyncio
import csv
from pathlib import Path

import httpx

from bench.lib.api_client import AsyncControlPlaneClient
from bench.lib.cleanup import cleanup_created_sandboxes
from bench.lib.prober import BackgroundProber, wait_until_http_200
from bench.lib.stats import compute_stats


def test_async_api_client_timing_and_auth():
    seen = []

    def handler(request):
        seen.append((request.method, request.url.path, request.headers.get("authorization")))
        return httpx.Response(201, json={"state": "ACTIVE", "timings_ms": {"total": 8}})

    async def run():
        async with AsyncControlPlaneClient("https://mock.invalid", "test-secret",
                                           transport=httpx.MockTransport(handler)) as client:
            response = await client.provision_sandbox("bench-unit-1", ttl=17)
        return response

    result = asyncio.run(run())
    assert result.status_code == 201
    assert result.data["timings_ms"]["total"] == 8
    assert result.elapsed_seconds >= 0
    assert result.finished_perf_counter >= result.started_perf_counter
    assert result.started_utc.endswith("Z") and result.finished_utc.endswith("Z")
    assert seen == [("POST", "/api/sandboxes/provision", "Bearer test-secret")]


def test_wait_until_http_200_preserves_host_and_records_first_success():
    statuses = iter((503, 200))
    seen_hosts = []

    def handler(request):
        seen_hosts.append(request.headers.get("host"))
        return httpx.Response(next(statuses))

    async def run():
        return await wait_until_http_200("http://vip.invalid/", "bench-unit.example",
                                         timeout=1, interval=0,
                                         transport=httpx.MockTransport(handler))

    result = asyncio.run(run())
    assert result.success is True
    assert result.status_code == 200
    assert result.attempts == 2
    assert result.first_200_utc.endswith("Z")
    assert result.first_200_perf_counter >= result.started_perf_counter
    assert seen_hosts == ["bench-unit.example", "bench-unit.example"]


def test_background_prober_writes_utc_monotonic_csv(tmp_path):
    def handler(_request):
        return httpx.Response(200, headers={"X-Gateway-ID": "mock-gw"})

    async def run():
        prober = BackgroundProber("http://vip.invalid/", "bench-unit.example",
                                  interval_seconds=0.005, output_csv=tmp_path / "probes.csv",
                                  transport=httpx.MockTransport(handler))
        await prober.start()
        await asyncio.sleep(0.025)
        return await prober.stop()

    rows = asyncio.run(run())
    path = tmp_path / "probes.csv"
    with path.open(newline="", encoding="utf-8") as stream:
        saved = list(csv.DictReader(stream))
    assert rows and len(saved) == len(rows)
    assert all(row["success"] and row["gateway_id"] == "mock-gw" for row in rows)
    assert all(row["timestamp_utc"].endswith("Z") for row in rows)
    assert all(float(row["perf_counter_end"]) >= float(row["perf_counter_start"]) for row in saved)


def test_stats_helpers_report_all_requested_fields():
    stats = compute_stats([1, 2, 3, 4, 5])
    assert set(stats) == {"n", "min", "mean", "std", "p50", "p90", "p95", "p99", "max"}
    assert stats == {"n": 5, "min": 1, "mean": 3, "std": 1.4142,
                     "p50": 3, "p90": 4.6, "p95": 4.8, "p99": 4.96, "max": 5}


def test_cleanup_only_calls_delete_for_bench_resources():
    deleted = []

    class MockResponse:
        status_code = 204
        data = {}

    class MockAPI:
        async def delete_sandbox(self, name):
            deleted.append(name)
            return MockResponse()

    result = asyncio.run(cleanup_created_sandboxes(
        MockAPI(), ["bench-unit-1", "team1", "team2", "other", "bench-unit-1"], verbose=False))
    assert deleted == ["bench-unit-1"]
    assert result[0]["deleted"] is True
    assert {r["sandbox"] for r in result if not r["deleted"]} == {"team1", "team2", "other"}
