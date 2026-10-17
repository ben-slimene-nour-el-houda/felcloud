# FelCloud Resilient IP Optimizer — Benchmark Environment & Discovery Specification

**Date:** 2026-10-17T15:10:05Z  
**Git Commit Hash:** `243a906d691b261de6a28f1519b1297fe35c9df9`  
**Repository Branch:** `Dev`  
**Challenge Track:** CSTAM 3.0 (FelCloud Challenge)

---

## 1. System Discovery & Architecture

### 1.1 Backend & Control Plane
- **Language & Runtime:** Go 1.22+ (pure Go standard library HTTP server + Gophercloud v2 SDK).
- **Persistence:** SQLite3 database (`ipam.db` with WAL mode: `_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate`).
- **Server Entrypoint:** `controlplane/felcloud-controlplane serve --port 8081` (or `go run . serve`).
- **Authentication Method:**
  - HTTP Bearer Token via `Authorization: Bearer <ADMIN_TOKEN>`.
  - Configured via `ADMIN_TOKEN` environment variable (minimum 24 characters, constant-time SHA-256 hash comparison).
  - Open endpoints: `GET /health` and `GET /api/config`.
- **API Endpoints:**
  | Category | Method | Path | Functionality |
  |---|---|---|---|
  | **Health & Meta** | `GET` | `/health` | DB ping, reaper status, background worker metrics |
  | | `GET` | `/api/config` | Public gateway VIP and domain metadata |
  | **Gateways** | `GET` | `/api/gateways/status` | Live probe status of gw1 & gw2, DPA readiness, VIP holder |
  | | `GET` | `/api/gateways/dpa` | Direct HAProxy Data Plane API probe |
  | | `GET` | `/api/gateways/logs` | Fetch systemd journal logs (`haproxy` or `keepalived`) |
  | | `GET` | `/api/gateways/events` | Audit log of failovers, syncs, rollbacks |
  | | `GET` | `/api/gateways/generations`| Configuration version history and diffs |
  | | `POST` | `/api/gateways/failover` | Coordinated VRRP failover trigger to peer gateway |
  | | `POST` | `/api/gateways/sync` | Force dynamic configuration sync to dual gateways |
  | | `POST` | `/api/gateways/rollback` | Atomic rollback to previous stable configuration generation |
  | **Sandboxes** | `GET` | `/api/sandboxes` | List currently active provisioned sandboxes |
  | | `POST` | `/api/sandboxes/provision` | Atomically allocate IP, provision OpenStack VM, probe backend, deploy route |
  | | `POST` | `/api/sandboxes/extend` | Extend lease TTL for an active sandbox |
  | | `POST` | `/api/sandboxes/custom-domain`| Bind custom FQDN to an existing sandbox backend |
  | | `DELETE`| `/api/sandboxes/{id}` | Teardown sandbox VM, delete Neutron port, release IP to quarantine |
  | **IPAM** | `GET` | `/api/leases` | View all allocations and quarantine timers |
  | | `POST` | `/api/leases/allocate` | Direct IPAM lease allocation |
  | | `POST` | `/api/leases/release` | Direct IPAM lease release (enters quarantine) |
  | | `POST` | `/api/leases/reclaim` | Trigger reclamation of expired quarantined IPs |
  | **Reconcile** | `GET` | `/api/reconcile` | Detect drift between SQLite IPAM and OpenStack resources |
  | | `POST` | `/api/reconcile/apply` | Reconcile and purge orphan OpenStack resources |

---

### 1.2 Gateway Cluster & Routing Engine
- **Gateway Nodes:**
  - `gw1`: Management IP `10.20.10.24`, Edge IP `10.20.20.82`, Sandbox IP `10.20.30.86`, Keepalived Role `MASTER` (Priority 150).
  - `gw2`: Management IP `10.20.10.164`, Edge IP `10.20.20.190`, Sandbox IP `10.20.30.224`, Keepalived Role `BACKUP` (Priority 100).
- **High Availability & VRRP:**
  - Keepalived 2.2.8 (VRRP v2, VRID 51) over edge network `10.20.20.0/24`.
  - Virtual IP (VIP): `10.20.20.246` on edge network, mapped to Public Floating IP `197.5.133.119`.
  - Health check script: `/usr/local/bin/check_haproxy.sh` with weight `-60` (Master drops from 150 to 90, lower than Standby's 100).
- **Reverse Proxy & TLS:**
  - HAProxy 2.8.5 with HTTP/2 (ALPN `h2,http/1.1`) and TLS termination.
  - Certificate: `/etc/haproxy/certs/felcloud-gateway.pem` (wildcard SAN for `*.cstam.felcloud.tn`, `*.felcloud.local`, VIP, bastion).
  - Rate limiting stick-table: 100 req/10s with HTTP 429 denial.
  - Fallback backend: `default_maintenance` returning HTTP 503 maintenance page.
- **Reload & Rollback Mechanism:**
  - Primary Mechanism: HAProxy Data Plane API (DPA) v3 via HTTP REST on port 5555 (`dpa_client.go`).
    - Opens atomic transaction (`POST /v3/services/haproxy/transactions`).
    - Pushes raw configuration (`PUT /v3/services/haproxy/configuration/raw?transaction_id=...`).
    - Commits transaction (`PUT /v3/services/haproxy/transactions/{id}`).
  - Fallback Mechanism: Dual `haproxy -c` validation + transactional Ansible reload playbook.
  - Versioning: `config_generations` table stores raw configuration text, SHA-256 hash, and deployment status.
  - Rollback: Re-pushes the previous `COMMITTED` generation's raw configuration via DPA or Ansible.

---

### 1.3 IPAM & Address Reclamation Design
- **Database Engine:** Embedded SQLite3 with WAL mode and immediate transaction locks.
- **Tables:**
  - `subnets`: Defines CIDR `10.20.30.0/24` (usable host range: `10.20.30.10` to `10.20.30.240`).
  - `ip_allocations`: Tracks IP, entity, status (`ALLOCATED`, `QUARANTINED`, `FREE`), `quarantined_at`, `quarantine_until`.
  - `leases`: Tracks sandbox lease metadata, TTL, expiration timestamp.
  - `events`: Tracks allocation, release, quarantine, and reclamation events.
- **Lease Timers & Parameters:**
  - Default Lease TTL: 14,400 s (4 hours), configurable up to 86,400 s (24 hours).
  - Quarantine Cooldown: 120 s to 300 s (configurable via `QUARANTINE_SECONDS`).
- **Release Lifecycle:**
  1. Sandbox deletion or lease expiry triggers release.
  2. IP status transitions from `ALLOCATED` to `QUARANTINED` with `quarantine_until = NOW() + cooldown`.
  3. While `QUARANTINED`, the IP cannot be reallocated by new requests.
  4. Background reaper worker checks every 10 s (`reaperIntervalSeconds = 10`), moving expired quarantined IPs to `FREE`.
  5. The next sandbox allocation reclaims a `FREE` IP or takes the next available pool host.

---

### 1.4 Domain Mapping, DNS & Proxy Rules
- **Domain Structure:** Subdomains are structured as `<team>.cstam.felcloud.tn` (or custom domains).
- **DNS Architecture:**
  - OpenStack Designate DNSaaS manages zone `cstam.felcloud.tn.`.
  - Wildcard A-record `*.cstam.felcloud.tn.` points to Edge VIP `10.20.20.246` (public floating IP `197.5.133.119`).
  - Script [`scripts/designate-dns-sync.sh`](file:///home/ben-slimene-nour-el-houda/felcloud/scripts/designate-dns-sync.sh) ensures zone and wildcard A-records exist.
- **Proxy Routing:**
  - HAProxy generates ACLs for both HTTP (port 80) and HTTPS (port 443):
    ```haproxy
    acl is_<team> hdr(host) -i <team>.cstam.felcloud.tn
    use_backend <team>_backend if is_<team>
    ```
  - Backend configuration:
    ```haproxy
    backend <team>_backend
        mode http
        balance roundrobin
        server <team>_srv <sandbox_ip>:80 check inter 2000 rise 2 fall 3
    ```

---

### 1.5 Network Isolation & Access Safeguards
- **Topology:**
  - `felcloud-mgmt` (`10.20.10.0/24`): Internal management network for DPA, Keepalived synchronization, and SSH.
  - `felcloud-edge` (`10.20.20.0/24`): Edge network connected to `felcloud-edge-router` with SNAT/floating IPs and VRRP VIP.
  - `felcloud-sandbox` (`10.20.30.0/24`): Fully isolated multi-tenant network with NO default router gateway attached.
- **Neutron Security Groups:**
  - `sg-gateway`: Ingress HTTP (80), HTTPS (443), VRRP (protocol 112 between edge ports), SSH (22).
  - `sg-management`: Ingress SSH (22), DPA (5555 from `10.20.10.0/24`), management internal communication.
  - `sg-sandbox`: Strictly enforces multi-tenant isolation:
    - Ingress TCP port 80 and 8080 ONLY from `sg-gateway` (remote security group reference).
    - Blocks all inter-sandbox traffic (no remote subnet rule, no inter-sandbox ingress).
    - Blocks sandbox access to gateway management ports (5555, SSH) and control plane.

---

### 1.6 Environment Access & Credentials
- **OpenStack Access:**
  - Configured via `~/.config/openstack/clouds.yaml` (Cloud: `felcloud`, or via `$OS_CLOUD`).
  - Region: `North-Africa`, Availability Zone: `TN-Carthage`.
  - Compute API: `https://compute.api.felcloud.io/v2.1` (Microversion 2.100).
  - Network API: `https://network.api.felcloud.io/v2.0`.
  - Identity API: `https://identity.api.felcloud.io/v3` (Keystone v3.14).
- **Gateway & Host SSH Access:**
  - Bastion Jump Host: `$BASTION_IP` (User: `ansible`, Private Key: `$SSH_KEY_PATH`).
  - Gateways: `gw1` (`10.20.10.24`) and `gw2` (`10.20.10.164`) reachable via ProxyJump through bastion.
  - SSH Options required: `IPQoS=none` (prevents packet drop/blackholing on carrier networks).
  - Credentials & Secrets: Passed via environment variables (`$DPA_USER`, `$DPA_PASS`, `$ADMIN_TOKEN`).

---

## 2. Live Environment Snapshot

| Component / Property | Discovered Value |
|---|---|
| **Date & Time (UTC)** | `2026-10-17T15:10:05Z` |
| **Git Commit Hash** | `243a906d691b261de6a28f1519b1297fe35c9df9` |
| **OpenStack Release** | OpenStack 2024.1 (Caracal) / Nova 2.100 / Keystone v3.14 |
| **Cloud Region / AZ** | `North-Africa` / `TN-Carthage` |
| **Active Project Nodes** | 5 instances (`felcloud-gw1`, `felcloud-gw2`, `felcloud-bastion`, `team1`, `team2`) |
| **Gateway VM Flavor** | `G0.basic.2c2g` (2 vCPU, 2048 MB RAM, 20 GB Disk) |
| **Sandbox VM Flavor** | `G0.basic.1c1g` (1 vCPU, 1024 MB RAM, 20 GB Disk) |
| **Base VM Image** | `Ubuntu 24.04 LTS - Noble Numbat` (`a049a501-f812-4e6c-9c07-61aa3b08bb50`) |
| **Gateway OS** | Ubuntu 24.04.4 LTS (GNU/Linux 6.8.0-generic x86_64) |
| **HAProxy Version** | HAProxy 2.8.5 |
| **Data Plane API Version** | v2.9.4 deployed binary (note: `dpa_client.go` routes to `/v3/` endpoints, documented in `bugs_found.md`) |
| **Keepalived Version** | Keepalived v2.2.8 (`advert_int 1`, weight `-60`, failover priority formula: 150 - 60 = 90 < 100) |
| **Edge VIP IP (Internal / Public)** | `10.20.20.246` / `$PUBLIC_VIP_IP` |
| **IP Pool CIDR & Size** | `10.20.30.0/24` (231 allocatable addresses: `.10` to `.240`) |
| **Default Lease TTL** | 14,400 s (4 hours) |
| **Default Quarantine Cooldown** | 300 s (configurable to 5 s for testing via `$QUARANTINE_SECONDS`) |
| **Live Routing Status** | VERIFIED: HTTP 200 & HTTPS HTTP/2 TLS routing to `team1` backend |
