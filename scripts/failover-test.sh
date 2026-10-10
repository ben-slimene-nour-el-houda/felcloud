#!/usr/bin/env bash
# Measures an actual VIP failover by stopping Keepalived on its live holder.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATEWAY_API_BASE_URL="${GATEWAY_API_BASE_URL:-${CONTROL_PLANE_BASE_URL:-}}"
CONTROL_PLANE_TOKEN="${CONTROL_PLANE_TOKEN:-}"
EVENTS_TOKEN="${EVENTS_TOKEN:-}"
VIP_IP="${VIP_IP:-${VIP_ADDRESS:-10.20.20.246}}"
TARGET_URL="${FAILOVER_PROBE_URL:-${TARGET_URL:-http://${VIP_IP}/}}"
TEST_HOST_HEADER="${FAILOVER_PROBE_HOST:-${TEST_HOST_HEADER:-team1.${ROUTING_DOMAIN:-cstam.felcloud.tn}}}"
ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-$ROOT_DIR/ansible/inventory/hosts.yml}"
FAILOVER_THRESHOLD_MS="${FAILOVER_THRESHOLD_MS:-3000}"
POLL_INTERVAL_MS="${POLL_INTERVAL_MS:-75}"
REMOTE_USER="${REMOTE_USER:-ansible}"

FAILOVER_FROM="unknown"
FAILOVER_TO="unknown"
FAILOVER_RESULT="failed"
FAILOVER_REASON=""
DOWNTIME_MS=0
STOPPED_GATEWAY=""
PROBE_HEADERS=""
if [[ ! "$POLL_INTERVAL_MS" =~ ^[0-9]{1,4}$ ]] || (( 10#$POLL_INTERVAL_MS < 1 || 10#$POLL_INTERVAL_MS > 5000 )); then
  echo "POLL_INTERVAL_MS must be an integer from 1 to 5000." >&2
  exit 2
fi
POLL_INTERVAL_MS=$((10#$POLL_INTERVAL_MS))
if [[ ! "$REMOTE_USER" =~ ^[a-z_][a-zA-Z0-9_.-]*$ ]]; then
  echo "REMOTE_USER contains invalid characters." >&2
  exit 2
fi
if [[ ! "$TEST_HOST_HEADER" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*(:[0-9]{1,5})?$ ]]; then
  echo "TEST_HOST_HEADER must be a lowercase DNS name with an optional port." >&2
  exit 2
fi

now_ms() {
  python3 -c 'import time; print(int(time.time() * 1000))'
}

api_curl() {
  local url="$1"
  if [[ -n "$CONTROL_PLANE_TOKEN" ]]; then
    curl -fsS --connect-timeout 2 --max-time 8 -H "Authorization: Bearer ${CONTROL_PLANE_TOKEN}" "$url"
  else
    curl -fsS --connect-timeout 2 --max-time 8 "$url"
  fi
}

get_vip_holder() {
  local body
  body="$(api_curl "${GATEWAY_API_BASE_URL%/}/api/gateways/status?refresh=true")" || return 1
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("vip_holder", "unknown"))' <<<"$body"
}

run_remote_keepalived() {
  local gateway="$1" action="$2"
  if command -v ansible >/dev/null 2>&1 && [[ -f "$ANSIBLE_INVENTORY" ]]; then
    ansible -i "$ANSIBLE_INVENTORY" "$gateway" -m command \
      -a "systemctl ${action} keepalived" -b \
      --vault-password-file "$ROOT_DIR/ansible/.vault_pass"
  else
    ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${gateway}" \
      "sudo systemctl ${action} keepalived"
  fi
}

probe_vip() {
  local status gateway
  : >"$PROBE_HEADERS"
  status="$(curl -sS --connect-timeout 1 --max-time 1 -D "$PROBE_HEADERS" -o /dev/null \
    -w '%{http_code}' -H "Host: ${TEST_HOST_HEADER}" "$TARGET_URL" 2>/dev/null || true)"
  gateway="$(awk 'tolower($1) == "x-gateway-id:" {gsub("\r", "", $2); value=$2} END {print value}' "$PROBE_HEADERS")"
  printf '%s\t%s\n' "${status:-000}" "${gateway:-unknown}"
}

post_failover_event() {
  [[ -n "$GATEWAY_API_BASE_URL" ]] || return 0
  local payload
  payload="$(python3 - "$FAILOVER_FROM" "$FAILOVER_TO" "$DOWNTIME_MS" "$FAILOVER_RESULT" "$FAILOVER_REASON" <<'PY'
import json, sys
source, target, duration, result, reason = sys.argv[1:]
detail = f"from={source} to={target} result={result}"
if reason:
    detail += f" reason={reason}"
print(json.dumps({"type": "failover", "duration_ms": int(duration), "detail": detail}))
PY
)"
  if [[ -n "$CONTROL_PLANE_TOKEN" ]]; then
    curl -fsS --connect-timeout 2 --max-time 5 -X POST \
      -H "Authorization: Bearer ${CONTROL_PLANE_TOKEN}" -H 'Content-Type: application/json' \
      --data "$payload" "${GATEWAY_API_BASE_URL%/}/api/gateways/events" >/dev/null || \
      echo "WARN: could not post failover timing event" >&2
  elif [[ -n "$EVENTS_TOKEN" ]]; then
    curl -fsS --connect-timeout 2 --max-time 5 -X POST \
      -H "X-Events-Token: ${EVENTS_TOKEN}" -H 'Content-Type: application/json' \
      --data "$payload" "${GATEWAY_API_BASE_URL%/}/api/gateways/events" >/dev/null || \
      echo "WARN: could not post failover timing event" >&2
  else
    curl -fsS --connect-timeout 2 --max-time 5 -X POST -H 'Content-Type: application/json' \
      --data "$payload" "${GATEWAY_API_BASE_URL%/}/api/gateways/events" >/dev/null || \
      echo "WARN: could not post failover timing event" >&2
  fi
}

finish() {
  local exit_code=$?
  trap - EXIT
  set +e
  if [[ -n "$STOPPED_GATEWAY" ]]; then
    echo "Restoring keepalived on ${STOPPED_GATEWAY}..."
    if ! run_remote_keepalived "$STOPPED_GATEWAY" start; then
      echo "FAIL: could not restart keepalived on ${STOPPED_GATEWAY}" >&2
      FAILOVER_RESULT="failed"
      FAILOVER_REASON="${FAILOVER_REASON:+${FAILOVER_REASON}; }keepalived restart failed"
      exit_code=1
    fi
  fi
  post_failover_event
  [[ -z "$PROBE_HEADERS" ]] || rm -f "$PROBE_HEADERS"
  exit "$exit_code"
}
trap finish EXIT

if [[ -z "$GATEWAY_API_BASE_URL" ]]; then
  FAILOVER_REASON="GATEWAY_API_BASE_URL is required to read live gateway status"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi
if ! [[ "$FAILOVER_THRESHOLD_MS" =~ ^[1-9][0-9]*$ ]] || (( FAILOVER_THRESHOLD_MS > 60000 )); then
  FAILOVER_REASON="FAILOVER_THRESHOLD_MS must be 1..60000"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi
if ! [[ "$POLL_INTERVAL_MS" =~ ^[1-9][0-9]*$ ]]; then
  FAILOVER_REASON="POLL_INTERVAL_MS must be a positive integer"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi
PROBE_HEADERS="$(mktemp)"

echo "Reading live VIP holder from ${GATEWAY_API_BASE_URL%/}/api/gateways/status..."
if ! FAILOVER_FROM="$(get_vip_holder)"; then
  FAILOVER_REASON="could not read live gateway status"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi
FAILOVER_TO="$FAILOVER_FROM"
if [[ "$FAILOVER_FROM" != gw1 && "$FAILOVER_FROM" != gw2 ]]; then
  FAILOVER_REASON="status API reported unknown VIP holder: ${FAILOVER_FROM}"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi

baseline="$(probe_vip)"
baseline_status="${baseline%%$'\t'*}"
baseline_gateway="${baseline#*$'\t'}"
if (( baseline_status < 200 || baseline_status >= 400 )) || [[ "$baseline_gateway" != "$FAILOVER_FROM" ]]; then
  FAILOVER_REASON="baseline VIP probe was unhealthy or disagreed with status API (HTTP ${baseline_status}, header ${baseline_gateway})"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi

STOPPED_GATEWAY="$FAILOVER_FROM"
START_MS="$(now_ms)"
echo "Stopping keepalived on live VIP holder ${STOPPED_GATEWAY}..."
if ! run_remote_keepalived "$STOPPED_GATEWAY" stop; then
  FAILOVER_REASON="failed to stop keepalived on ${STOPPED_GATEWAY}"
  echo "FAIL: ${FAILOVER_REASON}" >&2
  exit 1
fi

OUTAGE_START_MS=0
RECOVERED_MS=0
DEADLINE_MS=$(( START_MS + FAILOVER_THRESHOLD_MS ))
while (( $(now_ms) < DEADLINE_MS )); do
  sample="$(probe_vip)"
  status="${sample%%$'\t'*}"
  responding_gateway="${sample#*$'\t'}"
  observed_ms="$(now_ms)"
  if ! [[ "$status" =~ ^[0-9]{3}$ ]] || (( status < 200 || status >= 500 )); then
    if (( OUTAGE_START_MS == 0 )); then OUTAGE_START_MS="$observed_ms"; fi
  elif [[ "$responding_gateway" == "$FAILOVER_FROM" ]]; then
    : # The original holder still answers; continue waiting for the new holder.
  elif [[ "$responding_gateway" == gw1 || "$responding_gateway" == gw2 ]]; then
    FAILOVER_TO="$responding_gateway"
    RECOVERED_MS="$observed_ms"
    break
  else
    if (( OUTAGE_START_MS == 0 )); then OUTAGE_START_MS="$observed_ms"; fi
  fi
  sleep "0.$(printf '%03d' "$POLL_INTERVAL_MS")"
done

if (( RECOVERED_MS > 0 )); then
  after_holder="$(get_vip_holder || printf unknown)"
  elapsed_ms=$(( RECOVERED_MS - START_MS ))
  if [[ "$after_holder" == "$FAILOVER_TO" ]] && (( elapsed_ms <= FAILOVER_THRESHOLD_MS )); then
    FAILOVER_RESULT="success"
    if (( OUTAGE_START_MS > 0 )); then DOWNTIME_MS=$(( RECOVERED_MS - OUTAGE_START_MS )); fi
    echo "PASS: VIP moved from ${FAILOVER_FROM} to ${FAILOVER_TO}; service resumed in ${elapsed_ms}ms (downtime ${DOWNTIME_MS}ms)."
    exit 0
  fi
  FAILOVER_TO="$after_holder"
  FAILOVER_REASON="HTTP responder changed to ${responding_gateway}, but live status reports ${after_holder} or threshold was exceeded (${elapsed_ms}ms)"
else
  now="$(now_ms)"
  if (( OUTAGE_START_MS > 0 )); then DOWNTIME_MS=$(( now - OUTAGE_START_MS )); fi
  FAILOVER_REASON="VIP did not move to a different responding gateway within ${FAILOVER_THRESHOLD_MS}ms"
fi

echo "FAIL: ${FAILOVER_REASON} (before=${FAILOVER_FROM}, after=${FAILOVER_TO}, downtime=${DOWNTIME_MS}ms)" >&2
exit 1
