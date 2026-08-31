#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
tag="${1:-}"

if [[ ! "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  printf 'ERROR: expected a release tag such as v0.1.1, got: %s\n' \
    "${tag:-<empty>}" >&2
  exit 1
fi

version="${tag#v}"
"${repo_root}/scripts/validate_release_version.sh" "${version}" >/dev/null

awk -v prefix="## [${version}] - " '
  index($0, prefix) == 1 {
    found = 1
    next
  }
  found && /^## \[/ { exit }
  found {
    lines += 1
    if ($0 !~ /^[[:space:]]*$/) content = 1
    print
  }
  END {
    if (!found || !lines || !content) exit 1
  }
' "${repo_root}/CHANGELOG.md"
