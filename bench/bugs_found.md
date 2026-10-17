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
