import { useState } from 'react'
import Icon from '../components/Icon'
import type { SharedState } from '../api'

interface Props {
  sharedState?: SharedState | null
  refreshSharedState?: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function Settings({ sharedState, refreshSharedState, showToast }: Props) {
  const [authUrl] = useState('https://keystone.openstack.felcloud.tn:5000/v3')
  const [projectName] = useState('cstam-hackathon-2026')
  const [leaseTtl, setLeaseTtl] = useState(3600)
  const [quarantineSec, setQuarantineSec] = useState(180)
  const [prioMaster, setPrioMaster] = useState(101)
  const [prioBackup, setPrioBackup] = useState(100)
  const [maintenanceMode, setMaintenanceMode] = useState(false)
  const [apiRateLimit, setApiRateLimit] = useState(120)

  function handleSave() {
    showToast('Settings Saved', 'Platform parameters, IPAM timing rules, and VRRP priorities have been updated.', 'success')
  }

  function handleReset() {
    setLeaseTtl(3600)
    setQuarantineSec(180)
    setPrioMaster(101)
    setPrioBackup(100)
    setMaintenanceMode(false)
    setApiRateLimit(120)
    showToast('Defaults Restored', 'All configuration fields reset to production baseline.', 'info')
  }

  return (
    <div className="space-y-6 max-w-6xl">
      {/* Header bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-bold text-[#170B1B] tracking-tight">Platform Configuration</h1>
          <p className="text-xs text-[#5E4D64] mt-0.5 font-medium">
            Tune how long team sandboxes live, how quickly IPs are recycled, and which gateway takes priority during failover.
          </p>
        </div>
        <div className="flex items-center gap-2.5">
          <button
            onClick={handleReset}
            className="px-3.5 py-2 bg-transparent hover:bg-slate-100 text-[#5E4D64] border border-[#E8E0EA] rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer"
          >
            <Icon name="history" size={15} />
            <span>Reset Defaults</span>
          </button>
          <button
            onClick={handleSave}
            className="px-4 py-2 bg-[#2D0035] text-white hover:bg-[#492B50] rounded-xl text-xs font-bold flex items-center gap-2 shadow-sm transition-all cursor-pointer"
          >
            <Icon name="check" size={15} className="text-[#FFD200]" />
            <span>Save Changes</span>
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 text-xs">
        {/* 1. OpenStack & Identity Bindings */}
        <div className="bg-white border border-[#E8E0EA] rounded-2xl p-5 shadow-xs space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-[#E8E0EA]">
            <div className="w-8 h-8 rounded-xl bg-purple-50 text-[#2D0035] flex items-center justify-center">
              <Icon name="hub" size={17} />
            </div>
            <div>
              <h2 className="font-bold text-sm text-[#170B1B]">OpenStack Keystone &amp; Neutron</h2>
              <p className="text-[11px] text-[#5E4D64]">Authentication endpoints and network tenant identity</p>
            </div>
          </div>
          <div className="space-y-3">
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">Keystone Auth URL</label>
              <input
                className="w-full px-3 py-2 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B]"
                value={authUrl}
                readOnly
              />
            </div>
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">Project Name / Tenant</label>
              <input
                className="w-full px-3 py-2 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B]"
                value={projectName}
                readOnly
              />
            </div>
            <div className="p-3 rounded-xl bg-emerald-50 border border-emerald-200 text-emerald-900 flex items-center gap-2.5">
              <Icon name="check_circle" size={16} className="text-emerald-700 flex-shrink-0" />
              <span className="text-[11px] font-medium leading-relaxed">
                Neutron Subnet <strong>10.20.10.0/24</strong> bound with OVS port security.
              </span>
            </div>
          </div>
        </div>

        {/* 2. IPAM & Lifecycle Timing */}
        <div className="bg-white border border-[#E8E0EA] rounded-2xl p-5 shadow-xs space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-[#E8E0EA]">
            <div className="w-8 h-8 rounded-xl bg-blue-50 text-blue-700 flex items-center justify-center">
              <Icon name="timer" size={17} />
            </div>
            <div>
              <h2 className="font-bold text-sm text-[#170B1B]">IPAM Timing &amp; Quarantine Policy</h2>
              <p className="text-[11px] text-[#5E4D64]">How long each team VM holds its IP, and the safety cooldown after it's released</p>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">Default TTL (seconds)</label>
              <input
                type="number"
                value={leaseTtl}
                onChange={e => setLeaseTtl(Number(e.target.value))}
                className="w-full px-3 py-2 bg-white border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:border-purple-600 focus:ring-1 focus:ring-purple-600"
              />
              <span className="text-[10px] text-[#5E4D64] mt-1 block">Default: 3600s (1 hour)</span>
            </div>
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">Quarantine Cooldown (s)</label>
              <input
                type="number"
                value={quarantineSec}
                onChange={e => setQuarantineSec(Number(e.target.value))}
                className="w-full px-3 py-2 bg-white border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:border-purple-600 focus:ring-1 focus:ring-purple-600"
              />
              <span className="text-[10px] text-[#5E4D64] mt-1 block">ARP safety flush delay</span>
            </div>
          </div>
          <div className="p-3 bg-purple-50/60 border border-purple-100 rounded-xl text-[11px] text-[#2D0035] leading-relaxed">
            Released IPs enter quarantine for <strong>{quarantineSec}s</strong> to prevent packet collisions and guarantee complete TCP conntrack teardown.
          </div>
        </div>

        {/* 3. Keepalived VRRP Priority Election */}
        <div className="bg-white border border-[#E8E0EA] rounded-2xl p-5 shadow-xs space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-[#E8E0EA]">
            <div className="w-8 h-8 rounded-xl bg-amber-50 text-amber-700 flex items-center justify-center">
              <Icon name="sync" size={17} />
            </div>
            <div>
              <h2 className="font-bold text-sm text-[#170B1B]">Keepalived VRRP Priorities</h2>
              <p className="text-[11px] text-[#5E4D64]">Higher priority wins the Virtual IP. The gap prevents flapping between the two nodes.</p>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">gw1 Priority (Master)</label>
              <input
                type="number"
                value={prioMaster}
                onChange={e => setPrioMaster(Number(e.target.value))}
                className="w-full px-3 py-2 bg-white border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:border-purple-600 focus:ring-1 focus:ring-purple-600"
              />
            </div>
            <div>
              <label className="block text-[#5E4D64] text-[11px] font-bold mb-1 font-mono uppercase">gw2 Priority (Backup)</label>
              <input
                type="number"
                value={prioBackup}
                onChange={e => setPrioBackup(Number(e.target.value))}
                className="w-full px-3 py-2 bg-white border border-[#E8E0EA] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:border-purple-600 focus:ring-1 focus:ring-purple-600"
              />
            </div>
          </div>
          <p className="text-[11px] text-[#5E4D64] leading-relaxed">
            Gateway 1 priority ({prioMaster}) exceeds Gateway 2 ({prioBackup}) by {prioMaster - prioBackup} points, ensuring stable VRRP advertisements without flapping.
          </p>
        </div>

        {/* 4. Platform Safety & Maintenance */}
        <div className="bg-white border border-[#E8E0EA] rounded-2xl p-5 shadow-xs space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-[#E8E0EA]">
            <div className="w-8 h-8 rounded-xl bg-rose-50 text-rose-700 flex items-center justify-center">
              <Icon name="shield" size={17} />
            </div>
            <div>
              <h2 className="font-bold text-sm text-[#170B1B]">Traffic &amp; Ingress Safeguards</h2>
              <p className="text-[11px] text-[#5E4D64]">Global rate protection and maintenance splash</p>
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-1.5">
              <label className="text-[#5E4D64] text-[11px] font-bold font-mono uppercase">Default Rate Limit</label>
              <span className="font-mono font-bold text-xs text-[#170B1B]">{apiRateLimit} req/min</span>
            </div>
            <input
              type="range"
              min="30"
              max="600"
              step="30"
              value={apiRateLimit}
              onChange={e => setApiRateLimit(Number(e.target.value))}
              className="w-full accent-purple-700 cursor-pointer"
            />
          </div>

          <div className="flex items-center justify-between p-3.5 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl">
            <div>
              <div className="font-bold text-xs text-[#170B1B]">Global Maintenance Mode</div>
              <p className="text-[11px] text-[#5E4D64] mt-0.5">Return HTTP 503 maintenance page for all frontends.</p>
            </div>
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={maintenanceMode}
                onChange={e => setMaintenanceMode(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-slate-300 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-[#2D0035]"></div>
            </label>
          </div>
        </div>
      </div>
    </div>
  )
}
