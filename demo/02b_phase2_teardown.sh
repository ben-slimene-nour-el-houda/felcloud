#!/usr/bin/env bash
# Phase 2 demo: lease expiry and automatic cleanup, with no DELETE request.
# Run from the control-plane/OpenStack CLI host. Set IP_QUARANTINE_SECONDS on
# the control plane to a short demo value (for example 10), then restart it if
# you need QUARANTINED -> FREE to fit the recording. This script only observes.
# Optional: CP_API, CP_TOKEN, DOMAIN, GW_VIP, TEAM, TTL, KEEP_TEAM,
# EXPECTED_VMS, IP_QUARANTINE_SECONDS, REAPER_INTERVAL_SECONDS, MAX_WAIT_SECONDS.
set -uo pipefail

API=${CP_API:-http://localhost:8081}
TOKEN=${CP_TOKEN:-felcloud-test-token-cstam-2026}
DOMAIN=${DOMAIN:-cloudpulse.cstam.felcloud.tn}
VIP=${GW_VIP:-197.5.133.119}
TEAM=${TEAM:-demoexp}
TTL=${TTL:-60}
KEEP=${KEEP_TEAM:-team1}
EXPECTED_VMS=${EXPECTED_VMS:-}
QUARANTINE=${IP_QUARANTINE_SECONDS:-10}
REAPER_INTERVAL=${REAPER_INTERVAL_SECONDS:-5}
MAX_WAIT=${MAX_WAIT_SECONDS:-$((TTL + QUARANTINE + REAPER_INTERVAL * 2 + 120))}
PAUSE=${PAUSE:-0}
AUTH=(); [[ -n $TOKEN ]] && AUTH=(-H "Authorization: Bearer $TOKEN")

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "required command not found: $1"; }
b() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; [[ $PAUSE == 1 ]] && read -r -p '[enter]' || true; }
for c in curl jq openstack; do need "$c"; done
[[ $TTL =~ ^[1-9][0-9]*$ && $MAX_WAIT =~ ^[1-9][0-9]*$ ]] || die 'TTL and MAX_WAIT_SECONDS must be positive integers'
[[ $TEAM =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die 'TEAM must be a lowercase DNS label'

h="$TEAM.$DOMAIN"; keep="$KEEP.$DOMAIN"
code_of() { local code; code=$(curl -sS -o /dev/null --max-time 2 -w '%{http_code}' --resolve "$1:80:$VIP" "http://$1/" 2>/dev/null) || code=000; printf '%s' "$code"; }
get() { curl -fsS "${AUTH[@]}" "$API$1"; }
lease() { get /api/leases | jq -r --arg t "$TEAM" '[.leases[]|select(.entity_name==$t and .entity_type=="TEAM_SANDBOX")|.status] | if any(.=="ALLOCATED") then "ALLOCATED" elif any(.=="QUARANTINED") then "QUARANTINED" elif any(.=="FREE") then "FREE" else "-" end'; }
sbx() { get /api/sandboxes | jq -r --arg t "$TEAM" '[.sandboxes[]|select(.sandbox_id==$t)|.status][0] // "gone"'; }
ttl_left() { get /api/sandboxes | jq -r --arg t "$TEAM" '[.sandboxes[]|select(.sandbox_id==$t)|.ttl_remaining_seconds][0] // "-"'; }
backends() { get '/api/gateways/generations?limit=1' | jq -r '.generations[0].backend_count // "-"'; }
server_list() { openstack server list --no-name-lookup -f value -c Name; }
vm_present() { server_list | grep -Eq "^(sandbox-)?${TEAM}$"; }

tmp=$(mktemp -d); trap 'kill "${probe_pid:-}" 2>/dev/null || true; rm -rf "$tmp"' EXIT
baseline_vms=$(printf '%s\n' "$(server_list)" | sed '/^[[:space:]]*$/d' | wc -l)
EXPECTED_VMS=${EXPECTED_VMS:-$baseline_vms}
b "Provision $h with a ${TTL}s lease (new sandbox returns HTTP 201)"
existing=$(get /api/sandboxes 2>/dev/null) || existing="{}"
if jq -e --arg id "$TEAM" 'any(.sandboxes[]?; .sandbox_id==$id and .status!="RELEASED" and .status!="FAILED")' <<<"$existing" >/dev/null; then
  curl -sS -X DELETE "${AUTH[@]}" "$API/api/sandboxes/$TEAM" >/dev/null 2>&1 || true
  openstack server delete "sandbox-$TEAM" 2>/dev/null || true
  sleep 3
fi
body=$(jq -nc --arg id "$TEAM" --argjson ttl "$TTL" '{sandbox_id:$id,ttl:$ttl}')
code=$(curl -sS -o "$tmp/provision.json" -w '%{http_code}' --max-time 300 -X POST "$API/api/sandboxes/provision" \
  "${AUTH[@]}" -H 'Content-Type: application/json' -d "$body") || code=000
[[ $code == 201 ]] || { cat "$tmp/provision.json" >&2; die "expected a new provision (HTTP 201), got HTTP $code"; }
jq -e --arg id "$TEAM" '.sandbox_id==$id and .state=="ACTIVE"' "$tmp/provision.json" >/dev/null || die 'provision response was not ACTIVE'
for _ in $(seq 1 30); do [[ $(code_of "$h") == 200 ]] && break; sleep 1; done
[[ $(code_of "$h") == 200 ]] || die "$h did not answer HTTP 200 through the gateway VIP"
printf '  %s -> HTTP 200  sandbox=%s  lease=%s  ttl=%ss  gateway_backends=%s\n' "$h" "$(sbx)" "$(lease)" "$(ttl_left)" "$(backends)"
openstack server list --no-name-lookup -c Name -c Status -c Networks

b "Wait for expiry and reaper; probe $keep continuously (no DELETE call)"
: > "$tmp/keepalive"
(while :; do printf '%s\n' "$(code_of "$keep")" >> "$tmp/keepalive"; sleep 0.5; done) & probe_pid=$!
prev=''; seen_quarantine=0; seen_released=0; completed=0
start=$(date +%s)
while (( $(date +%s) - start < MAX_WAIT )); do
  cur="ttl=$(ttl_left)s sandbox=$(sbx) VM=$(if vm_present; then echo yes; else echo no; fi) lease=$(lease) backends=$(backends) route_HTTP$(code_of "$h")"
  if [[ $cur != "$prev" ]]; then printf '  [%s] %s\n' "$(date +%T)" "$cur"; prev=$cur; fi
  [[ $(sbx) == RELEASED ]] && seen_released=1
  [[ $(lease) == QUARANTINED ]] && seen_quarantine=1
  if ! vm_present && [[ $(lease) == FREE ]]; then completed=1; break; fi
  sleep 1
done
kill "$probe_pid" 2>/dev/null || true; wait "$probe_pid" 2>/dev/null || true; unset probe_pid

b 'Verify teardown, route removal, IP reclamation, and uninterrupted permanent route'
openstack server list --no-name-lookup -c Name -c Status -c Networks
names=$(server_list) || die 'could not list Nova servers'
vm_count=$(printf '%s\n' "$names" | sed '/^[[:space:]]*$/d' | wc -l)
route_code=$(code_of "$h")
keep_total=$(wc -l < "$tmp/keepalive")
keep_bad=$(awk '$1!="200"{n++} END{print n+0}' "$tmp/keepalive")
printf '  Nova VMs=%s (expected %s); %s VM present=%s\n' "$vm_count" "$EXPECTED_VMS" "$TEAM" "$(vm_present && echo yes || echo no)"
printf '  sandbox status=%s; IP lease=%s; quarantine observed=%s; route HTTP=%s\n' "$(sbx)" "$(lease)" "$seen_quarantine" "$route_code"
printf '  %s: %s probes, %s non-200\n' "$keep" "$keep_total" "$keep_bad"
[[ $completed == 1 ]] || die "cleanup did not reach FREE within ${MAX_WAIT}s (set a shorter control-plane quarantine for the demo)"
[[ $seen_released == 1 && $seen_quarantine == 1 ]] || die 'did not observe both sandbox RELEASED and IP QUARANTINED states; increase polling visibility or use a longer demo quarantine'
[[ $vm_count -eq $EXPECTED_VMS ]] || die "expected $EXPECTED_VMS VMs after cleanup, found $vm_count"
! vm_present || die "$TEAM VM still exists"
[[ $route_code != 200 ]] || die "$h still answers HTTP 200 after teardown"
(( keep_total > 0 && keep_bad == 0 )) || die "$keep did not remain HTTP 200 for every probe"
printf '  PASS: server row retained as RELEASED for audit; Nova VM absent; route removed; IP is FREE.\n'
