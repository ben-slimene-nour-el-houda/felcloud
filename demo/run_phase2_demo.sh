#!/usr/bin/env bash
# ==============================================================================
# FelCloud Phase 2 Demonstration Master Runner
# Runs the 3 required Phase 2 test suites against live OpenStack infrastructure:
#   1. Concurrent Provisioning Stress Test (10+ parallel sandboxes) [15 Pts]
#   2. Automated Teardown & Lifecycle Security (TTL expiry & IP reuse) [10 Pts]
#   3. Network Isolation & Access Safeguards (Neutron SG & eBPF policy) [5 Pts]
# ==============================================================================
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR/.."

# ANSI Colors
CYAN='\033[1;36m'
GREEN='\033[1;32m'
YELLOW='\033[1;33m'
RED='\033[1;31m'
BOLD='\033[1m'
NC='\033[0m'

banner() {
  printf "\n%b================================================================================%b\n" "$CYAN" "$NC"
  printf "%b  %s%b\n" "$BOLD" "$1" "$NC"
  printf "%b================================================================================%b\n\n" "$CYAN" "$NC"
}

info() { printf "%b[*]%b %s\n" "$CYAN" "$NC" "$*"; }
pass() { printf "%b[PASS]%b %s\n" "$GREEN" "$NC" "$*"; }
fail() { printf "%b[FAIL]%b %s\n" "$RED" "$NC" "$*"; }

show_help() {
  cat <<EOF
Usage: $0 [all|1|2|3|cleanup|status]

Phase 2 Video Demo Commands:
  1, concurrent   Run Part 1: Concurrent Provisioning Stress Test (10 sandboxes) [15 Pts]
  2, teardown     Run Part 2: Automated Teardown & Lifecycle Security [10 Pts]
  3, isolation    Run Part 3: Network Isolation & Access Safeguards [5 Pts]
  all             Run all three demonstrations sequentially (ideal for continuous recording)
  cleanup         Clean up demo sandboxes and release leases
  status          Show current Nova instances, IPAM leases, and Gateway health
EOF
}

cmd_status() {
  banner "FelCloud Current Infrastructure Status"
  info "OpenStack Instances:"
  openstack server list --no-name-lookup -c Name -c Status -c Networks || true
  printf "\n"
  info "Control Plane /api/health:"
  curl -s http://localhost:8081/health | jq . || true
  printf "\n"
  info "Active Sandboxes in Control Plane:"
  curl -s -H "Authorization: Bearer felcloud-test-token-cstam-2026" http://localhost:8081/api/sandboxes | jq -c '.sandboxes[] | {id: .sandbox_id, ip: .private_ip, status: .status}' || true
}

cmd_part1() {
  banner "Part 1: Concurrent Provisioning Stress Test [15 Points]"
  info "Objective: Rapid, error-free sandbox provisioning, IP allocation, and routing updates for 10+ teams"
  t0=$(date +%s)
  bash "$SCRIPT_DIR/02a_phase2_concurrent.sh"
  t1=$(date +%s)
  pass "Part 1 completed successfully in $((t1 - t0))s"
}

cmd_part2() {
  banner "Part 2: Automated Teardown & Lifecycle Security [10 Points]"
  info "Objective: Automated cleanup pipeline purging expired sandboxes, proxy/DNS rules, sanitizing IP allocations"
  t0=$(date +%s)
  bash "$SCRIPT_DIR/02b_phase2_teardown.sh"
  t1=$(date +%s)
  pass "Part 2 completed successfully in $((t1 - t0))s"
}

cmd_part3() {
  banner "Part 3: Network Isolation & Access Safeguards [5 Points]"
  info "Objective: Neutron Security Groups blocking inter-sandbox and mgmt traffic; gateway proxy reachable"
  t0=$(date +%s)
  bash "$SCRIPT_DIR/02c_phase2_isolation.sh"
  t1=$(date +%s)
  pass "Part 3 completed successfully in $((t1 - t0))s"
}

cmd_cleanup() {
  banner "Cleaning up demo resources"
  PREFIX=live N=10 bash "$SCRIPT_DIR/02a_phase2_concurrent.sh" cleanup || true
  PREFIX=demo N=10 bash "$SCRIPT_DIR/02a_phase2_concurrent.sh" cleanup || true
  pass "Cleanup requests submitted."
}

ACTION=${1:-all}
case "$ACTION" in
  1|concurrent) cmd_part1 ;;
  2|teardown)   cmd_part2 ;;
  3|isolation)  cmd_part3 ;;
  status)       cmd_status ;;
  cleanup)      cmd_cleanup ;;
  all)
    start_total=$(date +%s)
    cmd_part1
    cmd_part2
    cmd_part3
    end_total=$(date +%s)
    banner "PHASE 2 DEMONSTRATION COMPLETE (Total duration: $((end_total - start_total))s)"
    ;;
  -h|--help|help) show_help ;;
  *) show_help; exit 1 ;;
esac
