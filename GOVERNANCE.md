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

Branch protection has **no bypass actors** (`bypass_pull_request_allowances` is empty), so
there is no role-based route around the review requirement. That is a separate control from
the tag-creation bypass described under Tag immutability; the two must not be conflated.

A pull request whose author is a CODEOWNER requires approval from another current
CODEOWNER. Self-approval and merges without a current independent approval are not
permitted. After the current head has that approval and all gates pass, an eligible
author or maintainer may perform the normal merge.

## Tag immutability

Release tags matching `refs/tags/v*` are covered by two active rulesets, and their bypass
posture is **not** symmetric:

- `Protect stable release tags` (`21888356`) blocks `update` and `deletion`, and has **no
  bypass actors**. No role, repository admin included, can move or delete a published `v*`
  tag.
- `Restrict stable release tag creation` (`21899500`) blocks `creation`, and has **exactly
  one** bypass actor: repository role `admin` (`repositoryRoleDatabaseId` 5),
  `bypass_mode: always`. A repository admin can cut a `v*` tag directly.

That asymmetry is the point. Creation stays reachable so a release can always be cut by an
admin, while published tags stay immutable for everyone, admins included. Immutability is
enforced by `21888356`, not by `21899500`.

Read both from the individual ruleset endpoint, `GET /repos/{owner}/{repo}/rulesets/{id}`.
The collection endpoint `GET /repos/{owner}/{repo}/rulesets` renders `bypass_actors` as
`null` for every ruleset, which is what caused an earlier revision of this section to
record "neither of which has bypass actors" — false when written, and the reason the
per-ruleset read is called out here. GraphQL's `repositoryRoleName` on
`RepositoryRulesetBypassActor` returns the role name directly and is the clearest check.

Rulesets do expose `created_at` and `updated_at` on the individual endpoint, so the pair is
orderable: `21888356` was created at `2026-08-31T00:34:47Z`, roughly four hours before
`21899500` at `2026-08-31T04:47:11Z`, which was itself updated 54 seconds later at
`04:48:05Z`.

`v0.1.1` is the empirical proof of the admin creation bypass. The annotated tag `v0.1.1`
(tag object `c2ecc1c49ff8bbe13b9d7800bc910e4b7ac99b74`, pointing at commit
`cc727db0bc72655f299166de1f60756f5c686cc7`) is tagged `2026-08-31T06:32:10Z` — one hour
and forty-five minutes after the creation restriction became active, by a repository admin.

- A release tag, once created, **must not** be moved or deleted. Force-moving a tag to
  paper over a broken release destroys the provenance that a release tag exists to
  provide. This is mechanically enforced against every role.
- If a release is broken, cut a **new patch tag** (`v0.1.1`) from a fixed commit. Do not
  retag `v0.1.0`.
- Published tags must not be moved, deleted, or reused, including during a security
  incident.
- Tag **creation** is restricted to repository admins by `21899500`. Cutting a release tag
  is therefore an admin action that needs no ruleset change, and the admin who cuts it is
  accountable for the release-cut review requirements in the next section.

## Release-cut pull requests

A release cut (a PR that bumps the version, updates a changelog, or otherwise
prepares a release) is held to a stricter bar than an ordinary PR:

- The release-cut PR **must be approved by a non-author CODEOWNER**. The author's
  own approval does not count, and branch protection has no bypass actors, so no
  administrator route around this review exists.
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
   from that new tag. Tag creation on `refs/tags/v*` is restricted to repository admins
   by ruleset `21899500`, so this step is an admin action — it does not require changing
   any ruleset, and no ruleset change should be made in order to perform it.
