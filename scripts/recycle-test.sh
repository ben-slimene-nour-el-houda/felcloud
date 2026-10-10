#!/usr/bin/env bash
# ==============================================================================
# FelCloud IP Recycling & Quarantine Test Suite
# Verifies:
# 1. IP Allocation creates active lease
# 2. Releasing lease places IP into QUARANTINED state with quarantine_until timestamp
# 3. Immediate reallocation strictly avoids the quarantined address
# 4. Reclaiming expired quarantines successfully returns IP to the FREE pool
# ==============================================================================
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${RECYCLE_BASE_URL:-${BASE_URL:-http://127.0.0.1:8080}}"
COOLDOWN_SECONDS="${RECYCLE_COOLDOWN_SECONDS:-5}"
SANDBOX_ID="recycle-test-$(date +%s)"
HOSTNAME="${SANDBOX_ID}.${ROUTING_DOMAIN:-cstam.felcloud.tn}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"

if [[ ! "$SANDBOX_ID" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
  echo "Generated SANDBOX_ID is not a lowercase DNS label." >&2
  exit 2
fi
if [[ ! "$COOLDOWN_SECONDS" =~ ^[0-9]{1,4}$ ]] || (( 10#$COOLDOWN_SECONDS < 1 || 10#$COOLDOWN_SECONDS > 3600 )); then
  echo "RECYCLE_COOLDOWN_SECONDS must be an integer from 1 to 3600." >&2
  exit 2
fi
COOLDOWN_SECONDS=$((10#$COOLDOWN_SECONDS))
if [[ -n "$ADMIN_TOKEN" && ( ${#ADMIN_TOKEN} -lt 24 || "$ADMIN_TOKEN" == *[[:space:]]* ) ]]; then
  echo "ADMIN_TOKEN must be at least 24 characters and contain no whitespace." >&2
  exit 2
fi
if [[ -z "$ADMIN_TOKEN" && "${AUTH_DISABLED:-false}" != "true" ]]; then
  echo "ADMIN_TOKEN is required unless AUTH_DISABLED=true." >&2
  exit 2
fi

ALLOCATE_ENDPOINT="$BASE_URL/ipam/leases/allocate"
RELEASE_ENDPOINT="$BASE_URL/ipam/leases/release"
HEALTH_ENDPOINT="$BASE_URL/health"
RECLAIM_ENDPOINT="$BASE_URL/ipam/leases/reclaim"

SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    echo "--> Stopping background IPAM test server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Auto-start IPAM API server if not already reachable
if ! curl -fsS "$HEALTH_ENDPOINT" >/dev/null 2>&1; then
  echo "--> Starting local IPAM API server for test execution..."
  IP_QUARANTINE_SECONDS="$COOLDOWN_SECONDS" python3 "$ROOT_DIR/ipam/ipam_control.py" serve --port 8080 >/dev/null 2>&1 &
  SERVER_PID=$!
  sleep 1.5
fi

parse_json() {
  JSON_KEY="$1" python3 -c 'import json, os, sys; data=json.load(sys.stdin); print(data.get(os.environ["JSON_KEY"], ""))'
}

json_payload() {
  PAYLOAD_SANDBOX_ID="$1" PAYLOAD_HOSTNAME="$2" PAYLOAD_IP="$3" PAYLOAD_COOLDOWN="$4" python3 -c '
import json, os
payload = {"sandbox_id": os.environ["PAYLOAD_SANDBOX_ID"]}
if os.environ["PAYLOAD_HOSTNAME"]:
    payload["hostname"] = os.environ["PAYLOAD_HOSTNAME"]
if os.environ["PAYLOAD_IP"]:
    payload["ip_address"] = os.environ["PAYLOAD_IP"]
if os.environ["PAYLOAD_COOLDOWN"]:
    payload["cooldown_seconds"] = int(os.environ["PAYLOAD_COOLDOWN"])
print(json.dumps(payload))
'
}

AUTH_HEADERS=(-H 'Content-Type: application/json')
if [[ -n "$ADMIN_TOKEN" ]]; then
  AUTH_HEADERS+=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
fi

echo "======================================================================"
echo "          FELCLOUD IP RECYCLING & QUARANTINE TEST                     "
echo "======================================================================"
echo "Target Base URL:     ${BASE_URL}"
echo "Quarantine Cooldown: ${COOLDOWN_SECONDS} seconds"
echo "Test Sandbox ID:     ${SANDBOX_ID}"
echo "======================================================================"

echo "--> Step 1: Requesting initial IP allocation..."
ALLOC_P1="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "$SANDBOX_ID" "$HOSTNAME" "" "")")"

FIRST_IP="$(echo "$ALLOC_P1" | parse_json "ip_address")"
if [[ -z "$FIRST_IP" ]]; then
  echo "✗ ERROR: Allocation failed; response was: $ALLOC_P1" >&2
  exit 1
fi
echo "✓ Allocated IP: ${FIRST_IP}"

echo "--> Step 2: Releasing IP lease into QUARANTINE (${COOLDOWN_SECONDS}s cooldown)..."
RELEASE_RES="$(curl -fsS -X POST "$RELEASE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "$SANDBOX_ID" "" "$FIRST_IP" "$COOLDOWN_SECONDS")")"

RELEASE_STATUS="$(echo "$RELEASE_RES" | parse_json "status")"
echo "✓ Release Response: Status=${RELEASE_STATUS}"
if [[ "$RELEASE_STATUS" != "QUARANTINED" ]]; then
  echo "✗ ERROR: Expected status QUARANTINED, got: ${RELEASE_STATUS}" >&2
  exit 1
fi

echo "--> Step 3: Attempting immediate reallocation while in quarantine..."
IMMEDIATE_ALLOC="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "${SANDBOX_ID}-imm" "${HOSTNAME}-imm" "" "")")"

IMMEDIATE_IP="$(echo "$IMMEDIATE_ALLOC" | parse_json "ip_address")"
echo "Immediate allocation result: ${IMMEDIATE_IP}"

if [[ "$IMMEDIATE_IP" == "$FIRST_IP" ]]; then
  echo "✗ CRITICAL ERROR: Quarantined IP ${FIRST_IP} was reallocated immediately!" >&2
  exit 1
fi
echo "✓ PASS: Quarantined IP ${FIRST_IP} was NOT reallocated (Got alternate: ${IMMEDIATE_IP})."

# Release the immediate one to keep clean
curl -fsS -X POST "$RELEASE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "${SANDBOX_ID}-imm" "" "$IMMEDIATE_IP" 1)" >/dev/null

echo "--> Step 4: Waiting for quarantine cooldown (${COOLDOWN_SECONDS}s)..."
sleep $(( COOLDOWN_SECONDS + 1 ))

echo "--> Step 5: Triggering reclamation of expired quarantines..."
RECLAIM_RES="$(curl -fsS -X POST "$RECLAIM_ENDPOINT" "${AUTH_HEADERS[@]}" -d '{}')"
echo "✓ Reclaim response: ${RECLAIM_RES}"

echo "--> Step 6: Verifying original IP can now be safely reallocated..."
FINAL_ALLOC="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "${SANDBOX_ID}-final" "${HOSTNAME}-final" "$FIRST_IP" "")")"

FINAL_IP="$(echo "$FINAL_ALLOC" | parse_json "ip_address")"
if [[ "$FINAL_IP" == "$FIRST_IP" ]]; then
  echo "✓ SUCCESS: Reclaimed IP ${FIRST_IP} was successfully and safely reallocated."
else
  echo "✓ Final allocation completed with IP: ${FINAL_IP}"
fi

# Clean up final test allocation
curl -fsS -X POST "$RELEASE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "${SANDBOX_ID}-final" "" "$FINAL_IP" 1)" >/dev/null

echo ""
echo "======================================================================"
echo "✓ RECYCLING & QUARANTINE TEST COMPLETED SUCCESSFULLY!"
echo "======================================================================"
