# Release Governance

This document supplements the repository workflow in [AGENTS.md](AGENTS.md) and the
release policy in [docs/maintainers/releasing.md](docs/maintainers/releasing.md). It
records the repository's branch-protection, tag-immutability, and release-cut review
governance without weakening or replacing either document. Repository settings are
the immediate mechanical enforcement; any drift between them and this additive
charter must be corrected without weakening the stronger rule.

## Motivation

The v0.1.0 release on 2026-08-30 exposed three gaps:

1. The `v0.1.0` tag had multiple create/delete cycles in one day while a broken
   release workflow was iterated on. Tag history was not immutable.
2. Issue #81 resolved the structurally unsatisfiable single-CODEOWNER setup by
   adding a second owner. A separate gap remained: branch protection did not apply
   to administrators, so an administrator could merge without the otherwise-required
   independent approval. The recent-five audit covered #117, #115, #114, #108, and
   #105.
3. A broken `download-artifact` SHA reached the release workflow because no reviewer
   saw the release-cut PR before it was merged.

Issue #124 revision R2 records the maintainer decision and applied setting change.
This charter documents that decision; the matching branch protection and tag ruleset
enforce it mechanically.

## Branch protection on `main`

`main` is protected with the following non-negotiable settings:

- **`enforce_admins: true`** — administrators are **not** exempt, preventing future
  administrator bypasses without changing the status of historical merges.
- **Strict required status checks** (`strict: true`, all must pass before merge):
  `Go`, `Worker`, `Web`,
  `Conventional title and linked issue`, `Workflow, scripts and Compose`,
  `PostgreSQL integration`, `Web memory and transfer acceptance`,
  `HTTP, CLI and MCP lifecycle`.
- **Required pull request reviews**: `required_approving_review_count = 1`,
  `require_code_owner_reviews = true`, `dismiss_stale_reviews = true`, and
  `require_last_push_approval = true`.
- **Required linear history** and **required conversation resolution** are enabled.
- **Force pushes and branch deletion are disabled**.
- **No direct pushes** to `main`. All changes go through a pull request.

A pull request whose author is a CODEOWNER requires approval from the **other**
CODEOWNER. Self-approval and merges without a current independent approval are not
permitted. After the current head has that approval and all gates pass, an eligible
author or maintainer may perform the normal merge.

## Tag immutability

Release tags matching `refs/tags/v*` are protected by the repository ruleset
`Protect stable release tags`, which blocks both `deletion` and `update`.
The ruleset is active and has no bypass actors.

- A release tag, once created, **must not** be moved or deleted. Force-moving a tag
  to paper over a broken release destroys the provenance that a release tag exists
  to provide.
- If a release is broken, cut a **new patch tag** (`v0.1.1`) from a fixed commit.
  Do not retag `v0.1.0`.
- Published tags must not be moved, deleted, or reused, including during a security
  incident.

## Release-cut pull requests

A release cut (a PR that bumps the version, updates a changelog, or otherwise
prepares a release) is held to a stricter bar than an ordinary PR:

- The release-cut PR **must be approved by a non-author CODEOWNER**. The author's
  own approval does not count, and the admin bypass does not exist.
- The release-cut PR must not be merged while any required status check is failing
  or in-progress. "Merge now, fix the release workflow by retagging" is the exact
  anti-pattern this charter prohibits.
- If a release workflow fails after the cut, the fix goes through a **new PR** that
  is reviewed and merged, then a **new tag** is cut — not a retag of the broken one.

## CODEOWNERS

Current owners (`.github/CODEOWNERS`):

```
* @PeterGuy326 @Bindy-lbb
```

Two owners is the minimum for a satisfiable non-author approval. It also creates a
potential availability bottleneck. The maintainer decision is to keep these two
owners; a third independent owner is optional redundancy, not a correctness
requirement. When either owner authors or makes the last push to a pull request, the
other owner supplies the required independent approval. Any addition is a governance
change to this document, not a silent edit to CODEOWNERS.

## Incident runbook

If a release tag is found to point at a broken or compromised commit:

1. Do not retag. Do not delete the tag.
2. For a security incident, open a security advisory and deprecate or yank affected
   distribution channels where supported. Leave the published tag in place as
   immutable evidence.
3. Open a fix PR. Get it reviewed and merged to `main` with a current non-author
   approval.
4. Cut a new patch tag from the fixed `main` tip and publish the replacement release
   from that new tag.
