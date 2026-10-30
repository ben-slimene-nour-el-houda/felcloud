#!/usr/bin/env bash
# Fires a rapid concurrent HTTP request burst to trigger HAProxy rate limiting (HTTP 429).
set -e

KEY_PATH="/home/ben-slimene-nour-el-houda/felcloud/ansible/.ssh/id_rsa_felcloud"
BASTION_IP="197.5.133.77"
VIP_IP="10.20.20.246"
TARGET_HOST="live1.cloudpulse.cstam.felcloud.tn"
CONCURRENCY=120

echo "==> Firing burst of ${CONCURRENCY} rapid requests from bastion to VIP ${VIP_IP} (${TARGET_HOST})..."
ssh -i "${KEY_PATH}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "ansible@${BASTION_IP}" \
  "for i in {1..${CONCURRENCY}}; do curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: ${TARGET_HOST}' http://${VIP_IP}/ & done; wait" \
  | sort | uniq -c

echo "==> Burst complete. Check Observability -> HAProxy access logs for HTTP 429 lines."
