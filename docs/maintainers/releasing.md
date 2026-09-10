# Releasing mem

This document defines the release and packaging policy for `mem`. The
repository Release workflow publishes multi-platform `mem-mcp` binaries to a
GitHub Release. The npm package is published only after that Release has been
verified because its runtime bootstrap downloads and verifies those assets.

The [2026-09-10 release decision](https://github.com/bytefolk/mem/issues/153#issuecomment-5612770493)
selects `@bytefolk/mem-mcp@0.1.2`, superseding the earlier keep-old-scope
instruction. [mem#153](https://github.com/bytefolk/mem/issues/153) and
[organization #22](https://github.com/bytefolk/.github/issues/22) govern the
migration. Source preparation does not clear their npm ownership/authentication
HOLD. The 2026-09-10 owner check returned `ENEEDAUTH`; no npm control, publisher
binding or real publication is established by this document or local tests.

Human contribution provenance: [#154](https://github.com/bytefolk/mem/pull/154)
(`bcd786f`, liyuanyang) supplies the actual package/cache migration;
[#162](https://github.com/bytefolk/mem/pull/162) (waterbro-8) supplies the MCP
registry identity correction only; [#168](https://github.com/bytefolk/mem/pull/168)
(`8a92baa`, waterbro-8) supplies mechanical version alignment. Retain these
sources in the release PR; do not attribute their changes to automated tools.

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
   annotated tag. The workflow resolves that input as the full
   `refs/tags/VERSION` ref, never as a short branch-or-tag name; the input is
   not permission to create a tag.
4. The workflow rejects a missing or lightweight tag, a tag not contained in
   `origin/main`, a checkout/tag mismatch, any public version mismatch, an
   existing GitHub Release, or anything other than the exact six expected
   binaries. Each binary embeds the tag commit as Go VCS metadata. The checksum
   manifest contains exactly one GNU `sha256sum` row per binary so the npm
   installer can parse it strictly. It uploads all seven assets to a draft,
   reads that draft back, and requires the exact seven names with non-zero
   sizes before its final command changes the draft to a public Release. Any
   earlier failure leaves the Release as a draft.
5. Read the completed GitHub Release back again. It must be non-draft, use the
   expected tag, and expose the six binaries plus `mem-mcp-checksums.txt`.
   Download all seven assets into a clean directory, run
   `sha256sum --check --strict mem-mcp-checksums.txt`, and inspect
   `go version -m` on each binary for the recorded release commit before
   starting npm publication.
6. Complete the interactive bootstrap and owner proof below. Run `npm test`
   in `npm/`, the npm 12 clean-tarball test on Linux, and
   `npm pack --dry-run --ignore-scripts`. Record any skipped platform explicitly.
7. Run `.github/workflows/npm-publish.yml` from the **exact stable tag**, after
   approving its `npm-release` environment. Its only registry write publishes
   the checked tarball with `--tag next --access public --provenance`.
8. Read back metadata, integrity, OIDC publisher ID, signatures, provenance and
   dist-tags. The workflow installs that exact public version with lifecycle
   scripts disabled and runs `npm audit signatures`. It does not launch the
   binary or promote `latest`. Complete the separate owner gates below.

## Bootstrap and Trusted Publisher setup (release owner only)

All commands in this section describe future owner actions, not actions
performed by the source-preparation PR. Use Node 24 and npm >=11.15.0. The
workflow pins npm 11.15.0 and validates the actual versions; a Node upgrade
alone does not prove the npm requirement.

1. The operator designated by organization #22 (`PeterGuy326`) logs into npm
   interactively on an authorized machine with 2FA. Privately inspect
   `npm whoami`, `npm org ls bytefolk --json`, organization role, package-name
   control, team/access and 2FA policy. Anonymous 404 and GitHub organization
   membership are not npm ownership proof. Record only a sanitized verdict on
   #153/#22. An error, missing permission or unclear ownership keeps the HOLD.
2. Prepare and independently review the **aligned** `0.1.2-rc.0` source,
   annotated `v0.1.2-rc.0` tag and matching seven GitHub assets using the same
   binary release gates. Do not just change a stable wrapper's version: the
   installer resolves assets for its own exact version. The stable npm workflow
   deliberately refuses RC tags.

   **Separate prerequisite, NOT VERIFIED:** this stable-source PR does not
   provide an aligned RC checkout, RC Release or bootstrap tarball. The current
   binary workflow and source/version validators accept the `-rc.NUMBER`
   spelling, but the RC must still have its own reviewed version/changelog
   surfaces and successful full gate run. If the selected RC source lacks
   those capabilities, a separate reviewed RC-gate implementation is required
   first. The following owner commands become usable only after that evidence
   exists. Do not merge/tag stable and then retroactively create an RC from
   different or unreviewed bytes; coordinate the RC prerequisite before sealing
   the stable release commit/tag. Never retag a published version.
3. From that clean RC checkout, pack and inspect the wrapper. After explicit
   bootstrap authorization, publish that reviewed tarball with interactive 2FA:

   ```sh
   npm publish ./bytefolk-mem-mcp-0.1.2-rc.0.tgz --access public --tag next --ignore-scripts --registry=https://registry.npmjs.org
   npm view @bytefolk/mem-mcp@0.1.2-rc.0 name version dist --json --registry=https://registry.npmjs.org
   npm dist-tag ls @bytefolk/mem-mcp --registry=https://registry.npmjs.org
   ```

   Verify public access and clean installation against the RC assets. The
   interactive bootstrap is not proof of GitHub OIDC provenance. Never assign
   this RC to `latest`, reuse a published version, or provide a CI token fallback.
4. On the new package's npm settings page, configure GitHub Trusted Publishing:
   organization `bytefolk`, repository `mem`, workflow filename
   `npm-publish.yml`, environment `npm-release`, and permission to run direct
   `npm publish`. A stage-only publisher is insufficient for this workflow.
   Read back the binding (settings UI or `npm trust list @bytefolk/mem-mcp
   --json`) and its configuration ID. Do not infer success just because npm
   accepted a settings form. No settings are created by the workflow.
5. Configure the GitHub `npm-release` environment with release-owner required
   review, prevention of self-review/admin bypass, and permitted release tags;
   separately protect tag creation. A tag's ancestry in protected `main` is
   checked by code. Record exact-commit CI and independent review before
   approving deployment. Environment approval is a gate, not evidence that
   npm ownership was verified.
6. Store a sanitized, exact-release JSON attestation as the **environment**
   variable `NPM_RELEASE_PROOF`, using the schema below. Fill it only from the
   authenticated owner's readback, with a lifetime of at most 24 hours. Do not
   put credentials, OTPs, raw account output or private evidence in it. Renew
   proof for each tag/commit or after a binding change.

```json
{
  "schema": 1,
  "package": "@bytefolk/mem-mcp",
  "tag": "v0.1.2",
  "commit": "REPLACE_WITH_EXACT_40_CHARACTER_TAG_COMMIT",
  "channel": "next",
  "organization": "bytefolk",
  "organizationControlVerified": false,
  "packageAccessVerified": false,
  "twoFactorVerified": false,
  "publisherVerified": false,
  "allowPublish": false,
  "repository": "bytefolk/mem",
  "workflow": "npm-publish.yml",
  "environment": "npm-release",
  "publisherId": "REPLACE_WITH_NPM_OIDC_CONFIG_ID",
  "approvedBy": "REPLACE_WITH_HUMAN_GITHUB_LOGIN",
  "evidence": "REPLACE_WITH_SANITIZED_OWNER_COMMENT_ON_153_OR_22",
  "verifiedAt": "REPLACE_WITH_UTC_TIMESTAMP",
  "expiresAt": "REPLACE_WITH_UTC_TIMESTAMP"
}
```

This deliberately non-authorizing example must fail. The attestation is a
human-controlled prerequisite, **not an automated npm membership lookup**.
The workflow also requires public bootstrap readback, a successful OIDC
exchange, and the same publisher ID in the published version. Missing or stale
proof blocks publication; no token, legacy scope or alternate registry fallback
exists. Do not fill these fields from the naming decision alone.

## Stable OIDC publication to next

Merge the stable `0.1.2` source/version PR, independently approve its exact
commit, and complete the stable GitHub binary Release first. The npm workflow
must exist in both the default branch and the selected stable tag. GitHub
events emitted by `GITHUB_TOKEN` normally do not start another workflow, so a
successful binary Release may need this explicit owner dispatch:

```sh
gh workflow run npm-publish.yml --repo bytefolk/mem --ref v0.1.2 -f version=v0.1.2
```

Unlike the binary Release workflow's manual entry, this npm entry rejects a
`main` dispatch. GitHub's event SHA/ref, checked-out annotated tag, npm package
and provenance source must agree. Its guard fetches `origin/main` and the exact
tag, rejects non-ancestry, dirty source, mismatched package/MCP/version surfaces,
draft/prerelease/wrong releases, and missing/extra/empty assets. It downloads all
seven assets, validates exactly six checksum rows, checks digests and embedded
Go commit/platform metadata without executing binaries, and packs the exact
six wrapper files with scripts disabled. Source, proof, Release identity,
registry version absence, channels and tarball integrity are rechecked before
the sole publish call. Repository npmrc files and ambient npm credentials or
configuration overrides are refused; npm runs with empty, isolated config.

The run then anonymously reads the new version, checks tarball integrity,
metadata, GitHub OIDC configuration ID, signature/provenance presence, `next`
and unchanged other tags, and verifies registry signatures/attestations through
`npm audit signatures` in a clean consumer. Its receipt records only `next`.
No automatic dist-tag promotion, access grant, deprecation or rollback occurs.

If publish fails or subsequent readback/audit fails, **stop**. The version may
already exist even if the run is red. Inspect the exact package/version,
preflight integrity and run before deciding recovery; never rerun publish as
an authentication test. Registry propagation delays also leave the run failed
pending read-only verification. Never overwrite/unpublish or move the tag.

## Separate latest and migration gates (release owner only)

Before promotion, attach all of the following to #153/#122: successful exact
tag OIDC run and receipt; current public access and Trusted Publisher readback;
verified npm signatures and provenance with expected repository/commit/workflow;
six binary checksums; and clean Linux, macOS and Windows install **and launch**
evidence. Install with lifecycle scripts disabled, invoke `mem-mcp`, and verify
its matching GitHub binary download. Include the existing-cache compatibility
case. Managed macOS machines must not execute newly built temporary Go binaries;
use approved isolated platform runners for those acceptance checks.

After the release owner explicitly accepts that evidence, use interactive 2FA:

```sh
npm dist-tag add @bytefolk/mem-mcp@0.1.2 latest --registry=https://registry.npmjs.org
npm view @bytefolk/mem-mcp dist-tags --json --registry=https://registry.npmjs.org
```

Require `latest=0.1.2` and repeat a clean default-channel install. Only after
OIDC is proven should the owner grant the reviewed `bytefolk:developers`
package access and enable npm's require-2FA/disallow-tokens setting, then read
those settings back. Complete consumer, directory/MCP, documentation and
lockfile migrations. Only after those consumers work may the owner authorize
deprecation of `@fullstack-ai-infra/mem-mcp` with an explicit replacement
message. Keep old `0.1.1` installable; **never unpublish** it or delete its cache.

Before cutover, rollback means stop and keep the old package untouched. After
cutover, restore consumers to the still-installable old coordinates if needed;
an owner may undo deprecation or restore a previously verified new-scope tag.
Correct bad releases with a higher patch. Recreate an incorrect publisher
binding through the owner process rather than adding a token fallback.

## Local verification and evidence limits

```sh
node --test scripts/npm-release.test.mjs
./scripts/test_release_guards.sh
./scripts/validate_release_version.sh 0.1.2
git diff --check
```

The new Node tests use temporary fixtures and injected Git/GitHub/npm command
adapters; no test publishes, changes remote settings, or executes a Go binary.
They exercise refusal and partial-failure behavior, including unavailable org
proof/registry, stale source, unsafe tags/packages, bad assets and failed
readback. A fixture PASS is local E3 evidence only. GitHub Actions execution,
independent approval, authenticated npm ownership/binding, actual OIDC/provenance
publication, three-platform launches and `latest` remain **NOT VERIFIED** until
their separate real evidence is recorded.

Technical assumptions checked against official documentation on 2026-09-10:
[trusted publishers](https://docs.npmjs.com/trusted-publishers/),
[npm trust prerequisites](https://docs.npmjs.com/cli/v11/commands/npm-trust/),
[publish options](https://docs.npmjs.com/cli/v11/commands/npm-publish/),
[dist-tags](https://docs.npmjs.com/cli/v11/commands/npm-dist-tag/), and
[provenance verification](https://docs.npmjs.com/generating-provenance-statements/).
GitHub's [workflow trigger rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)
explain the explicit dispatch after a Release published using `GITHUB_TOKEN`.
The trust command requires npm >=11.15.0 and an existing package; GitHub OIDC
requires the configured workflow/environment and hosted runner. Direct-publish
permission must be checked explicitly. `next` must be specified because npm's
default publication channel is `latest`.

## Deferred channels

The following are out of scope for the initial baseline:

- Automated release or version-bump pull requests
- PyPI publication
- Container registry publication
- Homebrew or other operating-system package managers
- Signed multi-platform binaries and binary provenance attestations (npm
  package provenance is required by the OIDC gate above)

Each new channel requires its own issue, threat and rollback analysis,
credential design, and independently reviewable workflow. GitHub Release
remains the source of truth even after additional channels are added.
