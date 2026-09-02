#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
commit="${2:-}"
asset_dir="${3:-}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "invalid release tag: ${tag:-<empty>}"
fi
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || die "invalid release commit: ${commit:-<empty>}"
[[ -d "${asset_dir}" ]] || die "asset directory does not exist: ${asset_dir:-<empty>}"

mcp_assets=(
  mem-mcp-darwin-amd64
  mem-mcp-darwin-arm64
  mem-mcp-linux-amd64
  mem-mcp-linux-arm64
  mem-mcp-windows-amd64.exe
  mem-mcp-windows-arm64.exe
)

server_assets=(
  memd-darwin-amd64
  memd-darwin-arm64
  memd-linux-amd64
  memd-linux-arm64
  mem-migrate-darwin-amd64
  mem-migrate-darwin-arm64
  mem-migrate-linux-amd64
  mem-migrate-linux-arm64
  mem-healthcheck-darwin-amd64
  mem-healthcheck-darwin-arm64
  mem-healthcheck-linux-amd64
  mem-healthcheck-linux-arm64
  mem-darwin-amd64
  mem-darwin-arm64
  mem-linux-amd64
  mem-linux-arm64
)

all_assets=()
for a in "${mcp_assets[@]}" "${server_assets[@]}"; do
  all_assets[${#all_assets[@]}]="${a}"
done

actual_assets=()
while IFS= read -r actual_asset; do
  actual_assets[${#actual_assets[@]}]="${actual_asset}"
done < <(
  find "${asset_dir}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort
)
expected_sorted="$(printf '%s\n' "${all_assets[@]}" | LC_ALL=C sort)"
actual_sorted="$(printf '%s\n' "${actual_assets[@]}" | LC_ALL=C sort)"
if [[ "${actual_sorted}" != "${expected_sorted}" ]]; then
  printf 'ERROR: release assets differ from the exact expected set\n' >&2
  printf 'expected:\n%s\n' "${expected_sorted}" >&2
  printf 'actual:\n%s\n' "${actual_sorted:-<none>}" >&2
  exit 1
fi

for asset in "${all_assets[@]}"; do
  [[ -f "${asset_dir}/${asset}" && ! -L "${asset_dir}/${asset}" ]] ||
    die "release asset is not a regular file: ${asset}"
  [[ -s "${asset_dir}/${asset}" ]] || die "release asset is empty: ${asset}"
done

generate_checksums() {
  local output_name="$1"
  shift
  local assets=("$@")
  local tmp_output
  tmp_output="$(mktemp "${asset_dir}/.${output_name}.XXXXXX")"
  (
    cd -- "${asset_dir}"
    sha256sum "${assets[@]}"
  ) > "${tmp_output}"
  mv -- "${tmp_output}" "${asset_dir}/${output_name}"
}

generate_checksums "mem-mcp-checksums.txt" "${mcp_assets[@]}"
generate_checksums "mem-checksums.txt" "${server_assets[@]}"

(
  cd -- "${asset_dir}"
  sha256sum --check --strict mem-mcp-checksums.txt
  sha256sum --check --strict mem-checksums.txt
)

printf 'PASS: checksums bind %d release assets to %s at %s\n' \
  "${#all_assets[@]}" "${tag}" "${commit}"
