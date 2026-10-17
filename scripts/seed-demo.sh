#!/usr/bin/env bash
# scripts/seed-demo.sh — Provision 4 mock team sandboxes for live demo
# Usage: ./scripts/seed-demo.sh [BASE_URL] [ADMIN_TOKEN]
# Requires: curl, jq (optional for pretty output)
set -euo pipefail

BASE_URL="${1:-${CONTROL_PLANE_BASE_URL:-http://localhost:8081}}"
TOKEN="${2:-${ADMIN_TOKEN:-felcloud-test-token-cstam-2026}}"

TEAMS=(team-alpha team-beta team-gamma team-delta)

echo "🌱 FelCloud Demo Seeder"
echo "   Base URL : $BASE_URL"
echo "   Provisioner: ${PROVISIONER:-auto-detect}"
echo ""

for TEAM in "${TEAMS[@]}"; do
  echo -n "  → Provisioning sandbox '$TEAM'… "
  HTTP=$(curl -s -o /tmp/fc_seed_resp.json -w "%{http_code}" \
    -X POST "$BASE_URL/api/sandboxes/provision" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"team\":\"$TEAM\",\"ttl\":7200}")
  
  if [ "$HTTP" = "201" ] || [ "$HTTP" = "200" ]; then
    IP=$(jq -r '.ip_address // .ip // "?"' /tmp/fc_seed_resp.json 2>/dev/null || echo "?")
    echo "✓ (HTTP $HTTP) IP=$IP"
  elif [ "$HTTP" = "409" ]; then
    echo "⚠ Already exists (HTTP 409)"
  else
    echo "✗ FAILED HTTP $HTTP"
    cat /tmp/fc_seed_resp.json 2>/dev/null || true
  fi
done

echo ""
echo "✅  Demo seed complete. Open dashboard at $BASE_URL/ui/"
echo "    Admin token: $TOKEN"
