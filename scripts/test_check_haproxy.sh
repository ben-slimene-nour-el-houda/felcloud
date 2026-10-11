#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE="$ROOT_DIR/ansible/roles/keepalived/templates/check_haproxy.sh.j2"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

render_script() {
  python3 - "$TEMPLATE" "$TMPDIR/rendered.sh" <<'PY'
import sys
from pathlib import Path
import jinja2

template_path = Path(sys.argv[1])
out_path = Path(sys.argv[2])
source = template_path.read_text(encoding='utf-8')
rendered = jinja2.Template(source).render()
out_path.write_text(rendered, encoding='utf-8')
PY
  chmod +x "$TMPDIR/rendered.sh"
}

make_fake_systemctl() {
  cat > "$TMPDIR/systemctl" <<'SH'
#!/usr/bin/env bash
if [[ "$*" == *"is-active"* ]]; then
  exit 0
fi
exit 0
SH
  chmod +x "$TMPDIR/systemctl"
}

make_fake_curl() {
  cat > "$TMPDIR/curl" <<'SH'
#!/usr/bin/env bash
printf '200'
SH
  chmod +x "$TMPDIR/curl"
}

run_case() {
  local name="$1"
  local stat_file="$2"
  local expected="$3"
  local mode="$4"
  render_script
  make_fake_systemctl
  make_fake_curl
  cat > "$TMPDIR/python3" <<'SH'
#!/usr/bin/env bash
# shim for the template's embedded python execution path
exec /usr/bin/env python3 "$@"
SH
  chmod +x "$TMPDIR/python3"
  export PATH="$TMPDIR:$PATH"
  export HAPROXY_STAT_FILE="$stat_file"
  if [[ "$mode" == "missing" ]]; then
    rm -f "$stat_file"
  fi
  set +e
  "$TMPDIR/rendered.sh"
  code=$?
  set -e
  if [[ "$code" -ne "$expected" ]]; then
    echo "case ${name} expected exit ${expected}, got ${code}" >&2
    exit 1
  fi
}

# Case 1: no backends => healthy
cat > "$TMPDIR/no_backends.csv" <<'CSV'
# pxname,svname,status
FRONTEND,FRONTEND,OPEN
BACKEND,BACKEND,OPEN
CSV
run_case no_backends "$TMPDIR/no_backends.csv" 0 csv

# Case 2: all backends are DOWN => unhealthy
cat > "$TMPDIR/all_down.csv" <<'CSV'
# pxname,svname,status
team1,web1,DOWN
team1,web2,DOWN
CSV
run_case all_down "$TMPDIR/all_down.csv" 1 csv

# Case 3: mixed backends => healthy
cat > "$TMPDIR/mixed.csv" <<'CSV'
# pxname,svname,status
team1,web1,DOWN
team1,web2,UP
CSV
run_case mixed "$TMPDIR/mixed.csv" 0 csv

# Case 4: missing socket => healthy
run_case missing_socket "$TMPDIR/does-not-exist.sock" 0 missing

echo "check_haproxy template validation OK"
