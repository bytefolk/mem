#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
commit="${2:-}"
asset_dir="${3:-}"
output="${asset_dir}/mem-mcp-checksums.txt"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "invalid release tag: ${tag:-<empty>}"
fi
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || die "invalid release commit: ${commit:-<empty>}"
[[ -d "${asset_dir}" ]] || die "asset directory does not exist: ${asset_dir:-<empty>}"

assets=(
  mem-mcp-darwin-amd64
  mem-mcp-darwin-arm64
  mem-mcp-linux-amd64
  mem-mcp-linux-arm64
  mem-mcp-windows-amd64.exe
  mem-mcp-windows-arm64.exe
)

actual_assets=()
while IFS= read -r actual_asset; do
  actual_assets[${#actual_assets[@]}]="${actual_asset}"
done < <(
  find "${asset_dir}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort
)
if [[ "${actual_assets[*]}" != "${assets[*]}" ]]; then
  printf 'ERROR: release assets differ from the exact expected set\n' >&2
  printf 'expected: %s\n' "${assets[*]}" >&2
  printf 'actual:   %s\n' "${actual_assets[*]:-<none>}" >&2
  exit 1
fi

for asset in "${assets[@]}"; do
  [[ -f "${asset_dir}/${asset}" && ! -L "${asset_dir}/${asset}" ]] ||
    die "release asset is not a regular file: ${asset}"
  [[ -s "${asset_dir}/${asset}" ]] || die "release asset is empty: ${asset}"
done

tmp_output="$(mktemp "${asset_dir}/.mem-mcp-checksums.XXXXXX")"
cleanup() {
  rm -f -- "${tmp_output}"
}
trap cleanup EXIT

{
  (
    cd -- "${asset_dir}"
    sha256sum "${assets[@]}"
  )
} > "${tmp_output}"
mv -- "${tmp_output}" "${output}"
trap - EXIT

(
  cd -- "${asset_dir}"
  sha256sum --check --strict --ignore-missing "$(basename -- "${output}")"
)

printf 'PASS: checksums bind six release assets to %s at %s\n' "${tag}" "${commit}"
