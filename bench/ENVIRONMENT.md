# Benchmark environment and repository discovery

**Discovery date (client context):** 2026-10-17
**Git commit:** `18adb7518a250236b742dabe5879201e791ee7c9`
**Branch:** `Dev`

This file distinguishes implementation facts found in the repository from values observed on the live cloud. No provisioning, teardown, gateway manipulation, or config reload was performed during discovery.

## Repository findings

### Backend, API, and authentication

- Backend: Go HTTP control plane in `controlplane/`; Gophercloud v2 is used for OpenStack. State/IPAM is SQLite. The SQLite connection enables WAL, a 5-second busy timeout, and immediate transaction locking.
- API server uses port 8081 in repository documentation. Live API health is **not observed**: `http://127.0.0.1:8081/health` refused connection during discovery.
- Authentication: `Authorization: Bearer <ADMIN_TOKEN>`; the token must be at least 24 characters. Authentication can only be disabled explicitly with `AUTH_DISABLED=true`. `GET /health` and selected public metadata/UI GET routes are open. No credential values are recorded here.
- Routes registered in `controlplane/server.go` include:
  - `GET /health`, `GET /api/config`
  - `GET /api/gateways/status`, `/api/gateways/dpa`, `/api/gateways/events`, `/api/gateways/generations`, `/api/gateways/logs`
  - `POST /api/gateways/failover`, `/api/gateways/sync`, `/api/gateways/rollback`
  - `GET /api/sandboxes`, `POST /api/sandboxes/provision`, `/api/sandboxes/extend`, `/api/sandboxes/custom-domain`, `DELETE /api/sandboxes/{id}`
  - `GET /api/leases` and `/ipam/leases`; POST allocate/release/reclaim routes under both prefixes
  - `GET /api/reconcile`, `POST /api/reconcile/apply`

### Gateways and configuration deployment

- Repository implementation: HAProxy is the L7 proxy; Keepalived provides VRRP active/standby. Ansible templates live under `ansible/roles/haproxy/` and `ansible/roles/keepalived/`.
- HAProxy config path in Ansible templates/operations: `/etc/haproxy/haproxy.cfg`. The Data Plane API config is `/etc/haproxy/dataplaneapi.yaml`; DPA listens on port 5555.
- Config sync uses HAProxy Data Plane API transactions when configured, with an Ansible transactional reload fallback. Candidate config is validated with `haproxy -c`; generation records store config and hashes. Rollback reapplies the previous committed generation. See `controlplane/dpa_client.go`, `controlplane/gateway_deploy.go`, and `ansible/haproxy-transaction.yml`.
- Local installed binaries observed: HAProxy `2.8.16-0ubuntu0.24.04.3`; Keepalived `2.2.8`. These are local tool versions, **not confirmed gateway-host versions**.
- Gateway addresses and intended priority/role are configured in repository files (gw1 mgmt `10.20.10.24`, edge `10.20.20.82`; gw2 mgmt `10.20.10.164`, edge `10.20.20.190`). Live roles, VM sizes, and gateway memory are **not observed**.

### IPAM, pool, and leases

- IPAM data is stored in SQLite tables including `subnets`, `ip_allocations`, `sandboxes`, and lease metadata. IP allocation uses database transactions; released IPs enter `QUARANTINED` with a cooldown before reclamation to free capacity.
- Repository defaults: lease TTL 14,400 seconds (4 hours), maximum 86,400 seconds (24 hours), and quarantine configured by `IP_QUARANTINE_SECONDS` (documented default 300 seconds). Reaper interval is 10 seconds in code. Environment overrides and live DB state were not observed.
- Repository network design describes sandbox CIDR `10.20.30.0/24`; no live IPAM pool CIDR, configured start/end, or usable pool size could be confirmed during discovery.
- Provisioning calls an allocator, creates Nova/Neutron resources using configured network/subnet/security group/image/flavor/keypair, waits for server ACTIVE, probes backend health, then synchronizes gateway routes. Teardown removes routing/resources and quarantines the address.

### Domain, DNS, and routing

- Provisioning derives a sandbox hostname from the configured routing domain; the intended form in repository documentation is `<team>.cstam.felcloud.tn`.
- HAProxy routes Host headers to per-sandbox backends. Documentation describes a wildcard DNS record; `scripts/designate-dns-sync.sh` manages Designate wildcard DNS. Per-sandbox DNS changes/live record state were not confirmed. The benchmark must verify whether the deployment uses wildcard DNS or creates/removes individual records before claiming DNS stage timings.

### Isolation

- Infrastructure code defines separate management, edge, and sandbox networks and Neutron security groups. Repository docs describe `sg-sandbox` allowing service ports from the gateway security group and blocking east-west sandbox access; gateway and management groups cover ingress, SSH, DPA, and VRRP.
- These are code/config design findings only. Live security-group rules and any host iptables/eBPF rules were not queried in discovery.

### OpenStack and SSH access

- Control plane supports OpenStack credentials from standard `OS_*` variables or a named cloud in `clouds.yaml` (`OS_CLOUD`). Local `openstack` and `ansible` commands exist; `~/.config/openstack/clouds.yaml` is present. The active cloud selection and working auth were not established.
- Read-only OpenStack discovery (`token issue`, `server list`, `flavor list`, `image list` using cloud `felcloud`) failed with `Operation not permitted` in this environment. Therefore OpenStack release, live node/server count, sandbox image/flavor, gateway vCPU/RAM, and gateway package versions are **not observed**.
- SSH client exists. Gateway SSH is configured through Ansible inventory and a bastion/jump host in repository configuration; successful SSH access and host identity were not tested.

## Auto-collected local environment snapshot

| Property | Observed value |
|---|---|
| Discovery date | 2026-10-17 (client date context) |
| Git commit | `18adb7518a250236b742dabe5879201e791ee7c9` |
| Host tools | Python 3.12.3; Go 1.27.1 linux/amd64 |
| Local HAProxy | 2.8.16-0ubuntu0.24.04.3 (not gateway-confirmed) |
| Local Keepalived | 2.2.8 (not gateway-confirmed) |
| OpenStack release | NOT OBSERVED (cloud inventory access denied) |
| OpenStack nodes / active VMs | NOT OBSERVED |
| Gateway vCPU / RAM | NOT OBSERVED |
| Sandbox image / flavor | NOT OBSERVED |
| Gateway HAProxy / Keepalived versions | NOT OBSERVED |
| IPAM CIDR and usable pool size | NOT OBSERVED live; repository docs describe `10.20.30.0/24` |
| Effective lease TTL / quarantine | NOT OBSERVED live; code defaults are 14,400 s / 300 s |
| Control plane API availability | NOT RUNNING at `127.0.0.1:8081` during discovery |

## Information needed before live benchmarks

No additional values should be guessed. To run cloud-backed benchmarks, the runtime needs working OpenStack auth and the control plane must be running with its valid admin token and provisioner configuration. Gateway SSH through the configured bastion must also work. The current environment has a clouds.yaml file but cloud access was denied, and no API listener was reachable. Once those are available, the benchmark can collect live cloud and gateway facts using read-only calls before any test mutations.
