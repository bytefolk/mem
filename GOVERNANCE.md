# Release governance

This document records the repository rules that govern release tag creation
and the sanctioned procedures maintainers follow to cut a release. It
supplements the step-by-step release workflow in
[`docs/maintainers/releasing.md`](docs/maintainers/releasing.md).

## Tag protection rulesets

Two repository rulesets protect `refs/tags/v*`:

| ID | Name | Rules | Created (UTC) |
| --- | --- | --- | --- |
| 21888356 | Protect stable release tags | `update`, `deletion` | 2026-08-31 00:34:47 |
| 21899500 | Restrict stable release tag creation | `creation` | 2026-08-31 04:47:11 |

Both rulesets target `refs/tags/v*`, enforce `active`, and have no bypass
actors configured. The `current_user_can_bypass` field reports `never` for
every caller, including repository admins.

## Inspection: how was v0.1.1 cut?

The `v0.1.1` annotated tag exists with tagger timestamp `2026-08-31T06:32:10Z`,
which is after both rulesets were created. GitHub does not expose ruleset
modification timestamps through the REST API or the audit log, so there is no
way to prove which path was used:

- The ruleset may have had a bypass actor that was later removed.
- An admin may have temporarily disabled the creation rule and re-enabled it.
- A different mechanism may have been used before the rulesets were finalised.

**Finding:** the v0.1.1 route is **unprovable** under the current GitHub API
surface. The status quo ante is recorded; no retrospective judgment is made.

## Sanctioned tag-cut path

Until ruleset 21899500 exposes a bypass actor, no maintainer — including
repository admins — can push a `refs/tags/v*` tag. The release runbook step
"cut a new patch tag" is therefore **unexecutable** in the current
configuration.

### Required admin action

A repository admin must configure exactly one of the following on ruleset
21899500:

1. **Bypass actor (preferred).** Add a narrowly-scoped bypass actor so that
   the release tag push is permitted without disabling the rule for everyone
   else. Acceptable scopes, in order of preference:
   - A repository role limited to the release maintainer set (for example,
     a custom `release-manager` role).
   - The built-in `admin` role, restricted to the two named release
     maintainers recorded on the release issue.
   - A GitHub Actions integration if the tag creation is later moved into
     the Release workflow.

   The rule stays enforced for all other actors. Ad-hoc disabling of the
   rule remains forbidden.

2. **Documented admin procedure.** If a bypass actor cannot be configured,
   record an admin-executed procedure here that:
   - Names the authorised admin.
   - Requires a second maintainer to witness the tag creation in a
     synchronous session.
   - Records the evidence (tag SHA, commit, timestamps) in the release
     issue within one hour.

   This path is a fallback only; the bypass actor is the intended design.

### Tag-cut procedure (after bypass actor is configured)

1. Confirm the release pull request is merged and the exact commit is on
   `origin/main`.
2. From a clean checkout of that commit:
   ```bash
   git tag -a -m "Release v0.1.2" v0.1.2
   git push origin v0.1.2
   ```
3. Verify the tag push triggers `.github/workflows/release.yml`.
4. Record the tag SHA, commit, and workflow run URL in the release issue
   as release evidence.

### Dry-run validation

Once the bypass actor is in place, validate the sanctioned path with a
dry-run before the next real release:

1. Create a test tag `v0.0.0-dry-run` from the latest `main` commit.
2. Confirm the push succeeds without disabling ruleset 21899500.
3. Delete the test tag immediately after confirmation.
4. Record the result in the release evidence ledger on the release issue.

## References

- Issue [#163](https://github.com/bytefolk/mem/issues/163) — original
  inspection and remediation request.
- Issue [#125](https://github.com/bytefolk/mem/issues/125) — parent
  governance charter.
- [`docs/maintainers/releasing.md`](docs/maintainers/releasing.md) —
  release preparation and publication sequence.
