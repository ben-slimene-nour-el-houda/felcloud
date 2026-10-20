#!/usr/bin/env bash
# Required: ADMIN_TOKEN, PUBLIC_VIP_IP, ROUTING_DOMAIN
# Run: ADMIN_TOKEN=... PUBLIC_VIP_IP=... ROUTING_DOMAIN=... ./demo/01_provision_10.sh
set -u -o pipefail

for name in ADMIN_TOKEN PUBLIC_VIP_IP ROUTING_DOMAIN; do
  if [[ -z "${!name:-}" ]]; then echo "missing required environment variable: $name" >&2; exit 1; fi
done
for tool in curl jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then echo "missing required command: $tool" >&2; exit 1; fi
done
CONTROL_PLANE_URL=${CONTROL_PLANE_URL:-http://127.0.0.1:8081}
COUNT=${COUNT:-10}
[[ "$COUNT" =~ ^[0-9]+$ ]] && (( COUNT >= 1 && COUNT <= 10 )) || { echo "COUNT must be between 1 and 10" >&2; exit 1; }
PREFIX=${PREFIX:-bench-demo-$(date -u +%Y%m%dT%H%M%SZ)}
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
start=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
launch=$(date +%s)
echo "$start launching $COUNT registrations"

job() {
  local i=$1 id body code elapsed ip probe=000 gateway= error= provision=FAILED
  id=$(printf '%s-%02d' "$PREFIX" "$i")
  body="$tmp/$id.json"
  jq -nc --arg team "$id" --arg domain "$id.$ROUTING_DOMAIN" '{team:$team,custom_domain:$domain}' >"$body"
  read -r code elapsed < <(curl -sS -o "$tmp/$id.response" -w '%{http_code} %{time_total}' --max-time 420 \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    -H 'User-Agent: FelCloud-Benchmark/1.0' \
    --data-binary "@$body" "$CONTROL_PLANE_URL/api/sandboxes/provision" 2>/dev/null || echo '000 0')
  if [[ "$code" == 429 ]]; then provision=rate-limited
  elif [[ "$code" == 200 || "$code" == 201 ]]; then
    provision=success
    ip=$(jq -r '.fixed_ip // empty' "$tmp/$id.response" 2>/dev/null || true)
    if [[ -z "$ip" ]]; then provision='failed:missing-ip'
    else
      local until=$(( $(date +%s) + 120 ))
      while (( $(date +%s) <= until )); do
        probe=$(curl -sS -o /dev/null -D "$tmp/$id.headers" -w '%{http_code}' --max-time 4 \
          -H "Host: $id.$ROUTING_DOMAIN" "http://$PUBLIC_VIP_IP/" 2>/dev/null || echo 000)
        gateway=$(awk 'BEGIN{IGNORECASE=1} /^X-Gateway-ID:/ {gsub("\r", ""); sub(/^[^:]*:[[:space:]]*/, ""); print; exit}' "$tmp/$id.headers" 2>/dev/null)
        [[ "$probe" == 200 ]] && break
        sleep 2
      done
      [[ "$probe" == 200 ]] || provision='failed:probe-timeout'
    fi
  else provision="failed:http-$code"; fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "${ip:-}" "$provision" "$probe" "${gateway:-}" "$elapsed" "$code" >"$tmp/$id.result"
}

pids=()
for ((i=1; i<=COUNT; i++)); do job "$i" & pids+=("$!"); done
for ((done=0; done<COUNT; done++)); do
  finished=
  wait -n -p finished "${pids[@]}" 2>/dev/null || true
  for f in "$tmp"/*.result; do
    [[ -e "$f" && ! -e "$f.printed" ]] || continue
    IFS=$'\t' read -r id ip status probe gateway elapsed code <"$f"
    touch "$f.printed"
    printf '%s | %s | %s (%s) | %s | %s | %ss\n' "$id" "${ip:--}" "$status" "$code" "$probe" "${gateway:--}" "$(( $(date +%s) - launch ))"
  done
done

success=0 failed=0 limited=0; ips="$tmp/ips"; : >"$ips"; longest=0; ids=
for f in "$tmp"/*.result; do
  IFS=$'\t' read -r id ip status probe gateway elapsed code <"$f"
  ids+=" $id"
  [[ -n "$ip" ]] && printf '%s\n' "$ip" >>"$ips"
  if [[ "$status" == success && "$probe" == 200 ]]; then ((success+=1)); else ((failed+=1)); echo "failure: $id $status HTTP-$probe"; fi
  [[ "$status" == rate-limited ]] && ((limited+=1))
  seconds=$(awk -v x="$elapsed" 'BEGIN{printf "%d", x+0.999}')
  (( seconds > longest )) && longest=$seconds
done
duplicates=$(sort "$ips" | uniq -d)
distinct=$(sort -u "$ips" | wc -l | tr -d ' ')
echo "summary: succeeded $success of $COUNT, failed $failed, rate-limited $limited, distinct IPs $distinct, longest ${longest}s"
if [[ -n "$duplicates" ]]; then printf 'duplicate IPs:\n%s\n' "$duplicates"; else echo 'no duplicate IPs'; fi
echo "SANDBOXES:${ids}"
[[ "$success" -eq "$COUNT" && -z "$duplicates" ]]
