# FelCloud Benchmark Report

# MOCK DATA - NOT A BENCHMARK

Mock input measurements are excluded from benchmark tables. ALLOW_MOCK=true permits this labelled dry-run report only.

## Environment

- Report date (UTC): 2026-10-10T12:20:05.879359+00:00
- Git commit: `3ec92f7444ed5585d83aadd29ecb0f096b2d3651`
- Cloud: NOT MEASURED
- Flavor: NOT MEASURED
- Image: NOT MEASURED
- MAX_CONCURRENT_PROVISIONS: NOT MEASURED
- HEALTH_PROBE_MODE: NOT MEASURED
- RATE_LIMIT_PER_MIN: 1000
- Input modes: MOCK DATA - NOT A BENCHMARK

- Input run dates: 2026-10-10T11:32:18.122629+00:00

## Provisioning latency (ms)

MOCK DATA - NOT A BENCHMARK

NOT MEASURED — mock data suppressed.

## Load test

MOCK DATA - NOT A BENCHMARK

NOT MEASURED — mock data suppressed.

## Teardown time (ms)

MOCK DATA - NOT A BENCHMARK

NOT MEASURED — mock data suppressed.

## IP recycling speed

MOCK DATA - NOT A BENCHMARK

NOT MEASURED — mock data suppressed.

## Gateway failover

NOT MEASURED — no failover results input.

## Assertions and limitations

- Percentiles use nearest-rank; p95 is reported only for n >= 20. For smaller samples, the maximum is shown instead.
- Every latency statistic includes its sample size. Samples below 10 are explicitly warned.
- Missing datasets are marked NOT MEASURED; no values are inferred.
- Failover inputs are accepted only when each run records a changed serving gateway and an approved responding-gateway verifier.
- The API's quarantine cooldown setting is inferred from lease timestamps where present; otherwise the recycle input's configured fallback is used.
- Mock results are excluded from measurement summaries even when ALLOW_MOCK=true.
- Benchmark inputs are sourced from: /home/yassmine/felcloud_final/dev/felcloud/docs/benchmarks/stress-20261010T112958Z/results.json.
- Mock measurements are never represented as live benchmark data.
