# Global Architecture & Implementation Plan
## Resilient IP Gateway & Multi-Tenant Routing Solution (Phases 1 – 15)

---

## 1. High-Level System Architecture

The Resilient IP Gateway solution provides a **High-Availability (HA), Multi-Tenant, Zero-Downtime Gateway System** built on OpenStack, managed via **Pulumi (Go IaC)**, **Ansible**, and an **IPAM & Gateway Control Plane**.

```mermaid
graph TD
    Client["External Public Client / Local Machine"] -->|HTTP:80 / HTTPS:443 (Host: team1.local)| FIP["Floating IP: 197.5.133.119"]
    FIP -->|OpenStack NAT| VIPPort["Keepalived VIP Port: 10.20.20.246"]

    subgraph Edge Network [10.20.20.0/24]
        VIPPort -->|VRRP MASTER (150)| GW1_Edge["GW1 Edge Interface (10.20.20.82)"]
        VIPPort -.->|VRRP BACKUP Standby (100)| GW2_Edge["GW2 Edge Interface (10.20.20.190)"]
    end

    subgraph Gateways Cluster
        direction TB
        GW1["felcloud-gw1 (Master - Priority 150)\nHAProxy (TLS 443 + HTTP 80) + Keepalived (Weight 60) + DPA"]
        GW2["felcloud-gw2 (Backup - Priority 100)\nHAProxy (TLS 443 + HTTP 80) + Keepalived (Weight 60) + DPA"]
        GW1_Edge --> GW1
        GW2_Edge --> GW2
        GW1 <-->|Keepalived VRRP Heartbeat| GW2
    end

    subgraph Sandbox Network [10.20.30.0/24 - Isolated]
        GW1 -->|L7 Host Header Match: team1.local| T1["team1 Sandbox VM (10.20.30.70:80)"]
        GW1 -->|L7 Host Header Match: team2.local| T2["team2 Sandbox VM (10.20.30.192:80)"]
        
        GW2 -.->|Standby Proxy Path| T1
        GW2 -.->|Standby Proxy Path| T2
    end

    subgraph Security Isolation
        T1 -.-x|BLOCKED by sg-sandbox| T2
    end

    subgraph Management Network [10.20.10.0/24]
        Bastion["felcloud-bastion VM (10.20.10.246)"]
        IPAM["IPAM Control Plane & REST API (Port 8080)"]
        IPAM -->|Two-Gateway Pre-Validation & Zero-Downtime Reload| GW1
        IPAM -->|Two-Gateway Pre-Validation & Zero-Downtime Reload| GW2
    end
```

---

## 2. IPAM & Subnet Topology Matrix

The solution partitions the OpenStack tenant network into **three dedicated `/24` subnets**:

| Subnet Name | CIDR | Purpose | Assigned IP Addresses |
| :--- | :--- | :--- | :--- |
| **`felcloud-mgmt`** | `10.20.10.0/24` | Isolated Management Control Plane & SSH Jump | `gw1`: `10.20.10.24`<br>`gw2`: `10.20.10.164`<br>`bastion`: `10.20.10.246` |
| **`felcloud-edge`** | `10.20.20.0/24` | Public Gateway, HTTP/HTTPS & Keepalived VRRP Traffic | `gw1`: `10.20.20.82`<br>`gw2`: `10.20.20.190`<br>`bastion`: `10.20.20.177`<br>**`VIP`**: `10.20.20.246` |
| **`felcloud-sandbox`** | `10.20.30.0/24` | Multi-Tenant Isolated Team Backends | `gw1`: `10.20.30.86`<br>`gw2`: `10.20.30.224`<br>`team1`: `10.20.30.70`<br>`team2`: `10.20.30.192` |

---

## 3. Phased Implementation Roadmap

### Phase 1: Networks & Subnets Infrastructure
- Created three isolated OpenStack networks (`edge`, `sandbox`, `mgmt`) with non-overlapping `/24` subnets.

### Phase 2: Edge Router & Public SNAT Gateway
- Provisioned `felcloud-edge-router` connected to external gateway IP `197.5.133.110` for outbound SNAT internet connectivity.

### Phase 3: Security Groups & Isolation Policies
- **`sg-gateway`**: Ingress HTTP (80/tcp), HTTPS (443/tcp), and VRRP protocol 112 between gateway edge interfaces.
- **`sg-sandbox`**: Ingress HTTP (80/tcp) strictly from gateway instances. **Blocks all inter-sandbox traffic**.
- **`sg-management`**: Ingress for internal control plane communication, Data Plane API (5555/tcp), and SSH jump host.

### Phase 4: Virtual IP (VIP) & Gateway Interfaces
- Created unbound reservation port `felcloud-vip-port` (`10.20.20.246`).
- Configured `allowed_address_pairs` on gateway edge ports to allow VIP binding without Neutron IP spoofing drops.

### Phase 5: Gateway & Bastion Compute Deployment
- Provisioned anti-affinity gateway instances (`felcloud-gw1` & `felcloud-gw2`) and `felcloud-bastion` jump host using Nova compute.

### Phase 6: High Availability & Guaranteed Failover (Keepalived)
- Configured Keepalived on both gateways:
  - `gw1`: MASTER (Priority 150)
  - `gw2`: BACKUP (Priority 100)
- **Guaranteed failover on HAProxy-only failure**:
  - `keepalived_check_weight: 60`
  - When `check_haproxy.sh` fails on GW1, priority drops from $150 \rightarrow 90 < 100$ (GW2), guaranteeing immediate VRRP master election.

### Phase 7: Layer 7 Multi-Tenant Reverse Proxy & TLS (HAProxy)
- Configured HAProxy on port 80 (HTTP) and port 443 (HTTPS with TLS termination):
  - Certificate: `/etc/haproxy/certs/felcloud-gateway.pem` with wildcard SANs (`*.local`, `*.felcloud.local`).
  - `Host: team1.local` $\rightarrow$ `team1_backend` (`10.20.30.70:80`)
  - `Host: team2.local` $\rightarrow$ `team2_backend` (`10.20.30.192:80`)
  - Default 503 maintenance page for unregistered hosts.

### Phase 8: HAProxy Data Plane API (DPA)
- Installed and configured HAProxy Data Plane API service (`dataplaneapi`) on port 5555.
- Supports authenticated configuration operations and reload triggers.

### Phase 9: IPAM Address Quarantine & Automatic Reclamation
- Transactional SQLite IPAM database (`ipam/ipam.db`).
- Released IP addresses enter `QUARANTINED` status with a configurable cooldown period (`quarantine_until`).
- Immediate reallocation strictly avoids quarantined addresses until cooldown expires and reclamation executes.

### Phase 10: Pre-Flight Sandbox Health Check before Route Opening
- Before a route is activated in HAProxy, the control plane executes an HTTP GET probe on `<sandbox_ip>:<port>`.
- Route is added to HAProxy **only** upon health check success.
- If health check fails, the route is not opened, preventing live gateway 502/503 errors.

### Phase 11: Two-Gateway Pre-Validation & Zero-Downtime Reload with Auto-Rollback
- Pre-validates candidate configuration syntax locally and on both gateways before deployment.
- If validation fails on either node, deployment aborts immediately, leaving the active configuration untouched.
- Creates timestamped configuration backups and executes zero-downtime graceful reload.
- If post-reload check fails, triggers **automatic rollback** to the previous generation.

### Phase 12: Automated Sandbox Provisioning API
- REST API server running on port 8080 (`python3 ipam/ipam_control.py serve`):
  - `GET /health`
  - `POST /ipam/leases/allocate`
  - `POST /ipam/leases/release`
  - `POST /ipam/leases/reclaim`
  - `POST /api/sandboxes/provision`
  - `POST /api/gateways/sync`
  - `POST /api/gateways/rollback`

### Phase 13: Automated Testing & Failover Measurement Suite
- [`scripts/run-all-tests.sh`](file:///home/ben-slimene-nour-el-houda/ip_resilien/scripts/run-all-tests.sh): Master test runner validating all system components.
- [`scripts/failover-test.sh`](file:///home/ben-slimene-nour-el-houda/ip_resilien/scripts/failover-test.sh): Measures sub-second VRRP failover times and packet loss during simulated node or HAProxy-only outages.
- [`scripts/recycle-test.sh`](file:///home/ben-slimene-nour-el-houda/ip_resilien/scripts/recycle-test.sh): Tests IP lease allocation, quarantine enforcement, and auto-reclamation.

---

## 4. Verification Runbook

| Test Goal | Command |
| :--- | :--- |
| **Run Complete Automated Test Suite** | `./scripts/run-all-tests.sh` |
| **Run IP Recycling & Quarantine Test** | `./scripts/recycle-test.sh` |
| **Run Measured VRRP Failover Test** | `FAILURE_MODE=haproxy-only ./scripts/failover-test.sh` |
| **Launch IPAM REST API Server** | `python3 ipam/ipam_control.py serve --port 8080` |
| **Onboard Team with Pre-Flight Check** | `python3 ipam/ipam_control.py add-team --name team3` |
| **Test Route via HTTP & HTTPS** | `curl -k -H "Host: team1.local" https://197.5.133.119`<br>`curl -H "Host: team1.local" http://197.5.133.119` |
