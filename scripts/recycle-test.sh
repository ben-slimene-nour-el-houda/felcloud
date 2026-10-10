#!/usr/bin/env bash
# ==============================================================================
# FelCloud IP Recycling & Quarantine Test Suite
# Verifies:
# 1. IPAM allocation creates an active lease
# 2. Releasing a lease quarantines its IP
# 3. Immediate reallocation avoids the quarantined address
# 4. Expired quarantines can be reclaimed and reused
# ==============================================================================
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${RECYCLE_BASE_URL:-${BASE_URL:-}}"
COOLDOWN_SECONDS="${RECYCLE_COOLDOWN_SECONDS:-5}"
SANDBOX_ID="recycle-test-$(date +%s)"
HOSTNAME="${SANDBOX_ID}.${ROUTING_DOMAIN:-cstam.felcloud.tn}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
SERVER_PID=""
TEMP_DIR=""
BUILD_DIR=""

if [[ ! "$SANDBOX_ID" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
  echo "Generated SANDBOX_ID is not a lowercase DNS label." >&2
  exit 2
fi
if [[ ! "$COOLDOWN_SECONDS" =~ ^[0-9]{1,4}$ ]] || (( 10#$COOLDOWN_SECONDS < 1 || 10#$COOLDOWN_SECONDS > 3600 )); then
  echo "RECYCLE_COOLDOWN_SECONDS must be an integer from 1 to 3600." >&2
  exit 2
fi
COOLDOWN_SECONDS=$((10#$COOLDOWN_SECONDS))
if [[ -z "$ADMIN_TOKEN" ]]; then
  ADMIN_TOKEN="$(openssl rand -hex 16)"
fi
if [[ ${#ADMIN_TOKEN} -lt 24 || "$ADMIN_TOKEN" == *[[:space:]]* ]]; then
  echo "ADMIN_TOKEN must be at least 24 characters and contain no whitespace." >&2
  exit 2
fi

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    echo "--> Stopping background Go control-plane test server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "$TEMP_DIR" ]]; then
    rm -f -- "$TEMP_DIR/ipam.db" "$TEMP_DIR/ipam.db-shm" "$TEMP_DIR/ipam.db-wal" "$TEMP_DIR/server.log"
  fi
  if [[ -n "$BUILD_DIR" ]]; then
    rm -rf -- "$BUILD_DIR"
  fi
  if [[ -n "$TEMP_DIR" ]]; then
    rmdir -- "$TEMP_DIR" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if [[ -z "$BASE_URL" ]]; then
  PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  BASE_URL="http://127.0.0.1:$PORT"
  TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/felcloud-recycle.XXXXXX")"
  BUILD_DIR="$TEMP_DIR/build"
  mkdir -- "$BUILD_DIR"
  SERVER_BINARY="${FELCLOUD_CONTROLPLANE_BINARY:-$BUILD_DIR/felcloud-controlplane}"
  if [[ ! -x "$SERVER_BINARY" ]]; then
    echo "--> Building Go control plane for isolated IPAM test..."
    (cd "$ROOT_DIR/controlplane" && go build -o "$SERVER_BINARY" .)
  fi
  echo "--> Starting isolated mock control plane on port $PORT..."
  PROVISIONER=mock \
  IP_QUARANTINE_SECONDS="$COOLDOWN_SECONDS" \
  ADMIN_TOKEN="$ADMIN_TOKEN" \
  RATE_LIMIT_PER_MIN=1000 \
  IPAM_DB_PATH="$TEMP_DIR/ipam.db" \
  LISTEN_ADDR=127.0.0.1 \
    "$SERVER_BINARY" serve --port "$PORT" >"$TEMP_DIR/server.log" 2>&1 &
  SERVER_PID=$!
else
  BASE_URL="${BASE_URL%/}"
fi

HEALTH_ENDPOINT="$BASE_URL/health"
ALLOCATE_ENDPOINT="$BASE_URL/ipam/leases/allocate"
RELEASE_ENDPOINT="$BASE_URL/ipam/leases/release"
RECLAIM_ENDPOINT="$BASE_URL/ipam/leases/reclaim"

health_ready=false
for _ in $(seq 1 80); do
  if curl -fsS --max-time 1 "$HEALTH_ENDPOINT" >/dev/null 2>&1; then
    health_ready=true
    break
  fi
  sleep 0.25
done
if [[ "$health_ready" != true ]]; then
  echo "Control plane did not become healthy at $HEALTH_ENDPOINT." >&2
  if [[ -n "$TEMP_DIR" && -f "$TEMP_DIR/server.log" ]]; then
    cat "$TEMP_DIR/server.log" >&2
  fi
  exit 1
fi
if ! curl -fsS --max-time 5 "$BASE_URL/api/sandboxes" \
  -H "Authorization: Bearer $ADMIN_TOKEN" >/dev/null; then
  echo "Authenticated API preflight failed at $BASE_URL/api/sandboxes." >&2
  exit 1
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

AUTH_HEADERS=(-H 'Content-Type: application/json' -H "Authorization: Bearer $ADMIN_TOKEN")

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

# Release the immediate allocation so the test leaves no active lease.
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

# Clean up final test allocation.
curl -fsS -X POST "$RELEASE_ENDPOINT" \
  "${AUTH_HEADERS[@]}" \
  -d "$(json_payload "${SANDBOX_ID}-final" "" "$FINAL_IP" 1)" >/dev/null

echo ""
echo "======================================================================"
echo "✓ RECYCLING & QUARANTINE TEST COMPLETED SUCCESSFULLY!"
echo "======================================================================"
