# Personal GitHub preview validation ledger

Local validation on 2026-10-01: Windows worktree mounted in Ubuntu 22.04 WSL,
Go 1.25 toolchain, Node 24.11.1, locked npm dependencies, CPython 3.11.15 and
locked uv dependencies. The original checkout and running user's mem stack
were not modified. PostgreSQL fixtures used a separately named disposable
`pgvector/pgvector:0.8.1-pg16` container on loopback port 55443; every test
database ended in `_test` and was created inside that owned container.

The requested scope is a personal preview: GitHub numeric allowlist, closed
password enrollment, one atomic first owner, explicit existing-account link,
browser cookies, unchanged machine Bearer API semantics, and a native Sealos
recipe. This does not complete the broader hosted-SaaS authentication roadmap.
The existing auth epic is design context, not a claimed ready issue or approval.
At this initial local validation stage, no PR, production release, merge or
independent end-to-end approval was claimed. Later CI follow-ups are recorded
below; the initial `NOT VERIFIED` rows describe that local validation stage.

| Check | Result | Evidence and limitations |
| --- | --- | --- |
| Go hermetic regression | PASS | `go test ./... -count=1` across server, CLI and MCP |
| Go static check and compile | PASS | `go vet ./...`, `go build ./...` |
| PostgreSQL integration and race | PASS | `MEM_TEST_DB=<owned regression DB> MEM_OAUTH_TEST_DB=<separate fresh OAuth DB> go test -race -p 1 ./... -count=1` |
| Latest OAuth authorization regression | PASS | Narrow race suite checks invalid Bearer does not fall back to cookie, foreign workspace denial and foreign-origin session denial |
| Populated migration upgrade | PASS | `MEM_MIGRATION_SEQUENCE_TEST_DB=<fresh owned DB> go test -race ./internal/db -count=1` exercises strict upgrades through 0027 and preserves the populated file/chunk/index fixture |
| OAuth-only downgrade guard | PASS | The HTTP fixture runs Goose downgrade, requires the explicit refusal while an OAuth-only user exists, and verifies identities remain readable |
| Provider exchange contract | PASS | PKCE S256, minimal `read:user user:email` scope, primary verified email, fixed endpoints and secret-safe failures use HTTP provider fixtures |
| Browser session state | PASS | Web's 119 tests include 10 new auth/API cases; the final focused 10-test run also passes |
| Web lint | PASS | `npm run lint`, no warnings |
| Web production build | PASS | `npm run build`; existing large-bundle warning remains informational |
| Browser acceptance | PASS | `test:i18n`, `test:theme`, `test:enrichment`, `test:memory`, `test:transfer` with Playwright Chromium and generated ignored MSW worker |
| Worker regression | PASS | `uv sync --locked --extra test`, `uv run pytest -q`; real visual-model evaluation skipped because `MEM_RUN_VISUAL_MODEL_EVAL` is unset |
| Native template syntax/grouping | PASS | 16 YAML documents parse; Template first, App last; deploy grouping labels on every resource and absent from Pod labels |
| GitHub Actions execution | NOT VERIFIED | CI now creates a separate fresh OAuth database; no remote CI success is inferred from local checks |
| Container publishing/anonymous pull | NOT VERIFIED | Runtime secrets are excluded from both changed Docker contexts; built GHCR images and pull access still need actual verification |
| Sealos dry-run, quota, deployment | NOT VERIFIED | Local structure checks do not replace the platform API or actual namespace quota |
| Live GitHub callback/account binding | NOT VERIFIED | All OAuth tests use a fake identity provider; the operator's actual OAuth App is not certified |
| Live file/memory acceptance on Sealos | NOT VERIFIED | After rollout: GitHub login, account identity readback, file upload/download, sourced remember/recall, and logout need real acceptance |
| Model-quality/real enrichment | NOT VERIFIED | The template has one authenticated CPU Worker but bundles no model service or optional AI extras |

The HTTP/PostgreSQL fixture verifies an anonymous visitor cannot password
register, an unlisted GitHub subject cannot enter, and the allowed owner can
bootstrap and write a sourced memory through the canonical API. It also checks
state tampering/replay, CSRF, cookie versus machine-credential separation,
email collisions without auto-link, identity transfer denial, and stale link
callbacks after logout/account switch or logout during provider exchange.

Frontend regression covers stale Bearer callback navigation, absent/failed
cookie bootstrap, same-user session replacement across tabs, CSRF/cache
refresh, logout acknowledgement/failure, late session readback, and late
JSON/raw/blob 401 responses from the previous actor. Browser session secrets
are HttpOnly cookies; only public session-generation metadata is persisted.

The Sealos recipe keeps database/cache ports internal, requests a private
managed bucket, uses managed object-store key references, passes only Worker
keys/Redis credentials to the Worker, and disables outer ingress access logs.
The Web proxy excludes callback codes/state from access logs. The recipe does
not claim verified namespace network isolation: no placeholder NetworkPolicy
is applied. Its limit total is 2.4 CPU/2048Mi and 7Gi RWO, plus object storage.

Browser artifacts retained outside Git:

- `/tmp/mem-localization-acceptance-52204`
- `/tmp/mem-theme-acceptance-53188`
- `/tmp/mem-enrichment-acceptance-53616`
- `/tmp/mem-memory-acceptance-54207`
- `/tmp/mem-transfer-acceptance-54648`

Windows line-ending conversion initially broke the pinned design-token hash
and exact CLI JSON golden comparison. Scoped `.gitattributes` rules preserve
their upstream LF bytes; the content is unchanged. The initial browser attempt
lacked ignored `public/mockServiceWorker.js`; `npx msw init public --save`
generated it locally, and all five browser gates subsequently passed.

Before any wider rollout, attach actual Sealos/GitHub/asset acceptance and
independent review to the PR validation ledger. Treat every `NOT VERIFIED`
row above as outstanding; source audit is not provider or deployment evidence.

## PR 231 dependency-audit follow-up

The first PR CI run at `6e85c9511625e043023dc6dd78ef126e0c8a5e8b` failed
both the [Web audit](https://github.com/bytefolk/mem/actions/runs/36865578915/job/110380137487)
and [Windows audit evidence](https://github.com/bytefolk/mem/actions/runs/36865578915/job/110380137434)
on newly reported development-only transitive dependencies. Production audit
already reported zero vulnerabilities. The Windows process fixture passed and
correctly preserved audit exit 1, so no audit script or severity gate was
weakened.

The targeted lockfile update preserves parent dependency ranges and changes
only `brace-expansion` 1.1.18 to 1.1.21, 2.1.4 to 2.1.7, and `undici` 7.29.0
to 7.30.0. npm supplied the updated registry integrity hashes. It addresses the
[brace-expansion advisories](https://github.com/advisories/GHSA-q2hr-2g5m-vwhr)
and [undici advisories](https://github.com/advisories/GHSA-3wwx-pv8p-q78v)
reported by the real registry; the complete registry report now contains no
vulnerabilities.

- Native Windows Node 24.19.0: `node --test scripts/test_win_audit_verify.mjs`
  passed all 4 process tests.
- Native `cmd.exe /d /c ..\scripts\win-audit-verify.bat` passed both the
  production-moderate and all-dependencies-high audits with zero vulnerabilities
  and final exit 0. Transcript retained outside Git at
  `C:\Users\huyz\rw-clone\mem-preview-evidence\win-audit-dependency-refresh.txt`;
  it records the tested checkout and exact audit exit status.
- Ubuntu WSL Node 24.11.1: the real `npm run audit` and affected Web checks
  passed after `npm ci --ignore-scripts --no-audit --no-fund`: both audit
  thresholds report zero vulnerabilities, all 119 unit tests pass, and lint,
  typecheck and the production build succeed. The large-bundle warning is
  unchanged; the produced JS/CSS asset hashes are unchanged.
- The same original CI run's [Go race/coverage job](https://github.com/bytefolk/mem/actions/runs/36865578915/job/110380137449)
  succeeded, including the separately provisioned OAuth database fixture.
- Remote CI must rerun on the committed repair before the PR is declared green.
