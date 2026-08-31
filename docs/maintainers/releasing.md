# Releasing mem

This document defines the release and packaging policy for `mem`. The
repository Release workflow publishes multi-platform `mem-mcp` binaries to a
GitHub Release. The npm package is published only after that Release has been
verified because its runtime bootstrap downloads and verifies those assets.

## Version policy

`mem` is a single-version monorepo. The Go service and clients, Python worker,
web application, schemas, and documentation ship under one
[Semantic Versioning](https://semver.org/) version:

- Stable release tag: `vMAJOR.MINOR.PATCH`
- Prerelease tag: `vMAJOR.MINOR.PATCH-rc.NUMBER`

Component-specific versions are not released independently. During the
experimental phase, breaking changes may be published under `v0.x.y`, but they
must still be documented.

An annotated Git tag and its GitHub Release are the canonical public release.
Do not move, delete, or reuse a published tag. Correct a bad release with a new
version.

## Current CI packages

The `CI` workflow builds verification artifacts for every pull request and
push to `main`:

| Artifact | Contents |
| --- | --- |
| `mem-go-linux-amd64-*` | `memd`, `mem`, `mem-mcp`, and `LICENSE` |
| `mem-worker-package-*` | Python wheel and source distribution |
| `mem-web-*` | Production web build |
| `go-coverage-*` | Go coverage profile and summary |
| `worker-coverage-*` | Worker coverage XML |

These artifacts are short-lived build evidence. They are not signed,
multi-platform release assets and must not be presented as a stable
distribution.

## Release preparation

Every release starts with a release issue and a dedicated release pull
request. The pull request must:

1. Select one SemVer version and document whether it is stable or a
   prerelease.
2. Move the relevant entries from `CHANGELOG.md`'s `Unreleased` section into a
   versioned section dated in `YYYY-MM-DD` format.
3. Synchronize every user-visible version field, including the Python worker,
   web package, MCP server metadata, and Go build version injection.
4. Regenerate affected lockfiles and generated metadata.
5. Record compatibility, migration, security, and rollback considerations.
6. Include reproducible validation evidence for all packages.

The release pull request may merge only after the protected `main` checks pass
and an independent reviewer approves it.

## Publication gate

Before creating a tag or GitHub Release, a maintainer must verify:

- The release pull request is merged into `main`.
- `CI`, `PR Policy`, and every required repository check passed for the exact
  release commit.
- The working tree is clean and the selected commit is contained in
  `origin/main`.
- The version does not already exist as a local tag, remote tag, or GitHub
  Release, and the npm version does not already exist in the registry.
- Release notes match the versioned `CHANGELOG.md` section.
- Packages were rebuilt from the exact tag commit in a clean GitHub-hosted
  environment and their checksums were recorded.

## Publication sequence

Publication is deliberately split at the tag boundary. The workflow never
creates, moves, or replaces a tag.

1. Merge the independently approved release pull request through protected
   `main`, then record its full commit ID and the successful required checks.
2. From an up-to-date, clean checkout of that exact commit, create one annotated
   tag and push only that new tag. Never reuse a version or move an existing
   tag.
3. The tag push starts `.github/workflows/release.yml`. A manual retry must be
   dispatched from the current default branch and must name the same existing
   annotated tag; the input is not permission to create a tag.
4. The workflow rejects a missing or lightweight tag, a tag not contained in
   `origin/main`, a checkout/tag mismatch, any public version mismatch, an
   existing GitHub Release, or anything other than the exact six expected
   binaries. Each binary embeds the tag commit as Go VCS metadata. The checksum
   manifest contains exactly one GNU `sha256sum` row per binary so the npm
   installer can parse it strictly.
5. Verify the completed GitHub Release is non-draft, uses the expected tag and
   exposes the six binaries plus `mem-mcp-checksums.txt`. Download all seven
   assets into a clean directory, run `sha256sum --check --strict
   mem-mcp-checksums.txt`, and inspect `go version -m` on each binary for the
   recorded release commit before starting npm publication.
6. In `npm/`, rerun `npm test`, the npm 12 clean-tarball test, and `npm pack
   --dry-run --ignore-scripts`. Confirm `npm view
   @fullstack-ai-infra/mem-mcp@VERSION version` does not find the version, then
   publish it once with `npm publish --access public`.
7. Install the public package in a clean temporary consumer with lifecycle
   scripts disabled, invoke `mem-mcp`, and confirm the checksum-verified binary
   is fetched from the matching GitHub Release. Record the Release URL, npm
   package URL, checksum result, and smoke-test result on the release issue.

Trusted Publishing is the target npm credential path. Until it is enabled, the
bootstrap publication may use a short-lived granular npm token restricted to
read/write access for `@fullstack-ai-infra/mem-mcp` only, with no unrelated
package or organization access. Keep the token out of repository files, logs,
workflow inputs, and issue comments, and revoke it immediately after the
post-publication smoke test.

If GitHub asset upload fails, inspect and remove any incomplete draft before a
reviewed retry; do not move the tag. If npm publication is wrong, deprecate the
bad version when possible and correct it with a new patch version rather than
reusing either the tag or package version.

## Deferred channels

The following are out of scope for the initial baseline:

- Automated release or version-bump pull requests
- PyPI publication
- Container registry publication
- Homebrew or other operating-system package managers
- Signed multi-platform binaries and provenance attestations

Each new channel requires its own issue, threat and rollback analysis,
credential design, and independently reviewable workflow. GitHub Release
remains the source of truth even after additional channels are added.
