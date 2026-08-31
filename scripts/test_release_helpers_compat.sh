#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
version_validator="${repo_root}/scripts/validate_release_version.sh"
checksum_generator="${repo_root}/scripts/generate_release_checksums.sh"
tmp_dir="$(mktemp -d)"

cleanup() {
  rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

current_version="$(
  sed -nE 's/^[[:space:]]*"version": "([^"]+)",$/\1/p' \
    "${repo_root}/npm/package.json" | head -n 1
)"

if ! (
  enable -n mapfile 2>/dev/null || :
  enable -n readarray 2>/dev/null || :
  # The absolute path is derived from this checked-in script's directory.
  # shellcheck disable=SC1090
  source "${version_validator}" "${current_version}" >/dev/null
); then
  printf 'ERROR: release version validator requires Bash 4-only collection builtins\n' >&2
  exit 1
fi

asset_dir="${tmp_dir}/assets"
mkdir -p -- "${asset_dir}"
assets=(
  mem-mcp-darwin-amd64
  mem-mcp-darwin-arm64
  mem-mcp-linux-amd64
  mem-mcp-linux-arm64
  mem-mcp-windows-amd64.exe
  mem-mcp-windows-arm64.exe
)
for asset in "${assets[@]}"; do
  printf 'compatibility fixture for %s\n' "${asset}" > "${asset_dir}/${asset}"
done

if ! (
  enable -n mapfile 2>/dev/null || :
  enable -n readarray 2>/dev/null || :

  # The checksum helper is sourced to exercise its control flow under the
  # current shell. These deterministic replacements keep this Bash-version
  # regression independent of GNU find/coreutils on stock macOS.
  # shellcheck disable=SC2317,SC2329
  find() {
    printf '%s\n' "${assets[@]}"
  }

  # shellcheck disable=SC2317,SC2329
  sha256sum() {
    if [[ "${1:-}" == --check ]]; then
      return 0
    fi
    local asset
    for asset in "$@"; do
      printf '%064d  %s\n' 0 "${asset}"
    done
  }

  # The absolute path is derived from this checked-in script's directory.
  # shellcheck disable=SC1090
  source "${checksum_generator}" \
    "v${current_version}" \
    0123456789abcdef0123456789abcdef01234567 \
    "${asset_dir}" >/dev/null
); then
  printf 'ERROR: release checksum generator requires Bash 4-only collection builtins\n' >&2
  exit 1
fi

[[ "$(wc -l < "${asset_dir}/mem-mcp-checksums.txt")" == 6 ]]

printf 'PASS: release version and checksum helpers run without Bash 4-only collection builtins\n'
