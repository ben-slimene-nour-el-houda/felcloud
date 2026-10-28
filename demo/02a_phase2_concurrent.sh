#!/usr/bin/env bash
# Phase 2 demo: concurrent provisioning. Run on the control-plane/OpenStack CLI host.
# Optional: CP_API, CP_TOKEN, DOMAIN, GW_VIP, N, PREFIX, PAYLOAD, PAUSE.
# PAYLOAD defaults to the API shape; custom payloads may contain __ID__.
set -uo pipefail

API=${CP_API:-http://localhost:8081}
TOKEN=${CP_TOKEN:-felcloud-test-token-cstam-2026}
REG_PATH=${REG_PATH:-/api/sandboxes/provision}
PAYLOAD=${PAYLOAD:-'{"sandbox_id":"__ID__"}'}
DOMAIN=${DOMAIN:-cloudpulse.cstam.felcloud.tn}
VIP=${GW_VIP:-197.5.133.119}
N=${N:-10}
PREFIX=${PREFIX:-live}
PAUSE=${PAUSE:-0}
AUTH=(); [[ -n $TOKEN ]] && AUTH=(-H "Authorization: Bearer $TOKEN")

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "required command not found: $1"; }
b() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; [[ $PAUSE == 1 ]] && read -r -p '[enter]' || true; }
[[ $N =~ ^[1-9][0-9]*$ ]] || die 'N must be a positive integer'
[[ $PREFIX =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die 'PREFIX must be a lowercase DNS label'
for c in curl jq; do need "$c"; done

case "${1:-}" in
  pane) need openstack; exec watch -n1 'openstack server list --no-name-lookup -c Name -c Status -c Networks' ;;
  cleanup)
    failed=0
    cleanup_tmp=$(mktemp)
    trap 'rm -f "$cleanup_tmp"' EXIT
    for i in $(seq 1 "$N"); do
      id="$PREFIX$i"
      code=$(curl -sS -o "$cleanup_tmp" -w '%{http_code}' --max-time 130 -X DELETE "${AUTH[@]}" "$API/api/sandboxes/$id") || code=000
      printf '%-24s HTTP %s\n' "$id" "$code"
      if [[ $code == 200 || $code == 202 || $code == 404 ]]; then
        [[ ! -s $cleanup_tmp ]] || jq -c . "$cleanup_tmp" 2>/dev/null | sed 's/^/  /' || true
      else
        failed=1
        [[ ! -s $cleanup_tmp ]] || sed 's/^/  /' "$cleanup_tmp"
      fi
    done
    exit "$failed" ;;
  '') ;;
  *) die "usage: $0 [pane|cleanup]" ;;
esac

need bc
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
b "Sending $N provisioning requests concurrently"
existing=$(curl -fsS "${AUTH[@]}" "$API/api/sandboxes") || die 'could not read sandbox list for collision check'
for i in $(seq 1 "$N"); do
  id="$PREFIX$i"
  jq -e --arg id "$id" 'any(.sandboxes[]?; .sandbox_id==$id and .status!="RELEASED" and .status!="FAILED")' <<<"$existing" >/dev/null && die "$id already exists; run '$0 cleanup' or choose a fresh PREFIX"
done
t0=$(date +%s.%N)
for i in $(seq 1 "$N"); do
  (
    id="$PREFIX$i"
    body=${PAYLOAD//__ID__/$id}
    code=$(curl -sS -o "$tmp/$i.body" -w '%{http_code}' --max-time 300 -X POST "$API$REG_PATH" \
      "${AUTH[@]}" -H 'Content-Type: application/json' -d "$body") || code=000
    printf '%s\n' "$code" > "$tmp/$i.code"
  ) &
done
wait
t1=$(date +%s.%N)
ok=0
for i in $(seq 1 "$N"); do
  id="$PREFIX$i"; code=$(cat "$tmp/$i.code")
  printf '  %-24s HTTP %s' "$id" "$code"
  if [[ $code == 201 ]] && jq -e --arg id "$id" '.sandbox_id == $id and .state == "ACTIVE"' "$tmp/$i.body" >/dev/null; then
    printf '  ACTIVE\n'; ((ok+=1))
  else
    printf '  FAILED\n'
    jq -c . "$tmp/$i.body" 2>/dev/null | sed 's/^/    /' || true
  fi
done
printf '\n  %s/%s active in %.1fs\n' "$ok" "$N" "$(echo "$t1 - $t0" | bc -l)"
(( ok == N )) || die "only $ok of $N sandboxes reached ACTIVE"

b 'IPAM: require one allocated, distinct private IP for every sandbox'
leases=$(curl -fsS "${AUTH[@]}" "$API/api/leases") || die 'could not read IPAM leases'
printf '%s\n' "$leases" | jq -r --arg p "$PREFIX" \
  '.leases[] | select(.entity_type=="TEAM_SANDBOX" and (.entity_name|startswith($p)) and .status=="ALLOCATED") | "\(.ip_address)\t\(.entity_name)\t\(.status)"' | sort -V | tee "$tmp/leases"
count=$(wc -l < "$tmp/leases")
unique=$(cut -f1 "$tmp/leases" | sort -u | wc -l)
allocated=$(awk -F '\t' '$3=="ALLOCATED"{n++} END{print n+0}' "$tmp/leases")
printf '  leases=%s distinct_ips=%s allocated=%s\n' "$count" "$unique" "$allocated"
[[ $count -eq $N && $unique -eq $N && $allocated -eq $N ]] || die 'IPAM did not show N unique ALLOCATED leases'

b "Routing each Host through gateway VIP $VIP"
failed=0
for i in $(seq 1 "$N"); do
  host="$PREFIX$i.$DOMAIN"; code=000; gw=''
  for _ in $(seq 1 20); do
    headers=$(curl -sS -o /dev/null -D - --max-time 4 --resolve "$host:80:$VIP" "http://$host/" 2>/dev/null) || headers=''
    code=$(awk 'NR==1{print $2}' <<<"$headers"); [[ -n $code ]] || code=000
    gw=$(awk -F': *' 'tolower($1)=="x-gateway-id"{gsub("\r", "", $2); print $2}' <<<"$headers")
    [[ $code == 200 && -n $gw ]] && break
    sleep 1
  done
  printf '  Host: %-32s HTTP %-3s X-Gateway-ID: %s\n' "$host" "$code" "${gw:--}"
  [[ $code == 200 && ( $gw == gw1 || $gw == gw2 ) ]] || failed=1
done
(( failed == 0 )) || die 'one or more routes failed HTTP 200 / gateway identity verification'
