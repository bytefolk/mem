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

Issue #124 records the resulting decisions through revision R4: R2 applied the narrow
admin-enforcement change, R3 canonicalized the founder-approved addition of a third code
owner, and R4 corrected the lifecycle record. The issue is closed. The matching branch
protection and the two `refs/tags/v*` rulesets enforce it mechanically.

Where this charter states a configuration value, it records a point-in-time read of that
configuration. GitHub's live settings stay authoritative and some of them are readable
only by repository administrators, so a stale line here is a documentation defect to
correct, never a change in enforcement and never a reason to weaken the stronger rule.

## Branch protection on `main`

`main` is protected with the following non-negotiable settings:

- **`enforce_admins: true`** — administrators are **not** exempt, preventing future
  administrator bypasses without changing the status of historical merges. This value is
  the field-for-field read-back recorded in #124 after the narrow
  `POST .../protection/enforce_admins` change. Contributors without admin cannot read the
  endpoint; they verify it by observing that a merge is blocked, not by reading it.
- **Strict required status checks** (`strict: true`): every required check must pass on a
  head that is up to date with `main` before a merge. The authoritative required-check list
  is repository configuration readable only by administrators. As of 2026-09-03 the check
  jobs observed on this repository are `Go`, `Worker`, `Web`,
  `Conventional title and linked issue`, `Workflow, scripts and Compose`,
  `PostgreSQL integration`, `Web memory and transfer acceptance`,
  `HTTP, CLI and MCP lifecycle`, `Agent host MCP contract`, `Deployment profiles`,
  `Offline recall benchmark`, `npm wrapper`, and `npm wrapper compatibility`
  (`node18-linux`, `node20-linux`, `node24-windows`). That enumeration is evidence of what
  runs, not a substitute for the configuration, and it is not presented here as the exact
  required set.
- **Required pull request reviews**: `required_approving_review_count = 1`,
  `require_code_owner_reviews = true`, `dismiss_stale_reviews = true`, and
  `require_last_push_approval = true`.
- **Required linear history** and **required conversation resolution** are enabled.
- **Force pushes and branch deletion are disabled**.
- **No direct pushes** to `main`. All changes go through a pull request.

A pull request whose author is a CODEOWNER requires approval from another current
CODEOWNER. Self-approval and merges without a current independent approval are not
permitted. After the current head has that approval and all gates pass, an eligible
author or maintainer may perform the normal merge.

## Tag immutability

Release tags matching `refs/tags/v*` are covered by two active rulesets, neither of which
has bypass actors:

- `Protect stable release tags` (`21888356`) blocks `update` and `deletion`.
- `Restrict stable release tag creation` (`21899500`) blocks `creation`.

GitHub exposes no creation timestamp for rulesets, so this document does not claim which
rule predates the other. What it records is the live pair.

- A release tag, once created, **must not** be moved or deleted. Force-moving a tag to
  paper over a broken release destroys the provenance that a release tag exists to
  provide.
- If a release is broken, cut a **new patch tag** (`v0.1.1`) from a fixed commit. Do not
  retag `v0.1.0`.
- Published tags must not be moved, deleted, or reused, including during a security
  incident.
- Tag **creation** is gated too, so cutting a tag is itself a restricted action. It must
  go through a path the creation rule permits. If no such path exists when a release is
  needed, that is a blocker to resolve through a reviewed ruleset change — never by
  disabling a rule, bypassing it, or relaxing this charter.

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

The roster is defined in [`.github/CODEOWNERS`](.github/CODEOWNERS) and is deliberately
not duplicated here. The rules below apply to whoever is listed there at the time of each
pull request: this section describes policy, not a name list, so an owner change cannot
leave the charter asserting a roster that no longer matches the file it defers to.

- At least two owners are required so that a non-author approval is always satisfiable.
  Two is a floor, not a target: one owner makes the review gate structurally
  unsatisfiable, and two leave an availability bottleneck.
- When an owner authors a pull request or makes its last push, a different current owner
  supplies the required independent approval.
- Changing the roster is a governance action taken in `.github/CODEOWNERS` through a
  reviewed pull request that cites the owner decision authorizing it. Adding an owner
  grants review authority only; it grants no tag creation, no role change, and no
  relaxation of anything above.
- The charter's narrative is kept in step with that change. #124 revision R3 authorized
  the third owner, which reached `main` through #138; the corresponding charter wording is
  this section, landed as the documentation follow-up rather than in the same cycle. That
  ordering is the defect this section exists to close, not a precedent to repeat.

## Incident runbook

If a release tag is found to point at a broken or compromised commit:

1. Do not retag. Do not delete the tag.
2. For a security incident, open a security advisory and deprecate or yank affected
   distribution channels where supported. Leave the published tag in place as
   immutable evidence.
3. Open a fix PR. Get it reviewed and merged to `main` with a current non-author
   approval.
4. Cut a new patch tag from the fixed `main` tip and publish the replacement release
   from that new tag. Tag creation on `refs/tags/v*` is restricted and has no bypass
   actors, so this step runs through a permitted creation path; a missing creation path
   is escalated, not worked around.
