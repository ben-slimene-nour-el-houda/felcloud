#!/usr/bin/env bash
# ==============================================================================
# FelCloud Live OpenStack Infrastructure & Lifecycle Test Runner
# Validates:
# - Keystone authentication and endpoint discovery via clouds.yaml
# - Neutron network/subnet/security group resolution
# - N1: Real Nova server VM launch, Neutron port binding, IP allocation
# - N1: Server ACTIVE polling, resource inspection, and clean teardown
# - N3: Ownership verification, unmanaged resource protection, drift safety
# ==============================================================================
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "========================================================================"
echo "          FELCLOUD LIVE OPENSTACK INFRASTRUCTURE TESTS                 "
echo "========================================================================"

if [[ -f "$ROOT_DIR/.env" ]]; then
  echo "Loading environment from .env..."
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

export OS_CLOUD="${OS_CLOUD:-felcloud}"

echo "Cloud:            ${OS_CLOUD}"
echo "Region:           ${OS_REGION_NAME:-North-Africa}"
echo "Sandbox Network:  ${SANDBOX_NETWORK_NAME:-felcloud-sandbox}"
echo "Sandbox Flavor:   ${SANDBOX_FLAVOR:-1001}"
echo "Sandbox Keypair:  ${SANDBOX_KEYPAIR:-felcloud-resilient-v2}"
echo "========================================================================"

cd "$ROOT_DIR/controlplane"

echo ""
echo "[1/3] Validating OpenStack Keystone Auth & Endpoint Discovery..."
go test -v -tags=live -run TestLiveOpenStackAuthenticationAndDiscovery ./...

echo ""
echo "[2/3] Validating N3 Ownership & Drift Safety on Live Cloud..."
go test -v -tags=live -run TestLiveOpenStackN3OwnershipAndDriftSafety ./...

echo ""
echo "[3/3] Validating N1 Live Provision, Health & Teardown Lifecycle..."
go test -v -tags=live -timeout 10m -run TestLiveOpenStackN1ProvisionAndTeardownLifecycle ./...

echo ""
echo "========================================================================"
echo "✅ ALL REAL OPENSTACK INFRASTRUCTURE TESTS PASSED SUCCESSFULLY!"
echo "========================================================================"
