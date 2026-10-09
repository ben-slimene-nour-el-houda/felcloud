#!/usr/bin/env bash
set -Eeuo pipefail

TEAM_NAME="${1:-${TEAM_NAME:-team-demo}}"
PORT="${PORT:-80}"
API_BASE_URL="${CONTROL_PLANE_BASE_URL:-http://127.0.0.1:8080}"

if [[ "$PORT" != "80" ]]; then
  echo "The provision API currently serves sandboxes on port 80; PORT must be 80." >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "Required tool not found: curl" >&2
  exit 1
fi

PAYLOAD="$(TEAM_NAME="$TEAM_NAME" python3 -c 'import json, os; print(json.dumps({"team": os.environ["TEAM_NAME"]}))')"
echo "Requesting sandbox ${TEAM_NAME} through ${API_BASE_URL}/api/sandboxes/provision..."
curl --fail-with-body --silent --show-error \
  -X POST "${API_BASE_URL%/}/api/sandboxes/provision" \
  -H 'Content-Type: application/json' \
  --data "$PAYLOAD"
printf '\n'
