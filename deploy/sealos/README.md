# Personal mem preview on Sealos

`template.yaml` is the native Sealos Template API recipe: one Web, one `memd`,
one small CPU/model-free Worker, private pgvector PostgreSQL 16, private Redis,
and a Sealos-managed private object bucket. Files, lexical memory recall,
checkpoints, and workspace transfer work without a model provider. The Worker
is authenticated but no Ollama server, ASR, face or CLIP model is bundled;
vector/image enrichment needs separately configured providers.

This preview reserves limits of 2.4 CPU, 2048Mi memory and 7Gi RWO storage
(plus managed object storage). Requests follow the Sealos quota ladder.
The actual namespace dry-run, balance and quota decide whether it fits.
Each process that writes uses a StatefulSet claim instead of unsupported
`emptyDir`; PostgreSQL uses a subdirectory so a fresh mounted volume initializes.

## Inputs

1. A Sealos namespace with sufficient quota and a registry accepting the built
   `server/`, `web/` and model-free `worker/` images for linux/amd64. Build the
   worktree revision, not a stale release. The optional preview workflow
   publishes commit-SHA tags under `ghcr.io/bytefolk/mem-preview-{server,web,worker}`;
   a workflow file is not evidence that the images were built or can be pulled.
2. The template's `pgvector/pgvector:0.8.1-pg16` image supplies the `vector`,
   `pgcrypto` and `pg_trgm` extensions. The private Redis URL uses generated
   credentials. Bucket keys/endpoint come from Sealos `object-storage-key`,
   and the bucket name from its managed per-bucket Secret. No MinIO console or
   database port is exposed. mem's MinIO client uses path-style S3 addressing.
3. The actual HTTPS hostname and Sealos ingress/TLS configuration.
4. A GitHub OAuth App owned by the operator, with callback URL exactly
   `https://<actual-mem-host>/v1/auth/github/callback`. It needs only
   `read:user user:email`; no repository or organization permission is used.

Supply the required Template inputs from a protected arguments file outside
Git. Generate the Worker auth key as standard Base64 of exactly 32 random
bytes. OAuth secrets and Worker keys enter the runtime Secret only; both
Docker build contexts exclude `.env*`. Do not print a rendered Secret or
commit arguments files. `secret.env.example` lists equivalent Helm keys.
OAuth identities, challenges and sessions are excluded from workspace bundles.

`MEM_REGISTRATION_MODE=disabled` closes password signup. The explicit
`MEM_GITHUB_BOOTSTRAP=true` switch permits only an allowlisted GitHub subject
to create the first owner atomically. Numeric ID `47820304` is the requested
personal account; usernames/email addresses are not authorization keys.
Further users cannot enroll. Existing email accounts must authenticate and
explicitly link GitHub; matching email never merges accounts.

## Rollout

Select the intended instance name and `defaults.app_host` before creating the
GitHub OAuth App; its callback must exactly match the generated hostname.
The personal deployment's requested host is
`mem-peterguy326-f5396ac8.hzh.sealos.run`; the generic template defaults remain
random to avoid duplicate instances. Use public images only after verifying
anonymous pull. For private images, precreate a fixed pull Secret and add its
name to all application workloads/init containers; do not reference a missing
Secret or copy a broad personal GitHub token to the cluster.

The operator uses the Sealos Template API CLI with the protected input file,
performs its free dry-run/quota preview, then deploys exactly that template
instance. Every resource carries `cloud.sealos.io/deploy-on-sealos` and the
last App CR opens the HTTPS Web entry. A successful API response still needs
pod readiness, URL checks and real GitHub callback/asset acceptance.

`mem-migrate` runs as the single memd replica's init container, then memd
starts with `MEM_AUTO_MIGRATE=false`. Database-not-ready failures retry with
the Pod; do not bypass migration. This revision adds migration 0027. It
preserves existing users/memberships/machine tokens; downgrade refuses to
orphan a GitHub-only account. Back up an existing deployment first.

The native recipe disables ingress access logging, and the inner nginx filters
OAuth callback query strings. It intentionally has no placeholder NetworkPolicy:
the base Helm chart's example CIDR would block Sealos dependencies/GitHub.
Before broader sharing, use verified namespace/pod labels and real egress
rules; do not mistake absence of a NetworkPolicy for network isolation.

For operators already running the Helm chart with provisioned dependencies,
`preview-values.yaml` remains a separate model-free/no-Worker overlay:

```sh
helm upgrade --install mem deploy/helm/mem \
  --namespace "$MEM_NAMESPACE" \
  -f deploy/sealos/preview-values.yaml \
  --set-string images.server.repository="$MEM_SERVER_IMAGE_REPOSITORY" \
  --set-string images.server.tag="$MEM_IMAGE_TAG" \
  --set-string images.web.repository="$MEM_WEB_IMAGE_REPOSITORY" \
  --set-string images.web.tag="$MEM_IMAGE_TAG" \
  --set-string ingress.host="$MEM_PUBLIC_HOST" \
  --set-string ingress.tls.secretName="$MEM_TLS_SECRET" \
  --wait --timeout 10m
```

Create the chart's `mem-runtime` Secret from the equivalent runtime keys.
Its pre-install/pre-upgrade migration Job must finish before the rollout.

## Acceptance

- Web `/healthz` and internal `memd /readyz` succeed.
- Login offers GitHub and no public account-creation option.
- An anonymous password registration fails; an unlisted GitHub user fails.
- The allowlisted account creates the first workspace and can upload/download
  a file and write/recall a sourced memory.
- Account menu shows the linked GitHub identity.
- Cookie-authenticated mutations require the exact origin and CSRF header.
- Logout revokes the server session; stale binding callbacks cannot log a user
  back in or change accounts. OAuth callback query strings are not access-logged.

Unit/HTTP fixtures do not certify real GitHub or Sealos interoperability.
Retain the actual callback/login/asset acceptance result after the rollout.
