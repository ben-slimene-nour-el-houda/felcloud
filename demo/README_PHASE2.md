# FelCloud Phase 2: Integration & Testing Video Demonstration Guide

This guide prepares and orchestrates the **short video demonstration (maximum 3 minutes)** showcasing all **Phase 2 features** against real OpenStack infrastructure.

---

## 1. Feature & Points Breakdown

| Feature | Points | Demo Script | Key Verifications Demonstrated |
| :--- | :---: | :--- | :--- |
| **Concurrent Provisioning Stress Test** | **15 pts** | `demo/02a_phase2_concurrent.sh` | • **10 simultaneous team registrations** triggered concurrently (`HTTP 201 ACTIVE`).<br>• **Atomic IPAM allocation**: 10 distinct, non-overlapping IPs in `10.20.30.0/24`.<br>• **Dynamic Gateway Route Sync**: HAProxy dual-gateway routing updated without dropping traffic.<br>• **Edge VIP validation**: Each team's Host is curl-tested via Gateway VIP (`197.5.133.119`) with `X-Gateway-ID: gw1/gw2`. |
| **Automated Teardown & Lifecycle Security** | **10 pts** | `demo/02b_phase2_teardown.sh` | • **Automated Reaper Loop**: Zero-touch cleanup on TTL expiration (no manual DELETE).<br>• **Nova & Neutron Purge**: VM instance deleted from Nova; Neutron port released.<br>• **HAProxy Route Eviction**: Route dynamically unmapped (`HTTP 503/404`).<br>• **IP Lifecycle & Quarantine**: `ALLOCATED` $\rightarrow$ `QUARANTINED` (cooldown) $\rightarrow$ `FREE` sanitization.<br>• **High Availability Guarantee**: Continuous 0.5s probes to permanent team route (`team1`) prove **100% zero downtime** during teardown. |
| **Network Isolation & Access Safeguards** | **5 pts** | `demo/02c_phase2_isolation.sh` | • Probes originate directly from inside a sandbox (`team1` `10.20.30.37`).<br>• **Gateway Proxy Reachability**: Ports `80` & `443` on Gateway sandbox interfaces are **OPEN**.<br>• **Gateway Management Isolation**: Port `22` (SSH) on management IPs `10.20.10.x` is **BLOCKED**.<br>• **Inter-Sandbox Isolation**: Direct traffic between sandboxes (to `team2` `10.20.30.15` on ports 22 & 80) is **BLOCKED**.<br>• **Management Access Safeguards**: Neutron Security Group (`sg-gateway`) inspected; confirms **0 ingress rules exposing SSH**. |

---

## 2. Infrastructure Topology Reference

- **Public VIP (Keepalived Edge)**: `197.5.133.119` (`10.20.20.246` internal)
- **Gateways**:
  - `felcloud-gw1`: `10.20.10.24` (mgmt), `10.20.20.82` (edge), `10.20.30.86` (sandbox) — *Keepalived MASTER*
  - `felcloud-gw2`: `10.20.10.164` (mgmt), `10.20.20.190` (edge), `10.20.30.224` (sandbox) — *Keepalived BACKUP*
- **Bastion / Jump Host**: `197.5.133.77` (`ansible@197.5.133.77`)
- **Sandbox Subnet**: `10.20.30.0/24` (Neutron network: `felcloud-sandbox`, isolated from edge router)
- **Base Sandboxes**: `team1` (`10.20.30.37`), `team2` (`10.20.30.15`)
- **Control Plane API**: `http://localhost:8081` (`ADMIN_TOKEN=felcloud-test-token-cstam-2026`)
- **Routing Domain**: `cloudpulse.cstam.felcloud.tn`

---

## 3. Video Recording Storyboard & Timing (Max 3:00)

```
0:00 ──────────────── 0:20 ──────────────── 1:15 ──────────────── 2:10 ──────────────── 2:55 ── 3:00
 │ Architecture Intro  │ Part 1: Concurrent  │ Part 2: Automated   │ Part 3: Network     │ Outro
 │ & Live Status       │ Provisioning (10x)  │ Teardown & Reaper   │ Isolation Matrix    │ & Pass
```

### Screen Setup Recommendation
Open a 2-pane terminal window (e.g. `tmux` or side-by-side terminal):
- **Left Pane (Wide)**: Demo execution terminal (`demo/run_phase2_demo.sh` or individual scripts).
- **Right Pane**: Live OpenStack watcher:
  ```bash
  watch -n 2 'openstack server list --no-name-lookup -c Name -c Status -c Networks'
  ```

---

### Minute-by-Minute Script & Narration

#### **Segment 1: Architecture & Current Infrastructure (0:00 – 0:20)**
- **Action**: In the left pane, run:
  ```bash
  demo/run_phase2_demo.sh status
  ```
- **Narration**:
  > *"Welcome to the FelCloud Phase 2 demonstration. We are running against our live OpenStack environment with dual HAProxy gateways `gw1` and `gw2`, Keepalived VRRP on public VIP `197.5.133.119`, and the Go control plane managing IPAM and routing."*

#### **Segment 2: Part 1 — Concurrent Provisioning Stress Test (0:20 – 1:15)** [15 Points]
- **Action**: Run:
  ```bash
  demo/run_phase2_demo.sh 1
  ```
- **What is happening on screen**:
  1. 10 simultaneous POST requests sent to `/api/sandboxes/provision` (`live1` through `live10`).
  2. Nova instances spawn concurrently in the right pane.
  3. All 10 reach `HTTP 201 ACTIVE`.
  4. IPAM database lease check confirms exactly 10 distinct, non-overlapping IPs allocated in `10.20.30.0/24`.
  5. Route verification curls every team host through the Gateway VIP (`http://live{1..10}.cloudpulse.cstam.felcloud.tn/` via `197.5.133.119`), returning `HTTP 200` with header `X-Gateway-ID: gw1`.
- **Narration**:
  > *"Feature 1: 10 concurrent team registrations are submitted simultaneously. The control plane assigns unique IPAM leases without collision, provisions Nova instances, and updates HAProxy configuration across both gateways. All 10 sandboxes become ACTIVE, and traffic is immediately verified through the public VIP with gateway identity headers."*

#### **Segment 3: Part 2 — Automated Teardown & Lifecycle Security (1:15 – 2:10)** [10 Points]
- **Action**: Run:
  ```bash
  demo/run_phase2_demo.sh 2
  ```
- **What is happening on screen**:
  1. A sandbox `demoexp` is provisioned with a short TTL (`TTL=15s`).
  2. The script begins continuously probing the permanent route `team1.cloudpulse.cstam.felcloud.tn` every 500ms in the background.
  3. The automated background reaper detects expiration:
     - Sandbox status transitions to `RELEASED`.
     - Nova VM `sandbox-demoexp` is destroyed.
     - HAProxy route is automatically purged (verified non-200).
     - IP transitions to `QUARANTINED` for cooldown, then sanitized to `FREE`.
  4. Permanent route probe report displays `0 non-200 responses` (100% continuous uptime).
- **Narration**:
  > *"Feature 2: Automated lifecycle security. A sandbox is provisioned with an expiring lease. Without any manual DELETE API calls, the background reaper detects lease expiration, deletes the OpenStack resources, removes the proxy route, and moves the IP into quarantine before reclaiming it as FREE. Concurrently, probes to existing production sandboxes confirm zero service interruption."*

#### **Segment 4: Part 3 — Network Isolation & Access Safeguards (2:10 – 2:55)** [5 Points]
- **Action**: Run:
  ```bash
  demo/run_phase2_demo.sh 3
  ```
- **What is happening on screen**:
  1. An SSH session executes inside the `team1` sandbox (`10.20.30.37`).
  2. Probes verify:
     - Gateway Sandbox HTTP/HTTPS ports (`10.20.30.86:80`, `:443`, `10.20.30.224:80`, `:443`) are **OPEN**.
     - Gateway Sandbox SSH (`:22`) is **BLOCKED**.
     - Gateway Management Network (`10.20.10.x:22`) is **BLOCKED**.
     - Inter-sandbox connectivity to `team2` (`10.20.30.15:22`, `:80`) is **BLOCKED**.
  3. OpenStack Neutron security group inspection confirms `sg-gateway` has 0 ingress rules exposing SSH port 22.
  4. Script outputs `PASS: observed connectivity matches the isolation policy`.
- **Narration**:
  > *"Feature 3: Network isolation. Testing from inside the sandbox environment proves that sandboxes can only reach the gateway HTTP and HTTPS proxy ports. Direct access to the gateway management network is blocked, inter-sandbox lateral movement is blocked by Neutron security groups, and the gateway security group exposes zero ingress SSH rules."*

#### **Segment 5: Conclusion (2:55 – 3:00)**
- **Narration**:
  > *"All Phase 2 requirements — concurrent provisioning, automated teardown with IP sanitization, and strict network isolation — are verified and operational against live infrastructure."*

---

## 4. Quick Execution Commands

To run all three parts in a single automated flow:
```bash
cd /home/ben-slimene-nour-el-houda/felcloud
demo/run_phase2_demo.sh all
```

Or execute individually:
```bash
# Part 1 (15 Pts)
demo/run_phase2_demo.sh 1

# Part 2 (10 Pts)
demo/run_phase2_demo.sh 2

# Part 3 (5 Pts)
demo/run_phase2_demo.sh 3

# Cleanup demo sandboxes after recording
demo/run_phase2_demo.sh cleanup
```

---

## 5. Troubleshooting & Pre-Flight Checklist

1. **Control Plane Daemon**: Ensure the control plane is running on `:8081`:
   ```bash
   curl -s http://localhost:8081/health
   # Should return {"status":"UP", ...}
   ```
2. **OpenStack Auth**: Ensure `clouds.yaml` or OpenStack environment variables are set in your shell:
   ```bash
   openstack server list --no-name-lookup -c Name -c Status
   ```
3. **Gateway VIP Routing**: Test that the VIP responds for `team1`:
   ```bash
   curl -sI -H "Host: team1.cloudpulse.cstam.felcloud.tn" http://197.5.133.119/
   # Should return HTTP/1.0 200 OK with x-gateway-id: gw1
   ```
4. **Clean Slate**: If you need to purge any residual demo sandboxes prior to recording:
   ```bash
   demo/run_phase2_demo.sh cleanup
   ```
