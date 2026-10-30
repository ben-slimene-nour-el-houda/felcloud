import type { BenchmarkOrigin, JOB_STATES } from '../api/contracts';

export type JobState = (typeof JOB_STATES)[number];

export interface CommittedBenchmarkRun {
  id: string;
  suite_id: string;
  status: JobState;
  origin: BenchmarkOrigin;
  started_at: string;
  finished_at?: string | null;
  parameters: Record<string, number | string>;
  environment?: string;
  metrics?: {
    attempted: number;
    succeeded: number;
    failed: number;
    p50_ms?: number | null;
    p95_ms?: number | null;
    p99_ms?: number | null;
    failover_seconds?: number | null;
  } | null;
  output?: string | null;
  evidence_url?: string | null;
}

export interface CommittedBenchmarkSuite {
  id: string;
  name: string;
  description: string;
  runs: CommittedBenchmarkRun[];
  scenarios?: Array<{
    name: string;
    status: 'COMPLETED' | 'NOT EXECUTED';
    reason?: string;
    detail?: string;
  }>;
}

export const COMMITTED_BENCHMARK_SUITES: CommittedBenchmarkSuite[] = [
  {
    id: 't1_provisioning',
    name: 'T1: MicroVM Provisioning Latency',
    description: 'Measures end-to-end sandbox creation from HTTP POST to first HTTP 200 via HAProxy.',
    runs: [
      {
        id: 't1_provisioning_20261018T175400Z',
        suite_id: 't1_provisioning',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T17:54:00Z',
        finished_at: '2026-10-18T17:57:09Z',
        parameters: { dataset: 'Dataset B (Final Run)', planned_runs: 1, concurrency: 1 },
        metrics: {
          attempted: 1,
          succeeded: 1,
          failed: 0,
          p50_ms: 178104,
          p95_ms: 178104,
          p99_ms: 178104,
        },
        output: 'T1 run completed: 1/1 sandboxes reached first HTTP 200 in 178.1s. IPAM allocate: 216ms, DNS: 69ms.',
      },
      {
        id: 't1_provisioning_20261018T133258Z',
        suite_id: 't1_provisioning',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T13:32:58Z',
        finished_at: '2026-10-18T13:36:20Z',
        parameters: { dataset: 'Dataset A (Baseline)', planned_runs: 1, concurrency: 1 },
        metrics: {
          attempted: 1,
          succeeded: 1,
          failed: 0,
          p50_ms: 182410,
          p95_ms: 182410,
          p99_ms: 182410,
        },
        output: 'T1 baseline run completed: 1/1 reached HTTP 200 in 182.4s.',
      },
    ],
  },
  {
    id: 't2_concurrent',
    name: 'T2: Concurrent Provisioning Burst',
    description: '10x burst load test evaluating IPAM locking, DHCP IP allocation, and routing convergence.',
    runs: [
      {
        id: 't2_concurrent_20261018T211200Z',
        suite_id: 't2_concurrent',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T21:12:00Z',
        finished_at: '2026-10-18T21:18:40Z',
        parameters: { dataset: 'Dataset B (10x Burst)', burst_size: 10, max_concurrent: 15 },
        metrics: {
          attempted: 10,
          succeeded: 10,
          failed: 0,
          p50_ms: 2450,
          p95_ms: 3820,
          p99_ms: 4190,
        },
        output: '10/10 sandboxes successfully allocated unique IPs without IPAM collisions.',
      },
      {
        id: 't2_concurrent_20261018T201509Z',
        suite_id: 't2_concurrent',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T20:15:09Z',
        finished_at: '2026-10-18T20:21:30Z',
        parameters: { dataset: 'Dataset A (Initial Burst)', burst_size: 10, max_concurrent: 10 },
        metrics: {
          attempted: 10,
          succeeded: 10,
          failed: 0,
          p50_ms: 2680,
          p95_ms: 4120,
          p99_ms: 4450,
        },
        output: '10/10 microVMs provisioned concurrently.',
      },
    ],
  },
  {
    id: 't3_failover',
    name: 'T3: VRRP Gateway Failover Recovery',
    description: 'Keepalived heartbeat loss, secondary election, Gratuitous ARP broadcast, and HTTP 200 traffic resumption.',
    scenarios: [
      {
        name: 'Baseline Probing',
        status: 'COMPLETED',
        detail: '60 seconds continuous probing on gw1 without failure.',
      },
      {
        name: 'Graceful Primary Failover & Failback',
        status: 'COMPLETED',
        detail: 'VRRP transition observed in 800 ms with 0 dropped packets; failback safely restored gw1.',
      },
      {
        name: 'Hard Stop Crash Scenario',
        status: 'NOT EXECUTED',
        reason: 'requires --include-hard-stop',
        detail: 'Omitted during automated benchmark to preserve host VM stability.',
      },
    ],
    runs: [
      {
        id: 't3_failover_20261019T174949Z',
        suite_id: 't3_failover',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-19T17:47:29Z',
        finished_at: '2026-10-19T17:49:49Z',
        parameters: {
          dataset: 'Dataset B (Production Setup)',
          hard_stop_scenario: 'NOT EXECUTED (requires --include-hard-stop)',
        },
        metrics: {
          attempted: 120,
          succeeded: 120,
          failed: 0,
          failover_seconds: 0.8,
        },
        output: 'VRRP failover verified: gw1 -> gw2 in 0.8s with zero failed requests. Hard stop scenario: NOT EXECUTED.',
      },
      {
        id: 't3_failover_20261019T145556Z',
        suite_id: 't3_failover',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-19T14:53:20Z',
        finished_at: '2026-10-19T14:55:56Z',
        parameters: {
          dataset: 'Dataset A (Preliminary)',
          hard_stop_scenario: 'NOT EXECUTED (requires --include-hard-stop)',
        },
        metrics: {
          attempted: 120,
          succeeded: 120,
          failed: 0,
          failover_seconds: 0.85,
        },
        output: 'VRRP failover verified: gw1 -> gw2 in 0.85s.',
      },
    ],
  },
  {
    id: 't4_ip_recycling',
    name: 'T4: IPAM Recycling & Quarantine Cooldown',
    description: 'Guarantees addresses enter 60-second cooldown after sandbox teardown and cannot be reassigned prematurely.',
    runs: [
      {
        id: 't4_ip_recycling_20261018T215336Z',
        suite_id: 't4_ip_recycling',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T21:53:36Z',
        finished_at: '2026-10-18T21:56:00Z',
        parameters: { dataset: 'Dataset B', quarantine_seconds: 60, cycles: 3 },
        metrics: {
          attempted: 3,
          succeeded: 3,
          failed: 0,
          p50_ms: 208,
          p95_ms: 209,
          p99_ms: 209,
        },
        output: 'Quarantine duration verified: 59.95s cooldown observed before address reassignment.',
      },
      {
        id: 't4_ip_recycling_20261018T214746Z',
        suite_id: 't4_ip_recycling',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-18T21:47:46Z',
        finished_at: '2026-10-18T21:50:00Z',
        parameters: { dataset: 'Dataset A', quarantine_seconds: 60, cycles: 1 },
        metrics: {
          attempted: 1,
          succeeded: 1,
          failed: 0,
          p50_ms: 212,
          p95_ms: 212,
          p99_ms: 212,
        },
        output: 'Quarantine cycle verified in preliminary test.',
      },
    ],
  },
  {
    id: 't6_reload_rollback',
    name: 'T6: Atomic Configuration Reload & Rollback',
    description: 'Hot-reloads HAProxy with zero dropped sockets and auto-rolls back invalid configs.',
    runs: [
      {
        id: 't6_reload_rollback_20261019T142804Z',
        suite_id: 't6_reload_rollback',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-19T14:28:04Z',
        finished_at: '2026-10-19T14:29:10Z',
        parameters: { dataset: 'Dataset B (Multi-Node)', nodes: 2 },
        metrics: {
          attempted: 4,
          succeeded: 4,
          failed: 0,
          p50_ms: 45,
          p95_ms: 52,
          p99_ms: 54,
        },
        output: 'Rollback verified: invalid config generation rejected by haproxy -c, active generation preserved.',
      },
      {
        id: 't6_reload_rollback_20261019T142508Z',
        suite_id: 't6_reload_rollback',
        status: 'succeeded',
        origin: 'imported',
        started_at: '2026-10-19T14:25:08Z',
        finished_at: '2026-10-19T14:26:00Z',
        parameters: { dataset: 'Dataset A (Single Node)', nodes: 1 },
        metrics: {
          attempted: 2,
          succeeded: 2,
          failed: 0,
          p50_ms: 42,
          p95_ms: 48,
          p99_ms: 48,
        },
        output: 'Single-node configuration reload & rollback verified.',
      },
    ],
  },
];
