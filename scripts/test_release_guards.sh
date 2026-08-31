#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"

cleanup() {
  rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

expect_failure() {
  local label="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    die "${label}: command unexpectedly succeeded"
  fi
}

verify_manifest() {
  local directory="$1"
  (
    cd -- "${directory}"
    sha256sum --check --strict mem-mcp-checksums.txt
  )
}

current_version="$(
  sed -nE 's/^[[:space:]]*"version": "([^"]+)",$/\1/p' \
    "${repo_root}/npm/package.json" | head -n 1
)"
current_tag="v${current_version}"
same_commit=0123456789abcdef0123456789abcdef01234567
release_workflow="${repo_root}/.github/workflows/release.yml"

manual_tag_ref="          ref: \${{ github.event_name == 'workflow_dispatch' && format('refs/tags/{0}', inputs.version) || github.ref }}"
grep -Fxq -- "${manual_tag_ref}" "${release_workflow}" ||
  die "manual releases must check out an explicit refs/tags/<version> ref"
if grep -Fq -- "ref: \${{ inputs.version || github.ref }}" "${release_workflow}"; then
  die "a short manual ref can be shadowed by a same-named branch"
fi
selected_manual_ref="refs/tags/${current_tag}"
same_named_branch_ref="refs/heads/${current_tag}"
[[ "${selected_manual_ref}" != "${same_named_branch_ref}" ]] ||
  die "same-named branch fixture unexpectedly aliases the release tag"

version_guard_line="$(
  grep -nF -- "if [[ ! \"\${version}\" =~ ^v" "${release_workflow}" | cut -d: -f1
)"
first_fetch_line="$(
  grep -nF -- 'git fetch --no-tags origin' "${release_workflow}" | head -n 1 | cut -d: -f1
)"
source_validator_line="$(
  grep -nF -- "commit=\"\$(./scripts/validate_release_source.sh" "${release_workflow}" |
    head -n 1 | cut -d: -f1
)"
((version_guard_line < first_fetch_line && first_fetch_line < source_validator_line)) ||
  die "release input must be validated before fetch and repository scripts"

[[ "$(grep -Fc -- 'gh release create' "${release_workflow}" || true)" == 1 ]] ||
  die "release workflow must contain exactly one Release creation call"
[[ "$(grep -Fc -- 'gh release edit' "${release_workflow}" || true)" == 1 ]] ||
  die "release workflow must contain exactly one Release publication call"
grep -Fq -- 'needs: [preflight, build]' "${release_workflow}" ||
  die "Release creation must depend on preflight and every build"
grep -Fq -- '--verify-tag' "${release_workflow}" ||
  die "Release creation must refuse an absent remote tag"
grep -Eq -- '^[[:space:]]+--draft([[:space:]]|$)' "${release_workflow}" ||
  die "Release assets must first upload to a draft"
grep -Fq -- '--draft=false' "${release_workflow}" ||
  die "the verified draft must be published explicitly"
grep -Fq -- '(.assets | length == 7)' "${release_workflow}" ||
  die "remote draft validation must require exactly seven assets"
grep -Fq -- '(.size > 0)' "${release_workflow}" ||
  die "remote draft validation must reject empty assets"

create_line="$(grep -nF -- 'gh release create' "${release_workflow}" | cut -d: -f1)"
draft_line="$(
  grep -nE -- '^[[:space:]]+--draft([[:space:]]|$)' "${release_workflow}" |
    cut -d: -f1
)"
verify_line="$(grep -nF -- '- name: Verify remote draft assets' "${release_workflow}" | cut -d: -f1)"
edit_line="$(grep -nF -- 'gh release edit' "${release_workflow}" | cut -d: -f1)"
((create_line < draft_line && draft_line < verify_line && verify_line < edit_line)) ||
  die "draft creation, remote asset validation, and publication are out of order"
[[ "$(sed '/^[[:space:]]*$/d' "${release_workflow}" | tail -n 1)" == \
  *'--draft=false' ]] || die "draft publication must be the workflow's final command"
if grep -Eq -- '(^|[[:space:]])git tag([[:space:]]|$)|npm publish|--generate-notes' \
  "${release_workflow}"; then
  die "release workflow must not create tags, publish npm, or synthesize notes"
fi

"${repo_root}/scripts/validate_release_version.sh" "${current_version}" >/dev/null
expect_failure "version mismatch" \
  "${repo_root}/scripts/validate_release_version.sh" 999.999.999

notes_file="${tmp_dir}/release-notes.md"
"${repo_root}/scripts/render_release_notes.sh" "${current_tag}" > "${notes_file}"
[[ -s "${notes_file}" ]] || die "release notes are empty"
grep -Fq -- '### Fixed' "${notes_file}" || die "release notes omit the current fixes"
if grep -Fq -- '## [0.1.0]' "${notes_file}"; then
  die "release notes leaked a different version section"
fi
expect_failure "missing changelog version" \
  "${repo_root}/scripts/render_release_notes.sh" v999.999.999

# Exercise Git object guards without creating or changing a real tag. The fake
# git command supplies only the read-only answers consumed by the validator.
fake_bin="${tmp_dir}/fake-bin"
mkdir -p -- "${fake_bin}"
cat > "${fake_bin}/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == -C ]]; then
  shift 2
fi

case "${1:-}" in
  cat-file)
    [[ "${FAKE_TAG_TYPE:-tag}" != missing ]] || exit 1
    printf '%s\n' "${FAKE_TAG_TYPE:-tag}"
    ;;
  rev-parse)
    if [[ "${2:-}" == HEAD ]]; then
      printf '%s\n' "${FAKE_HEAD_COMMIT:?}"
    else
      printf '%s\n' "${FAKE_TAG_COMMIT:?}"
    fi
    ;;
  show-ref)
    [[ "${FAKE_MAIN_REF_PRESENT:-yes}" == yes ]]
    ;;
  merge-base)
    [[ "${FAKE_MAIN_CONTAINS:-yes}" == yes ]]
    ;;
  *)
    printf 'unexpected fake git command: %s\n' "$*" >&2
    exit 2
    ;;
esac
EOF
chmod +x "${fake_bin}/git"

validated_commit="$(
  env \
    PATH="${fake_bin}:${PATH}" \
    FAKE_TAG_TYPE=tag \
    FAKE_TAG_COMMIT="${same_commit}" \
    FAKE_HEAD_COMMIT="${same_commit}" \
    "${repo_root}/scripts/validate_release_source.sh" "${current_tag}"
)"
[[ "${validated_commit}" == "${same_commit}" ]] ||
  die "annotated tag did not return its exact commit"

expect_failure "missing tag" env \
  PATH="${fake_bin}:${PATH}" \
  FAKE_TAG_TYPE=missing \
  FAKE_TAG_COMMIT="${same_commit}" \
  FAKE_HEAD_COMMIT="${same_commit}" \
  "${repo_root}/scripts/validate_release_source.sh" "${current_tag}"
expect_failure "lightweight tag" env \
  PATH="${fake_bin}:${PATH}" \
  FAKE_TAG_TYPE=commit \
  FAKE_TAG_COMMIT="${same_commit}" \
  FAKE_HEAD_COMMIT="${same_commit}" \
  "${repo_root}/scripts/validate_release_source.sh" "${current_tag}"
expect_failure "tag and checkout mismatch" env \
  PATH="${fake_bin}:${PATH}" \
  FAKE_TAG_TYPE=tag \
  FAKE_TAG_COMMIT="${same_commit}" \
  FAKE_HEAD_COMMIT=1111111111111111111111111111111111111111 \
  "${repo_root}/scripts/validate_release_source.sh" "${current_tag}"
expect_failure "tag not on main" env \
  PATH="${fake_bin}:${PATH}" \
  FAKE_TAG_TYPE=tag \
  FAKE_TAG_COMMIT="${same_commit}" \
  FAKE_HEAD_COMMIT="${same_commit}" \
  FAKE_MAIN_CONTAINS=no \
  "${repo_root}/scripts/validate_release_source.sh" "${current_tag}"
expect_failure "annotated tag version mismatch" env \
  PATH="${fake_bin}:${PATH}" \
  FAKE_TAG_TYPE=tag \
  FAKE_TAG_COMMIT="${same_commit}" \
  FAKE_HEAD_COMMIT="${same_commit}" \
  "${repo_root}/scripts/validate_release_source.sh" v999.999.999

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
  printf 'test payload for %s\n' "${asset}" > "${asset_dir}/${asset}"
done
"${repo_root}/scripts/generate_release_checksums.sh" \
  "${current_tag}" "${same_commit}" "${asset_dir}" >/dev/null

manifest="${asset_dir}/mem-mcp-checksums.txt"
[[ "$(wc -l < "${manifest}")" == 6 ]] || die "checksum manifest must have six rows"
(
  cd -- "${asset_dir}"
  sha256sum --check --strict "$(basename -- "${manifest}")" >/dev/null
)

printf 'tampered\n' >> "${asset_dir}/${assets[0]}"
expect_failure "tampered asset" verify_manifest "${asset_dir}"

rm -f -- "${manifest}" "${asset_dir}/${assets[0]}"
expect_failure "missing asset" \
  "${repo_root}/scripts/generate_release_checksums.sh" \
  "${current_tag}" "${same_commit}" "${asset_dir}"

printf 'test payload\n' > "${asset_dir}/${assets[0]}"
printf 'unexpected\n' > "${asset_dir}/mem-mcp-unexpected"
expect_failure "extra asset" \
  "${repo_root}/scripts/generate_release_checksums.sh" \
  "${current_tag}" "${same_commit}" "${asset_dir}"

rm -f -- "${asset_dir}/mem-mcp-unexpected"
: > "${asset_dir}/${assets[0]}"
expect_failure "empty asset" \
  "${repo_root}/scripts/generate_release_checksums.sh" \
  "${current_tag}" "${same_commit}" "${asset_dir}"

rm -f -- "${asset_dir}/${assets[0]}"
ln -s -- "${assets[1]}" "${asset_dir}/${assets[0]}"
expect_failure "symlink asset" \
  "${repo_root}/scripts/generate_release_checksums.sh" \
  "${current_tag}" "${same_commit}" "${asset_dir}"

printf 'PASS: release source, notes, asset-set and checksum guards fail closed\n'
