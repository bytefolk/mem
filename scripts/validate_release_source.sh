#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
tag="${1:-}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "expected a release tag such as v0.1.1, got: ${tag:-<empty>}"
fi

tag_ref="refs/tags/${tag}"
if ! tag_type="$(git -C "${repo_root}" cat-file -t "${tag_ref}" 2>/dev/null)"; then
  die "${tag_ref} does not exist in the checked-out repository"
fi
[[ "${tag_type}" == tag ]] ||
  die "${tag_ref} is ${tag_type}, not an annotated tag object"

if ! tag_commit="$(git -C "${repo_root}" rev-parse "${tag_ref}^{commit}" 2>/dev/null)"; then
  die "${tag_ref} does not resolve to a commit"
fi
[[ "${tag_commit}" =~ ^[0-9a-f]{40}$ ]] ||
  die "${tag_ref} resolved to an invalid commit id"

head_commit="$(git -C "${repo_root}" rev-parse HEAD)"
[[ "${head_commit}" == "${tag_commit}" ]] ||
  die "checkout ${head_commit} does not match ${tag_ref} commit ${tag_commit}"

main_ref="refs/remotes/origin/main"
if ! git -C "${repo_root}" show-ref --verify --quiet "${main_ref}"; then
  die "${main_ref} is unavailable; fetch origin/main before releasing"
fi
if ! git -C "${repo_root}" merge-base --is-ancestor "${tag_commit}" "${main_ref}"; then
  die "${tag_ref} commit ${tag_commit} is not contained in origin/main"
fi

"${repo_root}/scripts/validate_release_version.sh" "${tag#v}" >/dev/null
printf '%s\n' "${tag_commit}"
