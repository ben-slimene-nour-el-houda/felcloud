import { useState } from 'react'
import { Sparkline } from '../components/Sparkline'

interface Props {
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function Benchmarks({ showToast }: Props) {
  const [running, setRunning] = useState(false)
  const [history, setHistory] = useState([
    { run: '#14 (Today)', failover: '412ms', rollback: '640ms', p95: '2.31s', success: '100%' },
    { run: '#13 (Yesterday)', failover: '445ms', rollback: '710ms', p95: '2.45s', success: '96.7%' },
    { run: '#12 (2d ago)', failover: '398ms', rollback: '620ms', p95: '2.20s', success: '100%' },
  ])

  function runSuite() {
    setRunning(true)
    showToast('Benchmark Suite Running', 'Executing 10 parallel Nova VM provisions, VRRP failover probe, and config rollback check...', 'info')
    setTimeout(() => {
      setRunning(false)
      const newRun = {
        run: `#${history.length + 12} (Just now)`,
        failover: `${Math.floor(Math.random() * 50 + 390)}ms`,
        rollback: `${Math.floor(Math.random() * 60 + 590)}ms`,
        p95: '2.14s',
        success: '100%',
      }
      setHistory(prev => [newRun, ...prev])
      showToast('Benchmarks Completed', 'All SLA gates passed: Failover < 1000ms, Provision < 5s, Zero dropped packets.', 'success')
    }, 2200)
  }

  return (
    <div className="space-y-6">
      {/* Header bar */}
      <div className="card-elevated p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[#170B1B] tracking-tight">Performance Benchmarks &amp; SLAs</h2>
          <p className="text-xs text-[#827787] mt-1.5 leading-relaxed">
            Simulated stress test telemetry for OpenStack Nova Compute, Neutron IPAM, and Keepalived/HAProxy Gateway.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={runSuite}
            disabled={running}
            className="px-4 py-2 bg-[#2D0035] text-white hover:bg-[#43134c] rounded-xl text-xs font-bold flex items-center gap-2 shadow-md hover:shadow-lg transition-all duration-200 disabled:opacity-50 cursor-pointer"
          >
            <span className={`material-symbols-outlined text-[16px] ${running ? 'animate-spin' : ''}`}>
              {running ? 'refresh' : 'play_arrow'}
            </span>
            <span>{running ? 'Executing Suite…' : 'Run Benchmark Suite'}</span>
          </button>
        </div>
      </div>

      {/* 4 Metric cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        <div className="kpi-card glow-primary space-y-3">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Nova Provisioning p95</span>
            <span className="material-symbols-outlined text-[20px] text-[#74537a]">speed</span>
          </div>
          <div className="text-2xl font-bold font-mono text-[#170B1B]">1.84s / 2.31s</div>
          <div className="flex items-center justify-between">
            <span className="badge badge-emerald">Under 5s SLA</span>
            <Sparkline data={[2.4, 2.35, 2.3, 2.31]} color="emerald" width={48} height={16} />
          </div>
        </div>

        <div className="kpi-card glow-healthy space-y-3">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">VRRP Failover SLA</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">timer</span>
          </div>
          <div className="text-2xl font-bold font-mono text-[#2D0035]">412 ms</div>
          <div className="flex items-center justify-between">
            <span className="badge badge-emerald">Zero TCP Drops</span>
            <Sparkline data={[445, 420, 412, 412]} color="emerald" width={48} height={16} />
          </div>
        </div>

        <div className="kpi-card glow-info space-y-3">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Config Rollback Latency</span>
            <span className="material-symbols-outlined text-[20px] text-blue-600">history</span>
          </div>
          <div className="text-2xl font-bold font-mono text-[#170B1B]">640 ms</div>
          <div className="flex items-center justify-between">
            <span className="badge badge-blue">Verified Atomic</span>
            <Sparkline data={[710, 680, 650, 640]} color="blue" width={48} height={16} />
          </div>
        </div>

        <div className="kpi-card glow-healthy space-y-3">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Concurrent Stress Test</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">groups</span>
          </div>
          <div className="text-2xl font-bold font-mono text-emerald-700">98.4%</div>
          <div className="flex items-center justify-between">
            <span className="badge badge-emerald">10 Teams Parallel</span>
            <Sparkline data={[95, 96, 97, 98.4]} color="emerald" width={48} height={16} />
          </div>
        </div>
      </div>

      {/* Benchmark run history */}
      <div className="card-elevated overflow-hidden">
        <div className="p-5 border-b border-purple-900/5 bg-[#F8F6F9] flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-xl bg-purple-100 flex items-center justify-center text-[#2D0035]">
              <span className="material-symbols-outlined text-[18px]">history</span>
            </div>
            <h3 className="text-sm font-bold text-[#170B1B]">Benchmark Execution History</h3>
          </div>
          <span className="badge badge-purple font-mono">Strict SLA Enforcement</span>
        </div>
        <div className="overflow-x-auto">
          <table className="table-modern">
            <thead>
              <tr>
                <th>Benchmark Run</th>
                <th>Failover Duration</th>
                <th>Rollback Duration</th>
                <th>Provision P95</th>
                <th className="text-right">Success Rate</th>
              </tr>
            </thead>
            <tbody>
              {history.map((h, i) => (
                <tr key={i}>
                  <td className="font-bold text-[#170B1B] text-xs">{h.run}</td>
                  <td className="text-[#2D0035] font-mono font-bold text-xs">{h.failover}</td>
                  <td className="text-blue-900 font-mono font-semibold text-xs">{h.rollback}</td>
                  <td className="font-mono text-xs text-[#827787]">{h.p95}</td>
                  <td className="text-right">
                    <span className="badge badge-emerald">{h.success}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
