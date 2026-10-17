"""
bench.lib.cleanup — Global and test-scoped cleanup utilities.
Strict Rule: Only cleans resources prefixed with 'bench-'.
Never touches team1, team2, bastion, or gateways.
"""

import time
from typing import Dict, Any, List
from bench.lib.api_client import ControlPlaneClient
from bench.lib.openstack_client import OpenStackClient, RESOURCE_PREFIX

PROTECTED_NAMES = {"team1", "team2", "felcloud-bastion", "felcloud-gw1", "felcloud-gw2"}

def cleanup_all_benchmark_resources(api_client: ControlPlaneClient, os_client: OpenStackClient, verbose: bool = True) -> Dict[str, Any]:
    """
    Purges all test resources created by the benchmark suite.
    Enforces that only resources with prefix 'bench-' are ever targeted.
    """
    cleaned: Dict[str, List[str]] = {
        "sandboxes": [],
        "openstack_servers": [],
        "openstack_ports": [],
        "reclaimed_ips": []
    }

    if verbose:
        print(f"--> [CLEANUP] Initiating purge for resources matching prefix '{RESOURCE_PREFIX}'...")

    # 1. Clean via Control Plane API
    status_code, data = api_client.list_sandboxes()
    if status_code == 200:
        sandboxes = data.get("sandboxes", [])
        for s in sandboxes:
            name = s.get("team", s.get("name", s.get("sandbox_id", "")))
            if name.startswith(RESOURCE_PREFIX) and name not in PROTECTED_NAMES:
                if verbose:
                    print(f"    Deleting benchmark sandbox via API: {name}")
                api_client.delete_sandbox(name)
                cleaned["sandboxes"].append(name)

    # 2. Check for orphan OpenStack VMs
    bench_servers = os_client.list_benchmark_servers()
    for srv in bench_servers:
        s_id = srv.get("ID")
        s_name = srv.get("Name", "")
        if s_name.startswith(RESOURCE_PREFIX) and s_name not in PROTECTED_NAMES:
            if verbose:
                print(f"    Deleting orphan benchmark VM: {s_name} ({s_id})")
            os_client.delete_server_safe(s_id, s_name)
            cleaned["openstack_servers"].append(s_name)

    # 3. Check for orphan OpenStack Ports
    bench_ports = os_client.list_benchmark_ports()
    for p in bench_ports:
        p_id = p.get("ID")
        p_name = p.get("Name", "")
        if p_name.startswith(RESOURCE_PREFIX) and p_name not in PROTECTED_NAMES:
            if verbose:
                print(f"    Deleting orphan benchmark port: {p_name} ({p_id})")
            os_client.delete_port_safe(p_id, p_name)
            cleaned["openstack_ports"].append(p_name)

    # 4. Trigger reconciliation & reclamation
    api_client.reclaim_leases()

    if verbose:
        print(f"✓ [CLEANUP] Completed. Purged: {len(cleaned['sandboxes'])} sandboxes, "
              f"{len(cleaned['openstack_servers'])} VMs, {len(cleaned['openstack_ports'])} ports.")

    return cleaned
