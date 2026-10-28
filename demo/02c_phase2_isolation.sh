#!/usr/bin/env bash
# Phase 2 demo: network isolation. All connectivity tests originate in a sandbox.
# Defaults reflect the current lab topology; override addresses/users for your deployment.
# Optional: BASTION, BASTION_USER, PROBER_IP, PROBER_USER, SSH_KEY, PROBER_SSH,
# GW_SB_IPS, GW_MGMT_IPS, MGMT_PORTS, OTHER_SANDBOX, OTHER_PORTS, SG, PAUSE.
set -uo pipefail

BASTION=${BASTION:-197.5.133.77}
BASTION_USER=${BASTION_USER:-ansible}
PROBER_IP=${PROBER_IP:-10.20.30.37}
PROBER_USER=${PROBER_USER:-ubuntu}
SSH_KEY=${SSH_KEY:-/home/ben-slimene-nour-el-houda/felcloud/ansible/.ssh/id_rsa_felcloud}
SANDBOX_KEY=${SANDBOX_KEY:-/home/ben-slimene-nour-el-houda/.ssh/felcloud_resilient}
if [[ -z ${PROBER_SSH:-} ]]; then
  PROBER_SSH="ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i $SSH_KEY -i $SANDBOX_KEY -J $BASTION_USER@$BASTION,ansible@10.20.10.24 $PROBER_USER@$PROBER_IP"
fi
GW_SB_IPS=${GW_SB_IPS:-'10.20.30.86 10.20.30.224'}
GW_MGMT_IPS=${GW_MGMT_IPS:-'10.20.10.24 10.20.10.164'}
MGMT_PORTS=${MGMT_PORTS:-22}
OTHER_SANDBOX=${OTHER_SANDBOX:-10.20.30.15}
OTHER_PORTS=${OTHER_PORTS:-'22 80'}
SG=${SG:-sg-gateway}
PAUSE=${PAUSE:-0}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "required command not found: $1"; }
b() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; [[ $PAUSE == 1 ]] && read -r -p '[enter]' || true; }
valid_ip() { [[ $1 =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; }
valid_port() { [[ $1 =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 65535 )); }
need ssh
need openstack
need awk
if [[ -n $PROBER_SSH ]]; then
  read -r -a SSH_CMD <<<"$PROBER_SSH"
else
  SSH_CMD=(ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new)
  [[ -n $SSH_KEY ]] && SSH_CMD+=(-i "$SSH_KEY")
  SSH_CMD+=(-J "$BASTION_USER@$BASTION" "$PROBER_USER@$PROBER_IP")
fi
((${#SSH_CMD[@]} > 0)) || die 'empty SSH command'

b 'Verify the sandbox prober and bastion connection before testing ports'
if ! prober=$("${SSH_CMD[@]}" 'hostname; hostname -I' 2>&1); then
  printf '%s\n' "$prober" >&2
  die 'SSH to the sandbox prober failed; port probes would be inconclusive'
fi
printf '  Prober: %s\n' "$(tr '\n' ' ' <<<"$prober")"

fails=0; probe_errors=0
probe() { # probe <host> <port> <expected-open|expected-blocked>
  local host=$1 port=$2 expected=$3 output rc got color
  if ! valid_ip "$host" || ! valid_port "$port"; then
    printf '  INVALID TARGET %s:%s\n' "$host" "$port"; ((fails+=1)); return
  fi
  # Exit 0 means TCP connected. A normal connect refusal/timeout means blocked.
  # SSH status 255 indicates transport/authentication trouble, not a network result.
  output=$("${SSH_CMD[@]}" "timeout 4 bash -c 'echo > /dev/tcp/$host/$port'" 2>&1); rc=$?
  if (( rc == 255 )); then
    got="ERROR(ssh)"; color='\033[31m'; ((probe_errors+=1)); ((fails+=1))
  elif (( rc == 0 )); then
    got=open; color='\033[32m'
  else
    got=blocked; color='\033[32m'
  fi
  if [[ $got != 'ERROR(ssh)' && ! ( $got == open && $expected == open || $got == blocked && $expected == blocked ) ]]; then
    color='\033[31m'; ((fails+=1))
  fi
  printf '  %-15s:%-5s %-12s expected %-16s %b\n' "$host" "$port" "$got" "$expected" "$color"
  [[ -z $output || $rc != 255 ]] || printf '    %s\n' "$output"
}

b 'Gateway sandbox-side ports: HTTP/HTTPS reachable, SSH blocked'
for ip in $GW_SB_IPS; do probe "$ip" 80 open; probe "$ip" 443 open; probe "$ip" 22 blocked; done

b 'Sandbox to gateway management network: SSH blocked'
for ip in $GW_MGMT_IPS; do for port in $MGMT_PORTS; do probe "$ip" "$port" blocked; done; done

b 'Sandbox to another sandbox: selected ports blocked'
for port in $OTHER_PORTS; do probe "$OTHER_SANDBOX" "$port" blocked; done

b "$SG security group rules (confirm no ingress SSH rule)"
openstack security group rule list "$SG" -c Direction -c Ethertype -c 'IP Protocol' -c 'Port Range' -c 'IP Range' || die "could not read security group $SG"
rules=$(openstack security group rule list "$SG" -f value -c Direction -c 'Port Range') || die "could not inspect $SG rules"
ssh_rules=$(awk 'tolower($1)=="ingress" && ($2=="22" || $2=="22:22" || $2 ~ /(^|[^0-9])22([^0-9]|$)/){n++} END{print n+0}' <<<"$rules")
printf '\n  ingress rules exposing port 22: %s\n  failed checks: %s (SSH probe errors: %s)\n' "$ssh_rules" "$fails" "$probe_errors"
(( ssh_rules == 0 )) || ((fails+=1))
(( fails == 0 )) || die "$fails isolation checks failed; fix topology/addresses or rules before recording"
printf '  PASS: observed connectivity matches the isolation policy.\n'
