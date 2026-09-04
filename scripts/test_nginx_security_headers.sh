#!/usr/bin/env bash
# Security response-header contract for the web reverse proxy.
#
# nginx is the single authority for X-Content-Type-Options, X-Frame-Options and
# Referrer-Policy; the API owns Content-Security-Policy, X-XSS-Protection and
# Content-Disposition. This runs a real nginx because both failure modes are
# runtime semantics that reading the config cannot prove: add_header in a nested
# block silently voids the inherited set, and an upstream header survives the
# proxy unless it is explicitly hidden.
#
# Usage: scripts/test_nginx_security_headers.sh [path-to-nginx-binary]
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
nginx_bin=${1:-${NGINX_BIN:-}}

if [[ -n "$nginx_bin" && ! -x "$nginx_bin" ]]; then
  # An explicitly requested binary that is not there must not become a green
  # run: a caller that pins NGINX_BIN is asserting that the check executed.
  printf 'ERROR: requested nginx binary is not executable: %s\n' "$nginx_bin" >&2
  exit 1
fi
if [[ -z "$nginx_bin" ]]; then
  nginx_bin=$(command -v nginx || true)
fi
if [[ -z "$nginx_bin" ]]; then
  printf 'SKIP: no nginx binary found (pass one as $1 or set NGINX_BIN)\n'
  exit 0
fi
for tool in curl envsubst python3; do
  command -v "$tool" >/dev/null 2>&1 || {
    printf 'ERROR: %s is required to drive the proxy\n' "$tool" >&2
    exit 1
  }
done

port=${PORT:-18080}
upstream_port=${UPSTREAM_PORT:-18081}
# 3 proxy-owned headers on five surfaces, + the 3 API-owned headers required on
# /v1/ and the 2 required absent on the other four, + the not-found status guard,
# + the /assets/ Cache-Control guard.
expected_checks=28
work=$(mktemp -d)
upstream_pid=''
nginx_pid=''
failures=0
checks=0

cleanup() {
  [[ -n "$nginx_pid" ]] && kill "$nginx_pid" 2>/dev/null || true
  [[ -n "$upstream_pid" ]] && kill "$upstream_pid" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

fail() { printf '  not ok - %s\n' "$*" >&2; failures=$((failures + 1)); checks=$((checks + 1)); }
pass() { printf '  ok - %s\n' "$*"; checks=$((checks + 1)); }

# --- render the shipped template the way the container entrypoint does ---
prefix=$work
mkdir -p "$prefix/conf" "$prefix/logs" "$prefix/run" "$prefix/html/assets" \
  "$prefix/tmp/client" "$prefix/tmp/proxy" "$prefix/tmp/fastcgi" \
  "$prefix/tmp/uwsgi" "$prefix/tmp/scgi"
printf 'console.log(1)\n' >"$prefix/html/assets/app.js"
printf '<html>mem</html>\n' >"$prefix/html/index.html"

export MEMD_UPSTREAM="http://127.0.0.1:${upstream_port}"
export MEM_MAX_BODY_SIZE=256m
export NGINX_ENVSUBST_FILTER='^(MEMD_UPSTREAM|MEM_MAX_BODY_SIZE)$'
rendered=$work/default.conf
envsubst '$MEMD_UPSTREAM $MEM_MAX_BODY_SIZE' \
  <"$repo_root/web/nginx/default.conf.template" >"$rendered"
if grep -Eq '\$\{[A-Z_]+\}' "$rendered"; then
  printf 'the template left a variable unexpanded:\n' >&2
  grep -Eo '\$\{[A-Z_]+\}' "$rendered" | sort -u >&2
  exit 1
fi

mime_candidates=(
  "$(cd -- "$(dirname -- "$nginx_bin")/.." && pwd)/conf/mime.types"
  /etc/nginx/mime.types
)
mime_types=''
for candidate in "${mime_candidates[@]}"; do
  if [[ -f "$candidate" ]]; then mime_types=$candidate; break; fi
done
if [[ -z "$mime_types" ]]; then
  printf 'no mime.types found near %s\n' "$nginx_bin" >&2
  exit 1
fi

python3 - "$rendered" "$work/conf/nginx.conf" "$prefix" "$port" "$mime_types" <<'PY'
import sys

conf_path, out_path, prefix, port, mime_types = sys.argv[1:6]
with open(conf_path, encoding="utf-8") as handle:
    server = handle.read()
server = server.replace("listen 8080;", "listen 127.0.0.1:%s;" % port)
server = server.replace("root /usr/share/nginx/html;", "root %s/html;" % prefix)
if "listen 127.0.0.1:%s;" % port not in server or ("%s/html" % prefix) not in server:
    raise SystemExit("the shipped server block drifted: could not rebind it for the harness")

with open(out_path, "w", encoding="utf-8") as handle:
    handle.write("""
worker_processes 1;
error_log {prefix}/logs/error.log warn;
pid {prefix}/run/nginx.pid;
daemon off;

events {{ worker_connections 64; }}

http {{
    include {mime_types};
    default_type application/octet-stream;
    access_log {prefix}/logs/access.log;
    client_body_temp_path {prefix}/tmp/client;
    proxy_temp_path {prefix}/tmp/proxy;
    fastcgi_temp_path {prefix}/tmp/fastcgi;
    uwsgi_temp_path {prefix}/tmp/uwsgi;
    scgi_temp_path {prefix}/tmp/scgi;

{server}
}}
""".format(prefix=prefix, server=server.rstrip(), mime_types=mime_types))
PY

# --- fake upstream answering exactly like the Go API middleware does ---
cat >"$work/upstream.py" <<'PY'
import http.server
import os

# Mirrors securityHeadersMiddleware (server/internal/api/util.go) plus the
# per-response Content-Disposition that the download handlers set. If the API's
# header set changes, this fixture must change with it.
class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):  # noqa: N802
        body = b'{"ok":true}'
        self.send_response(200)
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("Content-Security-Policy", "default-src 'none'")
        self.send_header("X-XSS-Protection", "0")
        self.send_header("Content-Disposition", 'attachment; filename="note.txt"')
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass

http.server.HTTPServer(("127.0.0.1", int(os.environ["UPSTREAM_PORT"])), Handler).serve_forever()
PY
UPSTREAM_PORT=$upstream_port python3 "$work/upstream.py" &
upstream_pid=$!

"$nginx_bin" -p "$prefix" -c "$work/conf/nginx.conf" &
nginx_pid=$!

ready=''
for _ in $(seq 1 50); do
  if curl -fsS -o /dev/null "http://127.0.0.1:${port}/v1/ping" 2>/dev/null; then ready=yes; break; fi
  sleep 0.2
done
if [[ -z "$ready" ]]; then
  printf 'nginx never became ready; error log:\n' >&2
  cat "$prefix/logs/error.log" >&2 || true
  exit 1
fi

header_values() {
  # No -f: a 404 or a 502 has to be probeable too, because `always` is what
  # keeps these headers on an error response.
  curl -sS -D - -o /dev/null "$1" | tr -d '\r' |
    awk -v h="$2" '
      /^$/ { exit }
      tolower($0) ~ "^" tolower(h) ":" { sub(/^[^:]*:[ \t]?/, ""); print }'
}

check_single() {
  local url=$1 header=$2 want=$3 got count
  got=$(header_values "$url" "$header")
  count=$(printf '%s' "$got" | grep -c . || true)
  if [[ $count -ne 1 ]]; then
    fail "${url##*/}: $header appears $count times, want exactly 1 ($(printf '%s' "$got" | tr '\n' '|'))"
  elif [[ $got != "$want" ]]; then
    fail "${url##*/}: $header = $got, want $want"
  else
    pass "${url##*/}: $header: $got"
  fi
}

check_absent() {
  local url=$1 header=$2 count
  count=$(header_values "$url" "$header" | grep -c . || true)
  if [[ $count -ne 0 ]]; then
    fail "${url##*/}: $header present, want absent (nginx must not set what the API owns)"
  else
    pass "${url##*/}: $header absent"
  fi
}

for path in /index.html /assets/app.js /assets/does-not-exist.js /v1/ping /healthz; do
  printf '\n== %s ==\n' "$path"
  url="http://127.0.0.1:${port}${path}"
  check_single "$url" X-Content-Type-Options nosniff
  check_single "$url" X-Frame-Options DENY
  check_single "$url" Referrer-Policy no-referrer
  if [[ $path == /v1/* ]]; then
    check_single "$url" Content-Security-Policy "default-src 'none'"
    check_single "$url" X-XSS-Protection 0
    check_single "$url" Content-Disposition 'attachment; filename="note.txt"'
  else
    check_absent "$url" Content-Security-Policy
    check_absent "$url" Content-Disposition
  fi
done

printf '\n== the not-found surface really was a not-found ==\n'
status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/assets/does-not-exist.js")
if [[ $status == 404 ]]; then
  pass "/assets/does-not-exist.js: HTTP $status"
else
  fail "/assets/does-not-exist.js: HTTP $status, want 404 -- the header assertions above would then be measuring a success response, not the always flag"
fi

printf '\n== /assets/ caching is not collateral damage ==\n'
got=$(header_values "http://127.0.0.1:${port}/assets/app.js" Cache-Control)
if [[ $got == *immutable* ]]; then
  pass "/assets/app.js: Cache-Control: $got"
else
  fail "/assets/app.js: Cache-Control = ${got:-<empty>}, want it to still say immutable"
fi

printf '\n'
if [[ $checks -ne $expected_checks ]]; then
  printf '%d security-header assertions ran, expected %d\n' "$checks" "$expected_checks" >&2
  exit 1
fi
if [[ $failures -ne 0 ]]; then
  printf '%d of %d security-header assertions failed\n' "$failures" "$checks" >&2
  exit 1
fi
printf 'all %d security-header assertions passed\n' "$checks"
