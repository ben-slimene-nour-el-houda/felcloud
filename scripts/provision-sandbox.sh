#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEAM_NAME="${1:-${TEAM_NAME:-team-demo}}"
IMAGE_NAME="${OPENSTACK_IMAGE_NAME:-${IMAGE_NAME:-Ubuntu 24.04 LTS - Noble Numbat}}"
FLAVOR_NAME="${OPENSTACK_FLAVOR_NAME:-${FLAVOR_NAME:-G0.basic.2c2g}}"
KEYPAIR_NAME="${OPENSTACK_KEYPAIR_NAME:-${KEYPAIR_NAME:-felcloud-resilient-v2}}"
AVAILABILITY_ZONE="${OPENSTACK_AVAILABILITY_ZONE:-${AVAILABILITY_ZONE:-TN-Carthage}}"
NETWORK_NAME="${OPENSTACK_NETWORK_NAME:-${NETWORK_NAME:-felcloud-sandbox}}"
SECURITY_GROUP_NAME="${OPENSTACK_SECURITY_GROUP_NAME:-${SECURITY_GROUP_NAME:-sg-sandbox}}"
USER_DATA_FILE="${USER_DATA_FILE:-}"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required tool not found: $1" >&2
    exit 1
  fi
}

ensure_openstack_auth() {
  if ! openstack token issue >/dev/null 2>&1; then
    echo "OpenStack authentication is not configured. Export OS_* variables or source your OpenStack RC file first." >&2
    exit 1
  fi
}

resolve_image_id() {
  local image_id
  image_id="$(openstack image show "$IMAGE_NAME" -f value -c id 2>/dev/null || true)"
  if [[ -z "$image_id" ]]; then
    echo "Image not found: $IMAGE_NAME" >&2
    exit 1
  fi
  printf '%s' "$image_id"
}

resolve_flavor_id() {
  local flavor_id
  flavor_id="$(openstack flavor show "$FLAVOR_NAME" -f value -c id 2>/dev/null || true)"
  if [[ -z "$flavor_id" ]]; then
    echo "Flavor not found: $FLAVOR_NAME" >&2
    exit 1
  fi
  printf '%s' "$flavor_id"
}

resolve_network_id() {
  local network_id
  network_id="$(openstack network show "$NETWORK_NAME" -f value -c id 2>/dev/null || true)"
  if [[ -z "$network_id" ]]; then
    echo "Network not found: $NETWORK_NAME" >&2
    exit 1
  fi
  printf '%s' "$network_id"
}

resolve_security_group_id() {
  local group_id
  group_id="$(openstack security group show "$SECURITY_GROUP_NAME" -f value -c id 2>/dev/null || true)"
  if [[ -z "$group_id" ]]; then
    echo "Security group not found: $SECURITY_GROUP_NAME" >&2
    exit 1
  fi
  printf '%s' "$group_id"
}

build_user_data() {
  if [[ -n "$USER_DATA_FILE" ]]; then
    cat "$USER_DATA_FILE"
    return
  fi

  cat <<EOF
#cloud-config
packages:
  - python3
runcmd:
  - mkdir -p /var/www/${TEAM_NAME}
  - sh -c 'echo "Hello from ${TEAM_NAME} sandbox!" > /var/www/${TEAM_NAME}/index.html'
  - nohup python3 -m http.server 80 --directory /var/www/${TEAM_NAME} >/var/log/${TEAM_NAME}_http.log 2>&1 &
EOF
}

create_port() {
  local port_name="$1"
  local network_id="$2"
  local security_group_id="$3"
  local port_id

  if openstack port show "$port_name" >/dev/null 2>&1; then
    openstack port show "$port_name" -f value -c id
    return 0
  fi

  port_id="$(openstack port create \
    --network "$network_id" \
    --security-group "$security_group_id" \
    --disable-port-security \
    "$port_name" \
    -f value -c id)"

  printf '%s' "$port_id"
}

wait_for_server_active() {
  local server_name="$1"
  local deadline=$((SECONDS + 180))

  while (( SECONDS < deadline )); do
    local status
    status="$(openstack server show "$server_name" -f value -c status 2>/dev/null || true)"
    if [[ "$status" == "ACTIVE" ]]; then
      return 0
    fi
    if [[ "$status" == "ERROR" || "$status" == "BUILD" || "$status" == "REBOOT" ]]; then
      sleep 5
      continue
    fi
    sleep 5
  done

  echo "Timed out waiting for server ${server_name} to become ACTIVE." >&2
  return 1
}

require_tool openstack
ensure_openstack_auth

if [[ -z "$TEAM_NAME" ]]; then
  echo "Usage: $0 <team-name>" >&2
  exit 1
fi

SERVER_NAME="${TEAM_NAME//[^a-zA-Z0-9_.-]/-}"
PORT_NAME="${SERVER_NAME}-port"

IMAGE_ID="$(resolve_image_id)"
FLAVOR_ID="$(resolve_flavor_id)"
NETWORK_ID="$(resolve_network_id)"
SECURITY_GROUP_ID="$(resolve_security_group_id)"

if openstack server show "$SERVER_NAME" >/dev/null 2>&1; then
  echo "Sandbox ${SERVER_NAME} already exists. Reusing it."
  openstack server show "$SERVER_NAME" -f json
  exit 0
fi

PORT_ID="$(create_port "$PORT_NAME" "$NETWORK_ID" "$SECURITY_GROUP_ID")"

USER_DATA_TMP="$(mktemp)"
trap 'rm -f "$USER_DATA_TMP"' EXIT
build_user_data > "$USER_DATA_TMP"

openstack server create \
  --name "$SERVER_NAME" \
  --image "$IMAGE_ID" \
  --flavor "$FLAVOR_ID" \
  --key-name "$KEYPAIR_NAME" \
  --availability-zone "$AVAILABILITY_ZONE" \
  --nic port-id="$PORT_ID" \
  --security-group "$SECURITY_GROUP_ID" \
  --user-data "$USER_DATA_TMP" \
  "$SERVER_NAME" >/dev/null

wait_for_server_active "$SERVER_NAME"

IP_ADDRESS="$(openstack server show "$SERVER_NAME" -f value -c addresses 2>/dev/null | awk -F'=' '{print $NF}' | tr ',' '\n' | head -n 1 | tr -d ' ' || true)"
if [[ -z "$IP_ADDRESS" ]]; then
  IP_ADDRESS="$(openstack port show "$PORT_NAME" -f value -c fixed_ips 2>/dev/null | awk -F'=' '{print $2}' | sed 's/,.*//' || true)"
fi

printf '\nSandbox provisioned successfully\n'
printf 'name=%s\n' "$SERVER_NAME"
printf 'network=%s\n' "$NETWORK_NAME"
printf 'security_group=%s\n' "$SECURITY_GROUP_NAME"
printf 'private_ip=%s\n' "${IP_ADDRESS:-unknown}"
printf 'image=%s\n' "$IMAGE_NAME"
printf 'flavor=%s\n' "$FLAVOR_NAME"
printf 'keypair=%s\n' "$KEYPAIR_NAME"
