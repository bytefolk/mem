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
No PR, production release, merge or independent end-to-end approval is claimed.

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
