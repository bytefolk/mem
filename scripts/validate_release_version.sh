#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
version="${1:-}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_exact_line() {
  local relative_path="$1"
  local expected_line="$2"
  local expected_count="${3:-1}"
  local actual_count

  actual_count="$(grep -Fxc -- "${expected_line}" "${repo_root}/${relative_path}" || true)"
  [[ "${actual_count}" == "${expected_count}" ]] ||
    die "${relative_path}: expected ${expected_count} occurrence(s) of: ${expected_line}"
}

require_line_number() {
  local relative_path="$1"
  local line_number="$2"
  local expected_line="$3"
  local actual_line

  actual_line="$(sed -n "${line_number}p" "${repo_root}/${relative_path}")"
  [[ "${actual_line}" == "${expected_line}" ]] ||
    die "${relative_path}:${line_number}: expected: ${expected_line}"
}

if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$ ]]; then
  die "expected a SemVer without a leading v, got: ${version:-<empty>}"
fi

require_exact_line npm/package.json "  \"version\": \"${version}\","
require_exact_line npm/server.json "  \"version\": \"${version}\","
require_exact_line server/cmd/mem-mcp/main.go \
  $'\t\"version\": \"'"${version}"$'\", // synced with npm/@fullstack-ai-infra/mem-mcp version'

require_exact_line worker/pyproject.toml "version = \"${version}\""
require_exact_line worker/mem_worker/__init__.py "__version__ = \"${version}\""
require_exact_line worker/README.md \
  "Expected: \`status: SERVING\\nversion: \"${version}\"\`."

if ! awk -v expected="${version}" '
  /^\[\[package\]\]$/ { in_package = 0 }
  /^name = "mem-worker"$/ { in_package = 1; names += 1; next }
  in_package && /^version = / {
    if ($0 == "version = \"" expected "\"") matches += 1
    in_package = 0
  }
  END { exit(names == 1 && matches == 1 ? 0 : 1) }
' "${repo_root}/worker/uv.lock"; then
  die "worker/uv.lock: mem-worker must occur once at version ${version}"
fi

require_exact_line web/package.json "  \"version\": \"${version}\","
require_line_number web/package-lock.json 3 "  \"version\": \"${version}\","
require_line_number web/package-lock.json 9 "      \"version\": \"${version}\","

require_exact_line deploy/helm/mem/Chart.yaml "version: ${version}"
require_exact_line deploy/helm/mem/Chart.yaml "appVersion: \"${version}\""
for values_file in \
  deploy/helm/mem/values.yaml \
  deploy/helm/mem/values-production.example.yaml; do
  tag_count="$(grep -Ec '^[[:space:]]+tag:' "${repo_root}/${values_file}" || true)"
  [[ "${tag_count}" == 3 ]] || die "${values_file}: expected exactly three image tags"
  require_exact_line "${values_file}" "    tag: \"${version}\"" 3
done

require_exact_line docs/DEPLOYMENT.md "export MEM_VERSION=${version}"

changelog="${repo_root}/CHANGELOG.md"
first_release_heading="$(awk '/^## \[/ && $0 != "## [Unreleased]" { print; exit }' "${changelog}")"
[[ "${first_release_heading}" == "## [${version}] - "* ]] ||
  die "CHANGELOG.md: ${version} must be the first versioned section"
release_date="${first_release_heading#"## [${version}] - "}"
[[ "${release_date}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] ||
  die "CHANGELOG.md: ${version} must use a YYYY-MM-DD date"

heading_count="$(grep -Fc -- "## [${version}] - " "${changelog}" || true)"
[[ "${heading_count}" == 1 ]] ||
  die "CHANGELOG.md: expected exactly one ${version} release heading"
require_exact_line CHANGELOG.md \
  "[Unreleased]: https://github.com/bytefolk/mem/compare/v${version}...HEAD"

version_links=()
while IFS= read -r version_link; do
  version_links[${#version_links[@]}]="${version_link}"
done < <(grep -F -- "[${version}]: " "${changelog}" || true)
[[ "${#version_links[@]}" == 1 ]] ||
  die "CHANGELOG.md: expected exactly one [${version}] comparison link"
if [[ "${version_links[0]}" != \
    "[${version}]: https://github.com/bytefolk/mem/releases/tag/v${version}" &&
  "${version_links[0]}" != \
    "[${version}]: https://github.com/bytefolk/mem/compare/"*"...v${version}" ]]; then
  die "CHANGELOG.md: [${version}] link must terminate at v${version}"
fi

printf 'PASS: all release version surfaces match %s\n' "${version}"
