# Bugs & Anomalies Found During Benchmarking

This log records bugs, architectural mismatches, and anomalies discovered during the execution of the CSTAM 3.0 benchmark suite.
In adherence to Absolute Rule 2: **No application logic has been modified to artificially mask these issues.**

---

## BUG-001: HAProxy Data Plane API Version Inconsistency (/v3 Endpoints on v2.9.4 Binary)

- **Component:** `controlplane/dpa_client.go` vs `ansible/roles/haproxy/tasks/main.yml`
- **Severity:** High (impacting direct DPA transaction execution)
- **Description:**
  - Ansible installs `dataplaneapi_2.9.4_linux_x86_64.tar.gz` on gateway nodes `gw1` and `gw2`.
  - The Go control plane client `dpa_client.go` hardcodes base URLs to `/v3` (e.g. `http://<ip>:5555/v3/services/haproxy/transactions` and `http://<ip>:5555/v3/info`).
  - HAProxy Data Plane API v2.9.4 exclusively serves the `/v2/` API specification (`/v2/services/haproxy/transactions` and `/v2/info`). The `/v3/` API namespace was introduced in Data Plane API v3.0.
  - Queries against `/v3/` return `404 Not Found`.
- **Reproduction Steps:**
  ```bash
  # Inside management network or via bastion:
  curl -s -u admin:<DPA_PASS> http://10.20.10.24:5555/v3/info
  # Returns: 404 page not found
  curl -s -u admin:<DPA_PASS> http://10.20.10.24:5555/v2/info
  # Returns: HTTP 200 with HAProxy info payload
  ```
- **Recommended Fix:**
  Upgrade the gateway Data Plane API binary to `v3.x` in Ansible, or update `dpa_client.go` base URL to `/v2` to match the installed v2.9.4 release.

---

## BUG-002: Missing IPQoS=none in SSH Health Probe Causes Jump Host Timeout

- **Component:** `controlplane/health_probe.go` (`sshHealthProbeAttempt`)
- **Severity:** High (blocks gateway-ssh health probing of sandboxes)
- **Description:**
  - While `ansible/inventory/hosts.yml` and `controlplane/gateway_probe.go` explicitly specify `-o IPQoS=none` to avoid TCP packet drops/blackholing during SSH banner exchange over carrier networks, `controlplane/health_probe.go` omits `-o IPQoS=none` in its `ssh` command construction (`args := []string{"-i", cfg.Key, "-J", cfg.Jump, ...}`).
  - As a result, when `HEALTH_PROBE_MODE="gateway-ssh"` is active, `ssh` hangs or times out during banner exchange with the jump host.
- **Reproduction Steps:**
  ```bash
  # Attempt SSH probe without IPQoS=none:
  ssh -J ansible@$BASTION_IP -o BatchMode=yes -o ConnectTimeout=3 ansible@10.20.10.24 "uptime"
  # Times out during banner exchange
  # With IPQoS=none:
  ssh -o IPQoS=none -J ansible@$BASTION_IP -o BatchMode=yes -o ConnectTimeout=3 ansible@10.20.10.24 "uptime"
  # Connects cleanly
  ```
- **Recommended Fix:**
  Add `-o IPQoS=none` to `args` in `sshHealthProbeAttempt` in `controlplane/health_probe.go`.

---

## BUG-003: Bastion Public Floating IP Unreachable from External Internet (Port 22 Timeout)

- **Component:** OpenStack Infrastructure / `felcloud-bastion` edge port
- **Severity:** Medium (workaround available via Gateway VIP)
- **Description:**
  - The dedicated bastion VM `felcloud-bastion` has public floating IP `197.5.133.77` mapped to fixed IP `10.20.20.177`.
  - Attempts to establish TCP port 22 connections to `197.5.133.77` time out (`Operation now in progress`), even though Neutron Security Group `sg-management` has an ingress rule allowing TCP port 22 from `0.0.0.0/0`.
  - In contrast, the Keepalived VRRP VIP floating IP `197.5.133.119` is fully reachable on port 22 and proxies cleanly to `felcloud-gw1`.
- **Reproduction Steps:**
  ```bash
  nc -zv -w 3 197.5.133.77 22
  # Times out
  nc -zv -w 3 197.5.133.119 22
  # Succeeded
  ```
- **Workaround:**
  Set `BASTION_IP=197.5.133.119` so that Ansible and SSH jump commands traverse the reachable VIP edge interface.

---

## BUG-004: Control Plane Failover VIP Probe Defaults to Internal IP Without Route

- **Component:** `controlplane/gateway_failover.go` (`failoverProbeTarget`)
- **Severity:** Medium (causes teardown and failover health checks to fail in external setups)
- **Description:**
  - `failoverProbeTarget()` defaults to `http://<VIP_ADDRESS>/` where `VIP_ADDRESS` defaults to `10.20.20.246`.
  - When the control plane runs outside the OpenStack private subnet, dialing `10.20.20.246` fails with context deadline exceeded.
  - Furthermore, if configured with a domain (e.g. `team1.cstam.felcloud.tn`), DNS lookup fails because `.felcloud.tn` is an internal challenge domain not published in public DNS.
- **Workaround:**
  Set `FAILOVER_PROBE_URL="http://197.5.133.119/"` and `FAILOVER_PROBE_HOST="team1.cstam.felcloud.tn"` in the control plane environment.

