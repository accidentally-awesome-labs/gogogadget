---
title: Feature flags
description: DB-backed flags with per-org overrides, admin UI, and the gate pattern.
section: Features
weight: 22
---

Feature flags are two tables (`feature_flags`, `flag_overrides`), a 30-second
in-process cache, and one evaluator. That is the default and it needs no
account: `ggg/feature-flags` selects `feature-flags-postgres@postgres` in
development, test **and** production. One managed target exists as an
alternative — see [Managed target](#managed-target) — and nothing below
changes when you select it, because the seam is the same seven methods.
Admins manage flags at `/admin/flags` (site-admin only); the shipped consumer
is the Settings → Webhooks tab.

## Semantics (fixed)

| State | Result |
|---|---|
| Key missing | `false` |
| Per-org override exists | the override, always |
| `enabled = false` | `false` |
| `enabled`, rollout 100 | `true` |
| `enabled`, rollout 0 | `false` |
| `enabled`, rollout N | `fnv32(orgID + "\|" + key) % 100 < N` — deterministic per org |

The bucket hash makes rollout increases monotonic (the 50% cohort is a subset
of the 60% cohort), and evaluation never errors out loud: a DB hiccup serves
the last cached rows or `false` — never widens a feature.

## Evaluating

`web.Deps.Flags` defaults to `flags.NewDBEvaluator(q, 30s)`. Handlers get the
org from ctx:

```go
if s.flags.Enabled(ctx, org.ClerkOrgID, "beta_search") { … }
```

Templates see the gate through a context value — the Webhooks tab pattern:
`Render` sets `templates.WithWebhooksEnabled(ctx, s.flags.Enabled(…,
"webhooks"))`, and `SettingsTabs` reads it. Route handlers gate separately
(404 when off — hiding the tab alone is not authorization).

## Admin UI

`/admin/flags` lists every flag (key, description, enabled badge, rollout
input 0–100, delete). The badge toggles on click; the rollout form sets the
percentage; delete removes the flag and cascades its overrides (FK, migration
0008). The create form adds flags — keys are lowercase letters, digits, and
dashes (max 64); new flags start **off**. Every mutation writes an audit row
(`flag.created` / `flag.updated` / `flag.deleted` / `flag.override`) and
drops the evaluator's 30s flag-row cache, so changes apply on the next
render — including deletions, which would otherwise keep serving for up to
the TTL.

Clicking a flag's key opens `/admin/flags/{key}`: the per-org override
table plus a set-override form (org, on/off). Overrides win over the global
setting in both directions and are read per evaluation (never cached), so
they apply immediately.

## Managed target

`feature-flags-flagsmith@flagsmith` evaluates through
[Flagsmith](https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview)
over its two documented HTTP APIs, with no vendor SDK: the public Environment
Key reads the Flags API (`X-Environment-Key`), and a secret organisation token
drives every mutation on the Management API (`Authorization: Api-Key …`).
Select it with `ggg provider set ggg/feature-flags production
feature-flags-flagsmith@flagsmith` and configure
`FLAGSMITH_ENVIRONMENT_KEY`, `FLAGSMITH_MANAGEMENT_TOKEN`,
`FLAGSMITH_PROJECT_ID` and `FLAGSMITH_ENVIRONMENT_ID`. A missing key is one
joined boot error naming all of them — a selected managed adapter never falls
back to Postgres wearing a provider's name.

**The provider performs the decision.** `Enabled` sends the *organisation id*
as the Flagsmith identity and returns Flagsmith's answer verbatim; nothing is
re-evaluated locally. That makes a per-org override a Flagsmith **identity
override**, and Flagsmith's documented precedence — [identity overrides,
then segment overrides, then the environment
default](https://docs.flagsmith.com/flagsmith-concepts/segments#flag-evaluation-precedence)
— is exactly the fixed order above.

Two differences from the Postgres target, both deliberate:

- **Rollout is not a Flagsmith field.** A gradual rollout there is a
  percentage-split segment override, a separate object this adapter does not
  author, so `Upsert` **refuses** a rollout other than 100 by name rather
  than storing a number Flagsmith would never consult. Set the split in the
  Flagsmith console; per-org overrides keep working unchanged.
- **Decisions are cached for 30 seconds** through the `cache.store`
  capability, because evaluation is a network call. A changed flag or
  override becomes visible within that bound instead of on the next render.

No self-hosted target is published even though the image exists: a
self-hosted Flagsmith needs [its own PostgreSQL and a dashboard signup before
an Environment Key
exists](https://docs.flagsmith.com/deployment-self-hosting/hosting-guides/docker),
so a generated Compose service could never boot the adapter. If you run your
own Flagsmith, point `FLAGSMITH_API_URL` and `FLAGSMITH_FLAGS_API_URL` at it
and use this same target.

## Gate a feature

1. Insert the flag (seed SQL or SQL directly):
   `INSERT INTO feature_flags (key, enabled, rollout) VALUES ('beta_search', false, 0)`.
2. Guard the handler: `if !s.flags.Enabled(ctx, org.ClerkOrgID, "beta_search") { 404 }`.
3. Hide the UI behind the same evaluation (context value like
   `WithWebhooksEnabled`, or pass a bool in the view data).
4. Roll out: admin UI 0 → 10 → 50 → 100, or per-org overrides from the
   flag's detail page (`/admin/flags/{key}`).
