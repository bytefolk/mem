#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
generator="${1:-${repo_root}/scripts/generate_release_checksums.sh}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/mem-checksum-output.XXXXXX")"
trap 'rm -rf -- "${tmp_dir}"' EXIT

die() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assets=(
  mem-mcp-darwin-amd64
  mem-mcp-darwin-arm64
  mem-mcp-linux-amd64
  mem-mcp-linux-arm64
  mem-mcp-windows-amd64.exe
  mem-mcp-windows-arm64.exe
)
commit=1111111111111111111111111111111111111111

prepare() {
  case_root="${tmp_dir}/${1} with spaces"
  asset_dir="${case_root}/assets with spaces"
  outside="${case_root}/outside with spaces"
  mkdir -p -- "${asset_dir}" "${outside}"
  for asset in "${assets[@]}"; do
    printf 'test payload for %s\n' "${asset}" > "${asset_dir}/${asset}"
  done
  printf 'external data must remain unchanged\n' > "${outside}/keep.txt"
  output="${asset_dir}/mem-mcp-checksums.txt"
}

snapshot_files() {
  find "${case_root}" -type f -exec sha256sum {} \; | LC_ALL=C sort
}

for kind in symlink-directory symlink-file dangling-symlink directory regular-file; do
  prepare "${kind}"
  case "${kind}" in
    symlink-directory) ln -s -- "${outside}" "${output}" ;;
    symlink-file) ln -s -- "${outside}/keep.txt" "${output}" ;;
    dangling-symlink) ln -s -- "${outside}/not-created.txt" "${output}" ;;
    directory) mkdir -- "${output}" ;;
    regular-file) printf 'existing manifest must remain unchanged\n' > "${output}" ;;
  esac
  before="$(snapshot_files)"
  status=0
  bash "${generator}" v0.1.1 "${commit}" "${asset_dir}" > "${tmp_dir}/result.log" 2>&1 || status=$?
  # Check side effects before status: the original directory-symlink bug may
  # return failure only after mv has already written outside the asset tree.
  [[ "$(snapshot_files)" == "${before}" ]] ||
    die "${kind}: output publication changed existing data or created an unexpected file"
  [[ "${status}" -ne 0 ]] || die "${kind}: existing output was accepted"
  grep -Fq 'checksum output path already exists' "${tmp_dir}/result.log" ||
    die "${kind}: missing output-path diagnostic"
  case "${kind}" in
    symlink-directory) [[ -L "${output}" && "$(readlink "${output}")" == "${outside}" ]] ;;
    symlink-file) [[ -L "${output}" && "$(readlink "${output}")" == "${outside}/keep.txt" ]] ;;
    dangling-symlink) [[ -L "${output}" && "$(readlink "${output}")" == "${outside}/not-created.txt" ]] ;;
    directory) [[ -d "${output}" && ! -L "${output}" ]] ;;
    regular-file) [[ -f "${output}" && ! -L "${output}" ]] ;;
  esac || die "${kind}: existing output path was replaced"
  printf 'PASS: %s rejected without changing external data or output path\n' "${kind}"
done

# Simulate an output symlink appearing while checksum generation is in progress.
# The second absence check must reject it and cleanup only our own staging file.
prepare output-created-during-hashing
fake_bin="${case_root}/hash tools"
mkdir -p -- "${fake_bin}"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' \
  "\"\${REAL_SHA256SUM}\" \"\$@\"" \
  "ln -s -- \"\${OUTPUT_TARGET}\" \"\${OUTPUT_MANIFEST}\"" > "${fake_bin}/sha256sum"
chmod +x "${fake_bin}/sha256sum"
before="$(snapshot_files)"
status=0
REAL_SHA256SUM="$(command -v sha256sum)" OUTPUT_TARGET="${outside}" OUTPUT_MANIFEST="${output}" \
  PATH="${fake_bin}:${PATH}" bash "${generator}" v0.1.1 "${commit}" "${asset_dir}" \
  > "${tmp_dir}/result.log" 2>&1 || status=$?
[[ "${status}" -ne 0 ]] || die 'late output symlink was accepted'
[[ "$(snapshot_files)" == "${before}" ]] || die 'late output symlink leaked staging data or changed external files'
[[ -L "${output}" && "$(readlink "${output}")" == "${outside}" ]] || die 'late output symlink was replaced'
grep -Fq 'checksum output path already exists' "${tmp_dir}/result.log" || die 'late output symlink lacks diagnostic'
printf 'PASS: late output symlink rejected and private staging file cleaned up\n'

# The mktemp template is not a predictable staging filename. A pre-existing
# template-shaped symlink must remain untouched while a fresh manifest works.
prepare unique-temporary-file
ln -s -- "${outside}/keep.txt" "${asset_dir}/.mem-mcp-checksums.XXXXXX"
before="$(sha256sum "${outside}/keep.txt")"
bash "${generator}" v0.1.1 "${commit}" "${asset_dir}" >/dev/null
[[ "$(sha256sum "${outside}/keep.txt")" == "${before}" ]] || die 'temporary output overwrote external data'
[[ -L "${asset_dir}/.mem-mcp-checksums.XXXXXX" ]] || die 'temporary symlink was replaced'
[[ -f "${output}" && ! -L "${output}" ]] || die 'fresh manifest was not created'
(
  cd -- "${asset_dir}"
  sha256sum --check --strict mem-mcp-checksums.txt >/dev/null
)
printf 'PASS: unpredictable temporary output preserves a pre-existing template-shaped symlink\n'
