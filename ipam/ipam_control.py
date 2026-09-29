import sqlite3
import os
import sys
import json
import subprocess
from datetime import datetime

DB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ipam.db")
VARS_YML_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ansible", "group_vars", "gateways", "vars.yml")

def get_db_connection():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn

def init_db():
    conn = get_db_connection()
    cursor = conn.cursor()

    # 1. Subnets Table
    cursor.execute("""
    CREATE TABLE IF NOT EXISTS subnets (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT UNIQUE NOT NULL,
        cidr TEXT NOT NULL,
        gateway_ip TEXT NOT NULL,
        description TEXT
    );
    """)

    # 2. IP Allocations Table
    cursor.execute("""
    CREATE TABLE IF NOT EXISTS ip_allocations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        ip_address TEXT UNIQUE NOT NULL,
        subnet_name TEXT NOT NULL,
        entity_name TEXT NOT NULL,
        entity_type TEXT NOT NULL, -- GATEWAY, VIP, BASTION, TEAM_SANDBOX
        status TEXT CHECK(status IN ('ALLOCATED', 'RESERVED', 'FREE')) DEFAULT 'ALLOCATED',
        assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY(subnet_name) REFERENCES subnets(name)
    );
    """)

    # Seed Subnets
    subnets_data = [
        ("mgmt", "10.20.10.0/24", "10.20.10.1", "Management Subnet"),
        ("edge", "10.20.20.0/24", "10.20.20.1", "Edge Subnet"),
        ("sandbox", "10.20.30.0/24", "10.20.30.1", "Sandbox Subnet")
    ]
    cursor.executemany("""
    INSERT OR IGNORE INTO subnets (name, cidr, gateway_ip, description)
    VALUES (?, ?, ?, ?);
    """, subnets_data)

    # Seed Known Ground Truth Allocations
    allocations_data = [
        # Management
        ("10.20.10.24", "mgmt", "gw1", "GATEWAY"),
        ("10.20.10.164", "mgmt", "gw2", "GATEWAY"),
        ("10.20.10.246", "mgmt", "bastion", "BASTION"),
        # Edge
        ("10.20.20.82", "edge", "gw1", "GATEWAY"),
        ("10.20.20.190", "edge", "gw2", "GATEWAY"),
        ("10.20.20.177", "edge", "bastion", "BASTION"),
        ("10.20.20.246", "edge", "keepalived-vip", "VIP"),
        # Sandbox
        ("10.20.30.86", "sandbox", "gw1", "GATEWAY"),
        ("10.20.30.224", "sandbox", "gw2", "GATEWAY"),
        ("10.20.30.70", "sandbox", "team1", "TEAM_SANDBOX"),
        ("10.20.30.192", "sandbox", "team2", "TEAM_SANDBOX")
    ]

    cursor.executemany("""
    INSERT OR IGNORE INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type)
    VALUES (?, ?, ?, ?);
    """, allocations_data)

    conn.commit()
    conn.close()
    print("✓ IPAM Database initialized successfully with ground truth subnets and allocations.")

def list_allocations():
    conn = get_db_connection()
    cursor = conn.cursor()

    print("\n=======================================================")
    print("                IPAM SUBNETS OVERVIEW                  ")
    print("=======================================================")
    subnets = cursor.execute("SELECT * FROM subnets").fetchall()
    for s in subnets:
        print(f" • [{s['name'].upper()}] CIDR: {s['cidr']} | Gateway: {s['gateway_ip']} | {s['description']}")

    print("\n=======================================================")
    print("               IP ADDRESS ALLOCATIONS                 ")
    print("=======================================================")
    print(f"{'IP Address':<18} {'Subnet':<10} {'Entity Name':<16} {'Entity Type':<14} {'Status'}")
    print("-" * 72)

    allocs = cursor.execute("SELECT * FROM ip_allocations ORDER BY subnet_name, ip_address").fetchall()
    for a in allocs:
        print(f"{a['ip_address']:<18} {a['subnet_name']:<10} {a['entity_name']:<16} {a['entity_type']:<14} {a['status']}")

    conn.close()

def add_team(team_name, ip_address, port=80):
    conn = get_db_connection()
    cursor = conn.cursor()

    # Check if IP or Team already exists
    existing_ip = cursor.execute("SELECT * FROM ip_allocations WHERE ip_address = ?", (ip_address,)).fetchone()
    if existing_ip:
        print(f"✗ Error: IP address {ip_address} is already allocated to '{existing_ip['entity_name']}'.")
        conn.close()
        return False

    existing_team = cursor.execute("SELECT * FROM ip_allocations WHERE entity_name = ?", (team_name,)).fetchone()
    if existing_team:
        print(f"✗ Error: Team '{team_name}' already exists with IP {existing_team['ip_address']}.")
        conn.close()
        return False

    # Insert into IPAM DB
    cursor.execute("""
    INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status)
    VALUES (?, 'sandbox', ?, 'TEAM_SANDBOX', 'ALLOCATED');
    """, (ip_address, team_name))
    conn.commit()
    conn.close()

    print(f"✓ Registered '{team_name}' ({ip_address}:{port}) in IPAM database.")

    # Update Ansible vars.yml
    update_ansible_teams()
    return True

def remove_team(team_name):
    conn = get_db_connection()
    cursor = conn.cursor()

    existing = cursor.execute("SELECT * FROM ip_allocations WHERE entity_name = ? AND entity_type = 'TEAM_SANDBOX'", (team_name,)).fetchone()
    if not existing:
        print(f"✗ Error: Team '{team_name}' not found in IPAM database.")
        conn.close()
        return False

    cursor.execute("DELETE FROM ip_allocations WHERE entity_name = ?", (team_name,))
    conn.commit()
    conn.close()

    print(f"✓ Removed team '{team_name}' from IPAM database.")
    update_ansible_teams()
    return True

def update_ansible_teams():
    conn = get_db_connection()
    cursor = conn.cursor()
    team_allocs = cursor.execute("SELECT entity_name, ip_address FROM ip_allocations WHERE entity_type = 'TEAM_SANDBOX'").fetchall()
    conn.close()

    teams_list = []
    for t in team_allocs:
        teams_list.append({
            'name': t['entity_name'],
            'ip': t['ip_address'],
            'port': 80
        })

    # Read current vars.yml content up to teams
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
        "-e", "ansible_ssh_common_args='-o ProxyJump=ansible@197.5.133.119 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'"
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
    elif action == "add-team":
        if not os.path.exists(DB_PATH):
            init_db()
        team_name = None
        ip_addr = None
        for i in range(2, len(sys.argv)):
            if sys.argv[i] == "--name" and i+1 < len(sys.argv):
                team_name = sys.argv[i+1]
            elif sys.argv[i] == "--ip" and i+1 < len(sys.argv):
                ip_addr = sys.argv[i+1]

        if team_name and ip_addr:
            if add_team(team_name, ip_addr):
                sync_haproxy()
        else:
            print("Error: Missing --name or --ip arguments.")
    elif action == "remove-team":
        if not os.path.exists(DB_PATH):
            init_db()
        team_name = None
        for i in range(2, len(sys.argv)):
            if sys.argv[i] == "--name" and i+1 < len(sys.argv):
                team_name = sys.argv[i+1]
        if team_name:
            if remove_team(team_name):
                sync_haproxy()
        else:
            print("Error: Missing --name argument.")
    elif action == "sync":
        update_ansible_teams()
        sync_haproxy()
    else:
        print(f"Unknown action: {action}")
