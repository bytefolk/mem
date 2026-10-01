#!/usr/bin/env sh
# Exercise the native template's startup script on the actual unprivileged image.
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
image=${MEM_SEALOS_WEB_TEST_IMAGE:-mem-web:deploy-validation}
work=$(mktemp -d)
container="mem-sealos-web-startup-$$"
started=0
cleanup() {
  if [ "$started" = 1 ]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

# Read the script that the ConfigMap actually mounts; no duplicate test copy.
python3 - "$repo_root/deploy/sealos/template.yaml" "$work/start-web.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text(encoding="utf-8")
key = "  vn-etcvn-memvn-startvn-webvn-sh: |\n"
if text.count(key) != 1:
    raise SystemExit("expected one native Web startup script")
block = text.split(key, 1)[1].split("\n---\n", 1)[0]
lines = block.splitlines()
if any(line and not line.startswith("    ") for line in lines):
    raise SystemExit("unexpected startup block indentation")
Path(sys.argv[2]).write_text("\n".join(line[4:] if line else "" for line in lines) + "\n", encoding="utf-8")
PY
chmod 644 "$work/start-web.sh"

docker run --detach --rm --name "$container" \
  --label com.bytefolk.mem.test=sealos-web-startup \
  --read-only --user 101:101 --cap-drop ALL --security-opt no-new-privileges \
  --memory 256m --cpus 0.2 \
  --tmpfs /tmp:rw,uid=101,gid=101,mode=1777 \
  --tmpfs /var/cache/nginx:rw,uid=101,gid=101,mode=1777 \
  --volume "$work/start-web.sh:/etc/mem/start-web.sh:ro" \
  --env MEMD_UPSTREAM=http://127.0.0.1:18081 --env MEM_MAX_BODY_SIZE=64m \
  --publish 127.0.0.1::8080 --entrypoint /bin/sh \
  "$image" /etc/mem/start-web.sh >/dev/null
started=1
address=$(docker port "$container" 8080/tcp)
case "$address" in 127.0.0.1:*) ;; *) echo "unexpected test bind" >&2; exit 1 ;; esac
attempt=0
until curl --fail --silent "http://$address/healthz" >"$work/health"; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    docker logs "$container" >&2 || true
    echo "native Sealos Web startup did not become healthy" >&2
    exit 1
  fi
  sleep 1
done
grep -qx 'ok' "$work/health"
curl --fail --silent "http://$address/login" >"$work/login.html"
grep -q '<script' "$work/login.html"
docker exec "$container" nginx -t -c /tmp/nginx.conf
docker exec "$container" cat /tmp/default.conf >"$work/generated.conf"
python3 - "$work/generated.conf" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text(encoding="utf-8")
assert "proxy_pass http://127.0.0.1:18081;" in text, "runtime upstream did not render"
assert "client_max_body_size 64m;" in text, "runtime upload limit did not render"
assert "${{" not in text and "${MEMD_UPSTREAM}" not in text, "unresolved template expression"
for variable in ("$uri", "$host", "$remote_addr", "$proxy_add_x_forwarded_for", "$mem_auth_loggable"):
    assert variable in text, f"nginx variable was substituted: {variable}"
assert "/v1/auth/github/callback 0;" in text, "OAuth access-log filter missing"
PY
echo "PASS: native Sealos Web startup under UID 101, read-only root and runtime rendering"
