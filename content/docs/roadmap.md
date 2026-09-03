---
title: Roadmap
description: What the framework actually does today, the known gaps recorded against it, and what is deliberately delegated.
section: Guides
weight: 29
---

This page is honest bookkeeping, not a wish list. Everything under **Shipped**
is in the tree and exercised by a gate; everything under **Known gaps** is a
defect or an unfinished edge someone has already found, with the file to look
at; everything under **Delegated** is a decision not to build something.

## Shipped: the framework surface

| Capability | What exists |
|---|---|
| Provider-aware schema v2 | `gogogadget.json` carries `registries`, `modules`, `exclude`, `providers`, `deployment`; `requires` is `{id, contract:{min,max}}`; `ggg migrate schema-1` is the one-way upgrade and normal commands accept schema 2 only |
| Provider slots | 18 slots, 36 adapter modules, 42 selectable adapter@target pairs; one selection per slot per environment, all compiled into one binary with only the `APP_ENV` branch initializing |
| Zero-account local path | Local adapter for every slot: synthetic identity, in-app billing, filesystem mail/storage, Postgres flags/search/realtime/notifications/webhooks/usage, memory cache and rate limiting, fake LLM, log reporter, noop analytics/telemetry/audit-export |
| Managed references | Neon, Clerk, Polar, Resend, R2, Upstash, Valkey, MinIO, Typesense, Ably, LaunchDarkly, Knock, Svix, OpenMeter, PostHog, Sentry, OTLP, any OpenAI-compatible API |
| Generated boot graph | One `Runtime` field per capability, one boot function per environment, `providerActive` gating on every executable contribution an adapter owns, compile-time health assertions |
| Health | `apphost.AggregateHealth` — concurrent, 2s per check, panic-safe, 10s cache; only `ggg/database`, `ggg/identity`, `ggg/billing` are `critical`. `GET /readyz` consumes the report through the `runtime.health` capability: an unhealthy critical slot is a 503 naming it, an unhealthy non-critical slot is a 200 reporting it degraded |
| Provider-neutral identity and billing | Opaque `usr_…`/`org_…` ids with `identity_subjects`, `identity_organizations`, `billing_accounts` mapping tables; `identity.ErrLinkRequired` instead of auto-merge; audited `ggg identity link` |
| Signed external registries | Ed25519 snapshot signing, pinned public keys and fingerprints, per-module snapshot provenance in the lock, offline resolution from the verified cache, two-signature key rotation with `not_before` |
| Typed CLI platform | One command table driving dispatch, help, completions and reserved names; sealed request/mutation types; the fixed envelope and exit codes `0`–`5` unchanged |
| Interactive console | `ggg ui` (contributed by `ggg/system/cli-ui`) with Home, Catalog, Providers, Plan, Conflicts, Tasks, Diagnostics and Help screens; `--accessible` / `GGG_ACCESSIBLE=1` switches guided forms to linear prompts; a non-TTY `ui` is the declared `interactive_terminal_required` usage failure |
| Project lifecycle | `ggg new`, `init`, `create module\|resource\|page\|workflow\|job\|migration\|component\|provider`, `setup`, `generate`, `services`, `dev`, `db`, `check`, `test`, `build` |
| Vertical-slice generation | `ggg create resource` emits queries, migration, transport, templates, test and every declaration; `--api`, `--admin`, `--search`, `--no-ui` are live, with three refusals and a plan-visible narrowing diagnostic |
| Remote operations | `ggg provider list\|set\|configure\|provision\|test`, `ggg deployment set`, `ggg deploy plan\|apply\|status\|logs\|rollback\|secrets`, `ggg db backup\|restore\|restore-drill`, `ggg doctor --runtime [--fix]`, stale-plan refusal, `--resume RUN_ID`. `deploy plan` calls the deploy target's own `Plan` and reports the ordered change set; `deploy status` is the observation. Every mutating form confirms: `--yes` (or `--resume`) is the noninteractive confirmation, and its absence off a TTY refuses with exit 3 |
| Targeted updates | `ggg update MODULES…` and `ggg update --registry NAMESPACE --ref REF`, per-module snapshots, conflict staging and `ggg resolve` |
| Publishing | `ggg registry init\|keygen\|build\|sign\|verify\|rotate\|add\|remove\|update\|validate` plus the maintained `templates/external-registry/` template and its CI workflow |
| Verification | `ggg registry validate --closures core\|external\|all` as two CI jobs, per-module e2e spec ownership with a mechanical no-orphan check, seam contract suites, provider permutation fixtures, race, fuzz, smoke, docker and visual gates |

## Known gaps

Recorded, reproducible, and not yet fixed.

| Gap | Evidence | Consequence |
|---|---|---|
| Two seams still carry vendor SDKs | `internal/identity` imports the Clerk SDK and `svix`; `internal/billing` imports `standard-webhooks` — the webhook parsers have not moved into `identity-clerk` / `billing-polar` | Selecting a non-Clerk identity adapter still compiles the Clerk SDK into the binary |
| `ggg doctor --fix` covers one finding | `doctorRemediation` maps only `env_file_missing` | Every other finding reports advice and expects a human |
| Seam manifests declare the vendor Go dependencies | `ggg/system/identity` and `ggg/system/billing` declare the Clerk, svix and standard-webhooks module requirements their adapters should own | Removing the Clerk adapter does not drop the SDK from `go.mod` |
| Per-category audit retention | `AUDIT_RETENTION_DAYS` is one global window (0 = forever) | Retention cannot differ per action category |

## Delegated

| Area | Decision |
|---|---|
| SSO/SAML/SCIM, passkeys, device management | The identity adapter's job. Document the provider, do not rebuild it |
| Status page | Recommend an external monitor reading `/healthz`; a status page on the same failure domain is not a status page |
| Custom domains per org, per-seat billing, coupons | Billing-provider configuration, not code |
| An alternate web stack or datastore | Out of scope by construction. Go + templ + htmx + Alpine + Postgres is the framework; another stack is a different generator family, not an interface layered into this one |
| An ORM, a bundler, a hydration runtime | Never. sqlc, the Tailwind standalone binary and htmx cover the ground |

## Not started

- **In-app help, breadcrumbs, a command palette** — absent from
  `internal/web/templates/`; additive whenever someone wants them.
- **A managed-target canary suite** — provider CI uses fake HTTP and local
  protocol containers by design; live credentials are not a contributor gate.
