import ipaddress
import os
import sqlite3
import subprocess
import sys

DB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ipam.db")
VARS_YML_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ansible", "group_vars", "gateways", "vars.yml")


def get_db_connection():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def init_db():
    conn = get_db_connection()
    cursor = conn.cursor()

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS subnets (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            cidr TEXT NOT NULL,
            gateway_ip TEXT NOT NULL,
            description TEXT
        );
        """
    )

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS ip_allocations (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ip_address TEXT UNIQUE NOT NULL,
            subnet_name TEXT NOT NULL,
            entity_name TEXT NOT NULL,
            entity_type TEXT NOT NULL,
            status TEXT CHECK(status IN ('ALLOCATED', 'RESERVED', 'FREE', 'RELEASED')) DEFAULT 'ALLOCATED',
            assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY(subnet_name) REFERENCES subnets(name)
        );
        """
    )

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS sandboxes (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            sandbox_id TEXT UNIQUE NOT NULL,
            hostname TEXT UNIQUE NOT NULL,
            subnet_name TEXT NOT NULL,
            private_ip TEXT UNIQUE,
            status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'FAILED', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY(subnet_name) REFERENCES subnets(name)
        );
        """
    )

    subnets_data = [
        ("mgmt", "10.20.10.0/24", "10.20.10.1", "Management Subnet"),
        ("edge", "10.20.20.0/24", "10.20.20.1", "Edge Subnet"),
        ("sandbox", "10.20.30.0/24", "10.20.30.1", "Sandbox Subnet"),
    ]
    cursor.executemany(
        """
        INSERT OR IGNORE INTO subnets (name, cidr, gateway_ip, description)
        VALUES (?, ?, ?, ?);
        """,
        subnets_data,
    )

    known_allocations = [
        ("10.20.10.24", "mgmt", "gw1", "GATEWAY"),
        ("10.20.10.164", "mgmt", "gw2", "GATEWAY"),
        ("10.20.10.246", "mgmt", "bastion", "BASTION"),
        ("10.20.20.82", "edge", "gw1", "GATEWAY"),
        ("10.20.20.190", "edge", "gw2", "GATEWAY"),
        ("10.20.20.177", "edge", "bastion", "BASTION"),
        ("10.20.20.246", "edge", "keepalived-vip", "VIP"),
        ("10.20.30.86", "sandbox", "gw1", "GATEWAY"),
        ("10.20.30.224", "sandbox", "gw2", "GATEWAY"),
        ("10.20.30.70", "sandbox", "team1", "TEAM_SANDBOX"),
        ("10.20.30.192", "sandbox", "team2", "TEAM_SANDBOX"),
    ]
    cursor.executemany(
        """
        INSERT OR IGNORE INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status)
        VALUES (?, ?, ?, ?, 'ALLOCATED');
        """,
        known_allocations,
    )

    conn.commit()
    conn.close()
    print("✓ IPAM database initialized successfully with subnet and sandbox lifecycle tables.")


def _find_available_ip(subnet_name="sandbox"):
    conn = get_db_connection()
    cursor = conn.cursor()

    row = cursor.execute(
        "SELECT ip_address FROM ip_allocations WHERE subnet_name = ? AND status = 'FREE' ORDER BY assigned_at LIMIT 1",
        (subnet_name,),
    ).fetchone()
    if row:
        conn.close()
        return row["ip_address"]

    network = ipaddress.ip_network("10.20.30.0/24") if subnet_name == "sandbox" else ipaddress.ip_network("10.20.10.0/24")
    used = {
        item["ip_address"]
        for item in cursor.execute(
            "SELECT ip_address FROM ip_allocations WHERE subnet_name = ?",
            (subnet_name,),
        ).fetchall()
    }
    conn.close()

    for ip in network.hosts():
        ip_str = str(ip)
        if ip_str in used:
            continue
        if ip_str.startswith("10.20.30.1"):
            continue
        return ip_str
    raise ValueError(f"No free IPs available in subnet {subnet_name}.")


def provision_sandbox(sandbox_id, hostname, subnet_name="sandbox", preferred_ip=None):
    conn = get_db_connection()
    cursor = conn.cursor()

    existing = cursor.execute(
        "SELECT * FROM sandboxes WHERE sandbox_id = ? OR hostname = ?",
        (sandbox_id, hostname),
    ).fetchone()
    if existing:
        if existing["status"] == "ACTIVE":
            conn.close()
            return {
                "sandbox_id": existing["sandbox_id"],
                "hostname": existing["hostname"],
                "private_ip": existing["private_ip"],
                "status": existing["status"],
            }
        cursor.execute(
            "UPDATE sandboxes SET status = 'PROVISIONING', updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ?",
            (existing["sandbox_id"],),
        )
        sandbox_id = existing["sandbox_id"]
        hostname = existing["hostname"]
    
    if preferred_ip is None:
        preferred_ip = _find_available_ip(subnet_name)
    else:
        preferred_ip = str(ipaddress.ip_address(preferred_ip))

    existing_ip = cursor.execute(
        "SELECT * FROM ip_allocations WHERE ip_address = ?",
        (preferred_ip,),
    ).fetchone()
    if existing_ip and existing_ip["entity_type"] == "TEAM_SANDBOX":
        raise ValueError(f"IP {preferred_ip} is already allocated to {existing_ip['entity_name']}.")

    cursor.execute(
        """
        INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status)
        VALUES (?, ?, ?, 'TEAM_SANDBOX', 'ALLOCATED')
        ON CONFLICT(ip_address) DO UPDATE SET
            subnet_name = excluded.subnet_name,
            entity_name = excluded.entity_name,
            entity_type = excluded.entity_type,
            status = 'ALLOCATED',
            assigned_at = CURRENT_TIMESTAMP;
        """,
        (preferred_ip, subnet_name, sandbox_id),
    )

    cursor.execute(
        """
        INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
        VALUES (?, ?, ?, ?, 'ACTIVE')
        ON CONFLICT(sandbox_id) DO UPDATE SET
            hostname = excluded.hostname,
            subnet_name = excluded.subnet_name,
            private_ip = excluded.private_ip,
            status = 'ACTIVE',
            updated_at = CURRENT_TIMESTAMP;
        """,
        (sandbox_id, hostname, subnet_name, preferred_ip),
    )

    conn.commit()
    conn.close()

    return {
        "sandbox_id": sandbox_id,
        "hostname": hostname,
        "private_ip": preferred_ip,
        "status": "ACTIVE",
    }


def release_sandbox(sandbox_id):
    conn = get_db_connection()
    cursor = conn.cursor()

    row = cursor.execute(
        "SELECT * FROM sandboxes WHERE sandbox_id = ?",
        (sandbox_id,),
    ).fetchone()
    if not row:
        conn.close()
        return {"status": "NOT_FOUND"}

    if row["private_ip"]:
        cursor.execute(
            "UPDATE ip_allocations SET status = 'FREE' WHERE ip_address = ?",
            (row["private_ip"],),
        )

    cursor.execute(
        "UPDATE sandboxes SET status = 'RELEASED', updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ?",
        (sandbox_id,),
    )

    conn.commit()
    conn.close()
    return {
        "sandbox_id": sandbox_id,
        "private_ip": row["private_ip"],
        "status": "RELEASED",
    }


def list_sandboxes():
    conn = get_db_connection()
    rows = conn.execute(
        """
        SELECT s.sandbox_id, s.hostname, s.subnet_name, s.private_ip, s.status, i.entity_name
        FROM sandboxes s
        LEFT JOIN ip_allocations i ON i.ip_address = s.private_ip
        ORDER BY s.created_at;
        """
    ).fetchall()
    conn.close()
    return [dict(r) for r in rows]


def list_allocations():
    conn = get_db_connection()
    rows = conn.execute("SELECT * FROM ip_allocations ORDER BY subnet_name, ip_address").fetchall()
    subnets = conn.execute("SELECT * FROM subnets").fetchall()
    conn.close()

    print("\n=======================================================")
    print("                IPAM SUBNETS OVERVIEW                  ")
    print("=======================================================")
    for s in subnets:
        print(f" • [{s['name'].upper()}] CIDR: {s['cidr']} | Gateway: {s['gateway_ip']} | {s['description']}")

    print("\n=======================================================")
    print("               IP ADDRESS ALLOCATIONS                 ")
    print("=======================================================")
    print(f"{'IP Address':<18} {'Subnet':<10} {'Entity Name':<16} {'Entity Type':<14} {'Status'}")
    print("-" * 72)
    for a in rows:
        print(f"{a['ip_address']:<18} {a['subnet_name']:<10} {a['entity_name']:<16} {a['entity_type']:<14} {a['status']}")


def add_team(team_name, ip_address=None, port=80):
    try:
        result = provision_sandbox(team_name, f"{team_name}.local", preferred_ip=ip_address)
    except ValueError as exc:
        print(f"✗ Error: {exc}")
        return False

    print(f"✓ Registered '{team_name}' ({result['private_ip']}:{port}) in IPAM database.")
    update_ansible_teams()
    return True


def remove_team(team_name):
    result = release_sandbox(team_name)
    if result["status"] == "NOT_FOUND":
        print(f"✗ Error: Team '{team_name}' not found in IPAM database.")
        return False
    print(f"✓ Removed team '{team_name}' from IPAM database.")
    update_ansible_teams()
    return True


def update_ansible_teams():
    conn = get_db_connection()
    team_allocs = conn.execute(
        "SELECT s.sandbox_id, s.hostname, s.private_ip FROM sandboxes s WHERE s.status IN ('ACTIVE', 'PROVISIONING')"
    ).fetchall()
    conn.close()

    teams_list = []
    for team in team_allocs:
        teams_list.append({
            "name": team["sandbox_id"],
            "ip": team["private_ip"],
            "port": 80,
        })

    vars_content = """---
virtual_router_id: 51
haproxy_dpa_port: 5555
keepalived_script_user: root
keepalived_check_interval: 2
keepalived_check_weight: 2

teams:
"""
    for team in teams_list:
        vars_content += f"  - name: {team['name']}\n"
        vars_content += f"    ip: {team['ip']}\n"
        vars_content += f"    port: {team['port']}\n"

    with open(VARS_YML_PATH, "w") as f:
        f.write(vars_content)

    print(f"✓ Synchronized Ansible variables in {os.path.basename(VARS_YML_PATH)} with IPAM database.")


def sync_haproxy():
    print("\n--> Syncing HAProxy configuration across gateway nodes...")
    cp_dir = os.path.expanduser("~/.ansible/cp")
    if os.path.exists(cp_dir):
        for f in os.listdir(cp_dir):
            try:
                os.remove(os.path.join(cp_dir, f))
            except Exception:
                pass

    cmd = [
        "ansible-playbook",
        "-i", "../ansible/inventory/hosts.yml",
        "../ansible/site.yml",
        "--vault-password-file", "../ansible/.vault_pass",
        "--tags", "haproxy",
        "-e", "ansible_ssh_common_args='-o ProxyJump=ansible@197.5.133.119 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'",
    ]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        print("✓ HAProxy configuration reloaded successfully across GW1 and GW2.")
    else:
        print(f"✗ Failed to reload HAProxy via Ansible:\n{res.stdout}\n{res.stderr}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage:")
        print("  python3 ipam_control.py init")
        print("  python3 ipam_control.py list")
        print("  python3 ipam_control.py provision --name <sandbox_id> --host <hostname>")
        print("  python3 ipam_control.py add-team --name <team> --ip <ip>")
        print("  python3 ipam_control.py remove-team --name <team>")
        print("  python3 ipam_control.py sync")
        sys.exit(1)

    action = sys.argv[1]

    if action == "init":
        init_db()
    elif action == "list":
        if not os.path.exists(DB_PATH):
            init_db()
        list_allocations()
    elif action == "provision":
        if not os.path.exists(DB_PATH):
            init_db()
        team_name = None
        hostname = None
        for i in range(2, len(sys.argv)):
            if sys.argv[i] == "--name" and i + 1 < len(sys.argv):
                team_name = sys.argv[i + 1]
            elif sys.argv[i] == "--host" and i + 1 < len(sys.argv):
                hostname = sys.argv[i + 1]
        if team_name and hostname:
            result = provision_sandbox(team_name, hostname)
            print(result)
        else:
            print("Error: Missing --name or --host arguments.")
    elif action == "add-team":
        if not os.path.exists(DB_PATH):
            init_db()
        team_name = None
        ip_addr = None
        for i in range(2, len(sys.argv)):
            if sys.argv[i] == "--name" and i + 1 < len(sys.argv):
                team_name = sys.argv[i + 1]
            elif sys.argv[i] == "--ip" and i + 1 < len(sys.argv):
                ip_addr = sys.argv[i + 1]

        if team_name:
            if add_team(team_name, ip_addr):
                sync_haproxy()
        else:
            print("Error: Missing --name argument.")
    elif action == "remove-team":
        if not os.path.exists(DB_PATH):
            init_db()
        team_name = None
        for i in range(2, len(sys.argv)):
            if sys.argv[i] == "--name" and i + 1 < len(sys.argv):
                team_name = sys.argv[i + 1]
        if team_name:
            if remove_team(team_name):
                sync_haproxy()
        else:
            print("Error: Missing --name argument.")
    elif action == "sync":
        update_ansible_teams()
    else:
        print(f"Unknown action: {action}")
        sys.exit(1)
