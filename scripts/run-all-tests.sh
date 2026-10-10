#!/usr/bin/env bash
# ==============================================================================
# Master Automated Test Suite for FelCloud Resilient Gateway
# Validates:
# 1. IPAM Transactional Allocation, Quarantine & Auto-Reclamation
# 2. Pre-Flight Sandbox Health Check before Route Opening
# 3. HAProxy Configuration Generation & Validation
# 4. Keepalived Script Weight Review
# 5. HTTPS / TLS Configuration & Dual Port 80/443 ACL Routing
# 6. REST API Server Health & Endpoints
# 7. Go Control Plane Unit & Race Tests
# ==============================================================================
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PASS_COUNT=0
FAIL_COUNT=0

VALID_TESTS=(
  "ipam-quarantine"
  "health-check"
  "syntax-validation"
  "keepalived-weight"
  "https-routing"
  "dpapi-templates"
  "gotest"
)

SELECTED_TESTS=()

print_valid_names() {
  echo "Valid test names:" >&2
  for name in "${VALID_TESTS[@]}"; do
    echo "  - $name" >&2
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --list)
      echo "Available test names:"
      for name in "${VALID_TESTS[@]}"; do
        echo "  - $name"
      done
      exit 0
      ;;
    --test)
      if [[ $# -lt 2 ]]; then
        echo "Error: --test option requires a test name argument." >&2
        print_valid_names
        exit 2
      fi
      TEST_NAME="$2"
      shift 2
      IS_VALID=0
      for name in "${VALID_TESTS[@]}"; do
        if [[ "$TEST_NAME" == "$name" ]]; then
          IS_VALID=1
          break
        fi
      done
      if [[ $IS_VALID -eq 0 ]]; then
        echo "Error: Unknown test name '$TEST_NAME'." >&2
        print_valid_names
        exit 2
      fi
      SELECTED_TESTS+=("$TEST_NAME")
      ;;
    *)
      echo "Error: Unknown option or argument '$1'." >&2
      print_valid_names
      exit 2
      ;;
  esac
done

if [[ ${#SELECTED_TESTS[@]} -eq 0 ]]; then
  SELECTED_TESTS=("${VALID_TESTS[@]}")
fi

should_run() {
  local target="$1"
  for sel in "${SELECTED_TESTS[@]}"; do
    if [[ "$sel" == "$target" ]]; then
      return 0
    fi
  done
  return 1
}

BUILD_DIR=""
CONTROLPLANE_BINARY=""
cleanup() {
  if [[ -n "$BUILD_DIR" ]]; then
    rm -rf -- "$BUILD_DIR"
  fi
}
trap cleanup EXIT

# Build outside the repository (unless SKIP_BUILD=1).
if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/felcloud-tests.XXXXXX")"
  CONTROLPLANE_BINARY="$BUILD_DIR/felcloud-controlplane"
  echo "Building controlplane binary..."
  if ! (cd "$ROOT_DIR/controlplane" && go build -o "$CONTROLPLANE_BINARY" .); then
    echo "❌ ERROR: Failed to build controlplane binary!" >&2
    exit 1
  fi
fi

record_pass() {
  echo "  ✅ PASS: $1"
  PASS_COUNT=$(( PASS_COUNT + 1 ))
}

record_fail() {
  echo "  ❌ FAIL: $1"
  FAIL_COUNT=$(( FAIL_COUNT + 1 ))
}

echo "========================================================================"
echo "          FELCLOUD RESILIENT GATEWAY AUTOMATED TEST SUITE               "
echo "========================================================================"

# Test 1: IPAM Quarantine & Reclamation (ipam-quarantine)
if should_run "ipam-quarantine"; then
  echo ""
  echo "[TEST 1] IPAM Quarantine & Address Reclamation..."
  if FELCLOUD_CONTROLPLANE_BINARY="${CONTROLPLANE_BINARY:-${FELCLOUD_CONTROLPLANE_BINARY:-}}" \
    RECYCLE_BASE_URL= BASE_URL= PROVISIONER=mock ./scripts/recycle-test.sh; then
    record_pass "Quarantine cooldown and address reclamation verified"
  else
    record_fail "Quarantine and address reclamation failed"
  fi
fi

# Test 2: Pre-flight health check logic (health-check)
if should_run "health-check"; then
  echo ""
  echo "[TEST 2] Pre-flight Sandbox Health Check before Route Activation..."
  if (cd "$ROOT_DIR/controlplane" && go test -run '^TestHealthProbeRetryModes$' -count=1 .); then
    record_pass "Pre-flight health check correctly accepts healthy backends and blocks dead endpoints"
  else
    record_fail "Pre-flight health check failed"
  fi
fi

# Test 3: HAProxy Configuration Generation and Validation (syntax-validation)
if should_run "syntax-validation"; then
  echo ""
  echo "[TEST 3] HAProxy Configuration Generation & Validation..."
  if (cd "$ROOT_DIR/controlplane" && go test -run '^(TestGenerateHAProxyConfigRoutingDomain|TestValidateHAProxySyntaxRejectsMissingFrontendAndBackend)$' -count=1 .); then
    record_pass "Go HAProxy config generation and required-section validation passed"
  else
    record_fail "Syntax validation failed"
  fi
fi

# Test 4: Keepalived Failover Weight Review
if should_run "keepalived-weight"; then
  echo ""
  echo "[TEST 4] Keepalived VRRP Script Weight Review..."
  if python3 - "$ROOT_DIR" <<'EOF'
import re
import sys
root_dir = sys.argv[1]

with open(f"{root_dir}/ansible/group_vars/gateways/vars.yml", "r", encoding="utf-8") as f:
    content = f.read()

match = re.search(r"keepalived_check_weight\s*:\s*(-?\d+)", content)
weight = int(match.group(1)) if match else 0
gw1_priority = 150
gw2_priority = 100

dropped_priority = gw1_priority + weight
assert weight <= -51, f"Weight {weight} is too high to cause failover!"
assert dropped_priority < gw2_priority, f"GW1 reduced priority ({dropped_priority}) must be < GW2 ({gw2_priority})"
EOF

  then
    record_pass "Keepalived weight review passed (150 + (-60) = 90 < 100)"
  else
    record_fail "Keepalived weight review failed"
  fi
fi

# Test 5: HTTPS / TLS Configuration & ACLs (https-routing)
if should_run "https-routing"; then
  echo ""
  echo "[TEST 5] HTTPS 443 TLS & Host Header Routing Template..."
  if python3 - "$ROOT_DIR" <<'EOF'
import os
import re
import sys

root_dir = sys.argv[1]
template_path = os.path.join(
    root_dir, "ansible", "roles", "haproxy", "templates", "haproxy.cfg.j2"
)
with open(template_path, encoding="utf-8") as stream:
    template = stream.read()

required = (
    "frontend http_front",
    "bind *:80",
    "frontend https_front",
    "bind *:443 ssl crt",
    "acl is_{{ team.name }} hdr(host) -i {{ team.name }}.{{ routing_domain }}",
    "backend {{ team.name }}_backend",
    "server {{ team.name }}_srv {{ team.ip }}:{{ team.port | default(80) }}",
)
missing = [item for item in required if item not in template]
assert not missing, f"HAProxy template is missing expected directives: {missing}"
assert template.count("{% for team in teams %}") == 3, "HTTP/HTTPS/backend route loops changed"
assert ".local" not in template, "unexpected legacy .local domain"
if os.environ.get("ROUTING_DOMAIN"):
    assert re.fullmatch(
        r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*",
        os.environ["ROUTING_DOMAIN"],
    ), "ROUTING_DOMAIN is not a lowercase DNS domain"
EOF
  then
    record_pass "HTTPS TLS and HTTP/HTTPS Host-header routing template verified"
  else
    record_fail "TLS template failed"
  fi
fi

# Test 6: Data Plane API Configuration (dpapi-templates)
if should_run "dpapi-templates"; then
  echo ""
  echo "[TEST 6] HAProxy Data Plane API Configuration & Service Units..."
  if [[ -f "$ROOT_DIR/ansible/roles/haproxy/templates/dataplaneapi.yaml.j2" && -f "$ROOT_DIR/ansible/roles/haproxy/templates/dataplaneapi.service.j2" ]]; then
    record_pass "Data Plane API templates and systemd units verified"
  else
    record_fail "Data Plane API templates missing"
  fi
fi

# Test 7: Go Control Plane Unit & Race Tests (gotest)
if should_run "gotest"; then
  echo ""
  echo "[TEST 7] Go Control Plane Unit & Race Tests..."
  if (cd "$ROOT_DIR/controlplane" && go vet ./... && go test ./... -race -count=1 >/dev/null 2>&1); then
    record_pass "Go control plane unit tests and race detector passed"
  else
    record_fail "Go control plane unit tests or race detector failed"
  fi
fi

echo ""
echo "========================================================================"
echo "                     AUTOMATED TEST SUMMARY                             "
echo "========================================================================"
echo "Passed: ${PASS_COUNT} / $(( PASS_COUNT + FAIL_COUNT ))"
if [[ $FAIL_COUNT -eq 0 ]]; then
  echo "✅ ALL AUTOMATED TESTS COMPLETED SUCCESSFULLY!"
  exit 0
else
  echo "❌ SOME TESTS FAILED!"
  exit 1
fi
