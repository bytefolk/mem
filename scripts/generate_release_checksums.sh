#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
commit="${2:-}"
asset_dir="${3:-}"
mcp_output="${asset_dir}/mem-mcp-checksums.txt"
server_output="${asset_dir}/mem-checksums.txt"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_absent_output() {
  # -e alone misses dangling symlinks. In particular, mv follows an output
  # symlink to a directory and would publish outside this asset directory.
  local output="$1"
  [[ ! -e "${output}" && ! -L "${output}" ]] ||
    die "checksum output path already exists: ${output}"
}

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "invalid release tag: ${tag:-<empty>}"
fi
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || die "invalid release commit: ${commit:-<empty>}"
[[ -d "${asset_dir}" ]] || die "asset directory does not exist: ${asset_dir:-<empty>}"
require_absent_output "${mcp_output}"
require_absent_output "${server_output}"

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

expected_sorted="$(printf '%s\n' "${all_assets[@]}" | LC_ALL=C sort)"
actual_sorted="$(printf '%s\n' "${actual_assets[@]}" | LC_ALL=C sort)"
if [[ "${#actual_assets[@]}" -eq 0 ]] || [[ "${actual_sorted}" != "${expected_sorted}" ]]; then
  printf 'ERROR: release assets differ from the exact expected set\n' >&2
  printf 'expected:\n%s\n' "${expected_sorted}" >&2
  printf 'actual:   %s\n' "${actual_assets[*]:-<none>}" >&2
  exit 1
fi

for asset in "${all_assets[@]}"; do
  [[ -f "${asset_dir}/${asset}" && ! -L "${asset_dir}/${asset}" ]] ||
    die "release asset is not a regular file: ${asset}"
  [[ -s "${asset_dir}/${asset}" ]] || die "release asset is empty: ${asset}"
done

generate_checksums() {
  local output="$1"
  shift
  local assets=("$@")
  local tmp_output
  tmp_output="$(mktemp "${asset_dir}/.$(basename -- "${output}").XXXXXX")"
  cleanup_tmp() {
    rm -f -- "${tmp_output}"
  }
  trap cleanup_tmp EXIT
  {
    (
      cd -- "${asset_dir}"
      sha256sum "${assets[@]}"
    )
  } > "${tmp_output}"
  # The post-publish self-check below uses --ignore-missing, which by definition
  # tolerates a listed file being absent, so completeness is asserted here while
  # the staging file and the expected set are both known.
  local manifest_rows
  manifest_rows="$(grep -c '' "${tmp_output}")"
  [[ "${manifest_rows}" -eq "${#assets[@]}" ]] ||
    die "checksum manifest must have ${#assets[@]} rows, got ${manifest_rows}"
  require_absent_output "${output}"
  mv -- "${tmp_output}" "${output}"
  trap - EXIT
}

generate_checksums "${mcp_output}" "${mcp_assets[@]}"
generate_checksums "${server_output}" "${server_assets[@]}"

(
  cd -- "${asset_dir}"
  sha256sum --check --strict --ignore-missing "$(basename -- "${mcp_output}")"
  sha256sum --check --strict --ignore-missing "$(basename -- "${server_output}")"
)

printf 'PASS: checksums bind %d release assets to %s at %s\n' \
  "${#all_assets[@]}" "${tag}" "${commit}"
