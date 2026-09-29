#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VIP_IP="${VIP_IP:-${TARGET_URL#http://}}"
VIP_IP="${VIP_IP:-${TARGET_URL#https://}}"
GW1_HOST="${GW1_HOST:-gw1}"
GW2_HOST="${GW2_HOST:-gw2}"
TARGET_URL="${TARGET_URL:-http://${VIP_IP}}"
REMOTE_USER="${REMOTE_USER:-root}"
ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-$ROOT_DIR/ansible/inventory/hosts.yml}"
STARTUP_TIMEOUT="${STARTUP_TIMEOUT:-180}"

require_value() {
  local name="$1"
  local value="$2"
  if [[ -z "$value" ]]; then
    echo "Missing required value: $name" >&2
    exit 1
  fi
}

run_remote_cmd() {
  local host="$1"
  local cmd="$2"

  if command -v ansible >/dev/null 2>&1 && [[ -f "$ANSIBLE_INVENTORY" ]]; then
    ansible -i "$ANSIBLE_INVENTORY" "$host" -a "$cmd" -b 2>/dev/null || ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${host}" "$cmd"
    return
  fi

  ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${host}" "$cmd"
}

wait_for_http() {
  local url="$1"
  local deadline=$((SECONDS + STARTUP_TIMEOUT))
  while (( SECONDS < deadline )); do
    if curl -fsS -m 3 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_vip_loss() {
  local url="$1"
  local deadline=$((SECONDS + STARTUP_TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! curl -fsS -m 3 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_vip_reacquire() {
  local url="$1"
  local deadline=$((SECONDS + STARTUP_TIMEOUT))
  while (( SECONDS < deadline )); do
    if curl -fsS -m 3 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

require_value "VIP_IP" "$VIP_IP"
require_value "TARGET_URL" "$TARGET_URL"

echo "Starting failover test against ${VIP_IP}"
echo "Target URL: ${TARGET_URL}"

FAILURE_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if ! run_remote_cmd "$GW1_HOST" 'systemctl stop keepalived'; then
  echo "Failed to stop keepalived on GW1. Check SSH/Ansible access." >&2
  exit 1
fi

if ! wait_for_vip_loss "$TARGET_URL"; then
  echo "VIP remained reachable after stopping keepalived on GW1; failover did not complete in ${STARTUP_TIMEOUT}s." >&2
  exit 1
fi

VIP_LOSS_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if ! wait_for_vip_reacquire "$TARGET_URL"; then
  echo "VIP did not return to service after failover on GW2 within ${STARTUP_TIMEOUT}s." >&2
  exit 1
fi

SUCCESS_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PACKET_LOSS="$(ping -c 5 -q "$VIP_IP" 2>/dev/null | awk -F'%' '/packet loss/ {print $1}' | awk '{print $NF}' || echo 'unknown')"

printf '\nFailover summary\n'
printf 'failure_started_at=%s\n' "$FAILURE_TS"
printf 'vip_lost_at=%s\n' "$VIP_LOSS_TS"
printf 'vip_reacquired_at=%s\n' "$SUCCESS_TS"
printf 'first_successful_request_url=%s\n' "$TARGET_URL"
printf 'packet_loss_percent=%s\n' "$PACKET_LOSS"

echo "Note: if the VIP is still on the old node, check the Keepalived state and resolve the edge-interface voting/priority issue before continuing."
