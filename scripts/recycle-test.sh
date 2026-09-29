#!/usr/bin/env bash
set -Eeuo pipefail

BASE_URL="${RECYCLE_BASE_URL:-${BASE_URL:-http://localhost:8080}}"
COOLDOWN_SECONDS="${RECYCLE_COOLDOWN_SECONDS:-${IP_QUARANTINE_SECONDS:-300}}"
SANDBOX_ID="${SANDBOX_ID:-recycle-test-$(date -u +%Y%m%dT%H%M%SZ)}"
HOSTNAME="${HOSTNAME:-recycle-test.local}"

ALLOCATE_ENDPOINT="${ALLOCATE_ENDPOINT:-$BASE_URL/ipam/leases/allocate}"
RELEASE_ENDPOINT="${RELEASE_ENDPOINT:-$BASE_URL/ipam/leases/release}"
HEALTH_ENDPOINT="${HEALTH_ENDPOINT:-$BASE_URL/health}"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required tool not found: $1" >&2
    exit 1
  fi
}

parse_json_field() {
  local json="$1"
  local field="$2"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$json" | jq -r ".$field"
    return
  fi
  python3 - "$json" "$field" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
field = sys.argv[2]
print(payload.get(field, ""))
PY
}

require_tool curl
require_tool python3

if ! curl -fsS "$HEALTH_ENDPOINT" >/dev/null 2>&1; then
  echo "Control-plane health endpoint is not reachable at $HEALTH_ENDPOINT." >&2
  echo "This script is designed to exercise the future IPAM lifecycle API; make sure the service is running before executing it." >&2
  exit 1
fi

echo "Starting recycle flow for sandbox_id=${SANDBOX_ID}"
ALLOCATE_P1="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" -H 'Content-Type: application/json' -d "{\"sandbox_id\":\"${SANDBOX_ID}\",\"hostname\":\"${HOSTNAME}\"}" )"
FIRST_IP="$(parse_json_field "$ALLOCATE_P1" ip_address)"

if [[ -z "$FIRST_IP" || "$FIRST_IP" == "null" ]]; then
  echo "Allocation did not return an IP address; response was: $ALLOCATE_P1" >&2
  exit 1
fi

echo "Allocated IP: ${FIRST_IP}"

RELEASE_P1="$(curl -fsS -X POST "$RELEASE_ENDPOINT" -H 'Content-Type: application/json' -d "{\"sandbox_id\":\"${SANDBOX_ID}\",\"ip_address\":\"${FIRST_IP}\"}" )"
echo "Release response: ${RELEASE_P1}"

IMMEDIATE_ALLOCATE="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" -H 'Content-Type: application/json' -d "{\"sandbox_id\":\"${SANDBOX_ID}-recheck\",\"hostname\":\"${HOSTNAME}-recheck\"}" )"
IMMEDIATE_IP="$(parse_json_field "$IMMEDIATE_ALLOCATE" ip_address)"

if [[ -n "$IMMEDIATE_IP" && "$IMMEDIATE_IP" == "$FIRST_IP" ]]; then
  echo "ERROR: the same IP was reallocated immediately after release, violating quarantine expectations." >&2
  exit 1
fi

echo "Immediate reallocation check: ${IMMEDIATE_IP:-none} (expected different from ${FIRST_IP})"

echo "Waiting for quarantine cooldown (${COOLDOWN_SECONDS}s) before re-run..."
for _ in $(seq 1 "$COOLDOWN_SECONDS"); do
  sleep 1
  if (( _ % 10 == 0 )); then
    echo "Cooldown elapsed: $(( _ )) / ${COOLDOWN_SECONDS}s"
  fi
done

FINAL_ALLOCATE="$(curl -fsS -X POST "$ALLOCATE_ENDPOINT" -H 'Content-Type: application/json' -d "{\"sandbox_id\":\"${SANDBOX_ID}-after-cooldown\",\"hostname\":\"${HOSTNAME}-after-cooldown\"}" )"
FINAL_IP="$(parse_json_field "$FINAL_ALLOCATE" ip_address)"
if [[ -n "$FINAL_IP" ]]; then
  echo "Post-cooldown allocation succeeded: ${FINAL_IP}"
else
  echo "Post-cooldown allocation did not return an IP address; response was: $FINAL_ALLOCATE" >&2
  exit 1
fi

echo "Recycle test completed successfully."
