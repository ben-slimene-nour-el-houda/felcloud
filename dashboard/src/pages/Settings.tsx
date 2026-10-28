import { useState } from 'react'

interface Props {
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function Settings({ showToast }: Props) {
  const [authUrl] = useState('https://keystone.openstack.felcloud.tn:5000/v3')
  const [projectName] = useState('cstam-hackathon-2026')
  const [leaseTtl, setLeaseTtl] = useState(3600)
  const [quarantineSec, setQuarantineSec] = useState(180)
  const [prioMaster, setPrioMaster] = useState(101)
  const [prioBackup, setPrioBackup] = useState(100)
  const [maintenanceMode, setMaintenanceMode] = useState(false)

  function handleSave() {
    showToast('Configuration Saved', 'Platform environment variables and IPAM timing parameters persisted.', 'success')
  }

  return (
    <div className="space-y-6">
      {/* Header bar */}
      <div className="card-elevated p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[#170B1B] tracking-tight">Platform Configuration &amp; OpenStack Bindings</h2>
          <p className="text-xs text-[#827787] mt-1.5 leading-relaxed">Control plane environment variables, IPAM timing rules, and VRRP cluster priorities.</p>
        </div>
        <button
          onClick={handleSave}
          className="px-4 py-2 bg-[#2D0035] text-white hover:bg-[#43134c] rounded-xl text-xs font-bold flex items-center gap-2 shadow-md hover:shadow-lg transition-all duration-200 cursor-pointer"
        >
          <span className="material-symbols-outlined text-[16px]">save</span>
          <span>Save Configuration</span>
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 text-xs">
        {/* OpenStack Auth */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-purple-900/5">
            <span className="material-symbols-outlined text-[20px] text-[#2D0035]">cloud</span>
            <h3 className="font-bold text-[#170B1B] text-sm">OpenStack Keystone &amp; Neutron</h3>
          </div>
          <div className="space-y-3.5">
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">Keystone Auth URL</label>
              <input
                className="w-full p-2.5 bg-[#F8F6F9] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none"
                value={authUrl}
                readOnly
              />
            </div>
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">Project Name / Tenant</label>
              <input
                className="w-full p-2.5 bg-[#F8F6F9] rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none"
                value={projectName}
                readOnly
              />
            </div>
            <div className="p-3 rounded-2xl bg-emerald-50/70 border border-emerald-200/50 text-emerald-900 flex items-center gap-2.5">
              <span className="material-symbols-outlined text-[18px] text-emerald-700">check_circle</span>
              <span className="font-medium text-xs">Neutron Subnet 10.20.10.0/24 bound with OVS port security.</span>
            </div>
          </div>
        </div>

        {/* IPAM & Cooldown */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-purple-900/5">
            <span className="material-symbols-outlined text-[20px] text-[#2D0035]">schedule</span>
            <h3 className="font-bold text-[#170B1B] text-sm">IPAM Timing &amp; Quarantine Policy</h3>
          </div>
          <div className="grid grid-cols-2 gap-3.5">
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">Default Lease TTL (s)</label>
              <input
                type="number"
                value={leaseTtl}
                onChange={e => setLeaseTtl(Number(e.target.value))}
                className="w-full p-2.5 bg-[#F8F6F9] focus:bg-white rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all"
              />
            </div>
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">Quarantine Cooldown (s)</label>
              <input
                type="number"
                value={quarantineSec}
                onChange={e => setQuarantineSec(Number(e.target.value))}
                className="w-full p-2.5 bg-[#F8F6F9] focus:bg-white rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all"
              />
            </div>
          </div>
          <p className="text-[11px] text-[#827787] leading-relaxed">
            Released IPs enter quarantine for {quarantineSec}s to prevent ARP collisions and ensure clean socket teardowns.
          </p>
        </div>

        {/* VRRP Priority Policies */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-purple-900/5">
            <span className="material-symbols-outlined text-[20px] text-[#2D0035]">sync_alt</span>
            <h3 className="font-bold text-[#170B1B] text-sm">Keepalived VRRP Priority Election</h3>
          </div>
          <div className="grid grid-cols-2 gap-3.5">
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">gw1 (Master Prio)</label>
              <input
                type="number"
                value={prioMaster}
                onChange={e => setPrioMaster(Number(e.target.value))}
                className="w-full p-2.5 bg-[#F8F6F9] focus:bg-white rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all"
              />
            </div>
            <div>
              <label className="block text-[#827787] text-[11px] mb-1 font-mono uppercase font-semibold">gw2 (Backup Prio)</label>
              <input
                type="number"
                value={prioBackup}
                onChange={e => setPrioBackup(Number(e.target.value))}
                className="w-full p-2.5 bg-[#F8F6F9] focus:bg-white rounded-xl font-mono text-xs text-[#170B1B] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all"
              />
            </div>
          </div>
          <p className="text-[11px] text-[#827787] leading-relaxed">
            gw1 priority ({prioMaster}) exceeds gw2 ({prioBackup}) to ensure deterministic primary master allocation.
          </p>
        </div>

        {/* Platform Maintenance Mode */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-purple-900/5">
            <span className="material-symbols-outlined text-[20px] text-[#2D0035]">build_circle</span>
            <h3 className="font-bold text-[#170B1B] text-sm">Global Ingress Maintenance Banner</h3>
          </div>
          <div className="flex items-center justify-between p-4 bg-[#F8F6F9] rounded-2xl">
            <div>
              <div className="font-bold text-[#170B1B]">Display 503 Maintenance Page</div>
              <p className="text-[11px] text-[#827787] mt-0.5">Routes all incoming traffic to maintenance splash during cluster upgrade.</p>
            </div>
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={maintenanceMode}
                onChange={e => setMaintenanceMode(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-purple-950/15 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-[#2D0035]"></div>
            </label>
          </div>
        </div>
      </div>
    </div>
  )
}
