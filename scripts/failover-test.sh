#!/usr/bin/env bash
# Runs a measured failover against the control plane API and records the result.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_URL="${API_URL:-${GATEWAY_API_BASE_URL:-${CONTROL_PLANE_BASE_URL:-http://localhost:8081}}}"
ADMIN_TOKEN="${ADMIN_TOKEN:-${CONTROL_PLANE_TOKEN:-}}"
FAILURE_MODE="${FAILURE_MODE:-haproxy-only}"
RUNS="${RUNS:-3}"
PAUSE_SECONDS="${PAUSE_SECONDS:-20}"
FAILOVER_THRESHOLD_MS="${FAILOVER_THRESHOLD_MS:-3000}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/docs/benchmarks/failover-$(date -u +%Y%m%dT%H%M%SZ)}"

if ! [[ "$FAILURE_MODE" =~ ^(keepalived|haproxy-only)$ ]]; then
  echo "FAILURE_MODE must be keepalived or haproxy-only" >&2
  exit 2
fi

if ! [[ "$RUNS" =~ ^[1-9][0-9]*$ ]] || (( RUNS > 20 )); then
  echo "RUNS must be an integer from 1 to 20" >&2
  exit 2
fi

if ! [[ "$PAUSE_SECONDS" =~ ^[0-9]+$ ]] || (( PAUSE_SECONDS > 300 )); then
  echo "PAUSE_SECONDS must be an integer from 0 to 300" >&2
  exit 2
fi

if ! [[ "$FAILOVER_THRESHOLD_MS" =~ ^[1-9][0-9]*$ ]] || (( FAILOVER_THRESHOLD_MS > 60000 )); then
  echo "FAILOVER_THRESHOLD_MS must be an integer from 1 to 60000" >&2
  exit 2
fi

if [[ -z "$API_URL" ]]; then
  echo "API_URL must be set (or GATEWAY_API_BASE_URL / CONTROL_PLANE_BASE_URL)" >&2
  exit 2
fi

api_headers=()
if [[ -n "$ADMIN_TOKEN" ]]; then
  api_headers+=("-H" "Authorization: Bearer ${ADMIN_TOKEN}")
fi

post_failover() {
  local payload
  payload="$(python3 - "$FAILURE_MODE" <<'PY'
import json, sys
print(json.dumps({"confirm": True, "mode": sys.argv[1]}))
PY
)"

  curl -fsS --connect-timeout 2 --max-time 15 \
    -H 'Content-Type: application/json' \
    "${api_headers[@]}" \
    --data "$payload" \
    "${API_URL%/}/api/gateways/failover"
}

fetch_run_metrics() {
  local response="$1"
  python3 - "$response" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
required = [
    "mode", "success", "before", "after", "downtime_ms", "failover_ms",
    "failed_requests", "total_requests", "serving_gateway_before",
    "serving_gateway_after", "verified_by",
]
missing = [key for key in required if key not in payload]
if missing:
    raise SystemExit(f"response missing required failover keys: {', '.join(missing)}")
if payload["serving_gateway_before"] == payload["serving_gateway_after"]:
    raise SystemExit("response did not verify an actual gateway transition")
print(json.dumps({
    "mode": payload["mode"],
    "failover_ms": int(payload.get("failover_ms", 0) or 0),
    "failed_requests": int(payload.get("failed_requests", 0) or 0),
    "total_requests": int(payload.get("total_requests", 0) or 0),
    "serving_gateway_before": payload["serving_gateway_before"],
    "serving_gateway_after": payload["serving_gateway_after"],
    "verified_by": payload.get("verified_by", "responding-gateway-header"),
    "success": bool(payload.get("success", False)),
    "reason": payload.get("reason", "")
}, separators=(",", ":")))
PY
}

mkdir -p "$RESULT_DIR"

runs_json='[]'
first_run=true
for ((i=1;i<=RUNS;i++)); do
  echo "[failover] run ${i}/${RUNS} with mode=${FAILURE_MODE}"
  response="$(post_failover)"
  metrics="$(fetch_run_metrics "$response")"
  if ! python3 - "$metrics" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
if not payload.get("success"):
    raise SystemExit(payload.get("reason") or "failover request was not successful")
PY
  then
    echo "$metrics" >&2
    exit 1
  fi

  json_value="$(python3 - "$metrics" <<'PY'
import json, sys
print(json.dumps(json.loads(sys.argv[1])))
PY
)"
  if [[ "$first_run" == true ]]; then
    runs_json="[$json_value]"
    first_run=false
  else
    runs_json="$(python3 - "$runs_json" "$json_value" <<'PY'
import json, sys
runs = json.loads(sys.argv[1])
runs.append(json.loads(sys.argv[2]))
print(json.dumps(runs))
PY
)"
  fi

  if (( i < RUNS )); then
    sleep "$PAUSE_SECONDS"
  fi
done

payload="$(python3 - "$runs_json" <<'PY'
import json, sys
runs = json.loads(sys.argv[1])
for run in runs:
    run["mode"] = run.get("mode", "haproxy-only")
    if run["mode"] not in {"haproxy-only", "keepalived", "gateway-reboot"}:
        raise SystemExit(f"unsupported failover mode: {run['mode']}")
    if run["total_requests"] < 1 or run["failed_requests"] > run["total_requests"]:
        raise SystemExit("total_requests must be >= 1 and failed_requests <= total_requests")
    if run["serving_gateway_before"] == run["serving_gateway_after"]:
        raise SystemExit("gateway before/after must differ")
    if run.get("verified_by") not in {"responding-gateway-header", "vip-owner"}:
        raise SystemExit("verified_by must be responding-gateway-header or vip-owner")
print(json.dumps({
    "runs": runs,
    "date": __import__('datetime').datetime.utcnow().strftime('%Y-%m-%dT%H:%M:%SZ'),
    "commit": __import__('subprocess').check_output(['git', '-C', __import__('os').path.dirname(__import__('os').path.dirname(__import__('os').path.abspath(__file__))), 'rev-parse', '--short', 'HEAD'], text=True).strip(),
}, separators=(",", ":")))
PY
)"

printf '%s\n' "$payload" > "$RESULT_DIR/results.json"

max_ms="$(python3 - "$payload" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
values = [run["failover_ms"] for run in payload["runs"]]
print(max(values) if values else 0)
PY
)"
if (( max_ms > FAILOVER_THRESHOLD_MS )); then
  echo "FAIL: max failover_ms=${max_ms} exceeds threshold ${FAILOVER_THRESHOLD_MS}ms" >&2
  exit 1
fi

cat "$RESULT_DIR/results.json"
