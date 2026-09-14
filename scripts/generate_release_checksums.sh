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

require_absent_output() {
  # -e alone misses dangling symlinks. In particular, mv follows an output
  # symlink to a directory and would publish outside this asset directory.
  [[ ! -e "${output}" && ! -L "${output}" ]] ||
    die "checksum output path already exists: ${output}"
}

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "invalid release tag: ${tag:-<empty>}"
fi
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || die "invalid release commit: ${commit:-<empty>}"
[[ -d "${asset_dir}" ]] || die "asset directory does not exist: ${asset_dir:-<empty>}"
require_absent_output

assets=(
  mem-mcp-darwin-amd64
  mem-mcp-darwin-arm64
  mem-mcp-linux-amd64
  mem-mcp-linux-arm64
  mem-mcp-windows-amd64.exe
  mem-mcp-windows-arm64.exe
)

# A process substitution hides find's exit status from the loop below, so a
# tool failure would surface as the misleading "differ from the exact expected
# set". Capture the listing first and report a failed listing as what it is.
asset_listing="$(
  find "${asset_dir}" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort
)" || die "cannot list release assets in ${asset_dir}"

actual_assets=()
while IFS= read -r actual_asset; do
  [[ -n "${actual_asset}" ]] || continue
  actual_assets[${#actual_assets[@]}]="${actual_asset}"
done <<< "${asset_listing}"
if [[ "${#actual_assets[@]}" -eq 0 ]] || [[ "${actual_assets[*]}" != "${assets[*]}" ]]; then
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
# The post-publish self-check below uses --ignore-missing, which by definition
# tolerates a listed file being absent, so completeness is asserted here while
# the staging file and the expected set are both known.
manifest_rows="$(grep -c '' "${tmp_output}")"
[[ "${manifest_rows}" -eq "${#assets[@]}" ]] ||
  die "checksum manifest must have ${#assets[@]} rows, got ${manifest_rows}"
require_absent_output
mv -- "${tmp_output}" "${output}"
trap - EXIT

(
  cd -- "${asset_dir}"
  sha256sum --check --strict --ignore-missing "$(basename -- "${output}")"
)

printf 'PASS: checksums bind six release assets to %s at %s\n' "${tag}" "${commit}"
