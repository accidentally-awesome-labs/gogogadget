---
title: Configuration
description: How environment configuration is declared, loaded, and validated.
section: Core
weight: 4
---

All configuration is environment variables. Every key is **declared by the
module that consumes it**, in that module's manifest, and both the parser
(`internal/config/config_registry_gen.go`) and the
[Configuration reference](/docs/configuration-reference) are generated from
those declarations. A module cannot read a setting it has not declared, and a
declared setting cannot be missing from the reference or from `.env.example`.

What stays hand-written in `internal/config/config.go` is behaviour over those
values: the environment predicates, the render clock, the `.env` reader, and the
few cross-field derivations described below.

All validation problems are reported together at boot, never one at a time.

Resolution order is fixed, and every layer below the first is a file:

1. **The process environment** — always wins.
2. **`.ggg/env/<environment>.env`** — the CLI-managed file, mode `0600` and
   gitignored. Genesis seeds it with the declared development posture and
   `ggg provider configure` writes into it; the generated `compose.yaml` names
   it as the app service's `env_file`. This is where a value you configured
   with the tool actually lives.
3. **`.env`** — the legacy file, **development only**. `.env.example` is
   generated and ships a working zero-account setup; copy it here if you
   prefer a single file.

A key that is present but empty counts as unset, so the next layer supplies
it. **Production reads no file at all** — not `.env`, and not
`.ggg/env/production.env` even if one exists; production configuration comes
from the deployment environment.

`ggg db migrate|status|seed|reset` resolve through exactly this order and
**refuse** when the value is missing rather than passing an empty one to the
tool. That refusal is deliberate: an empty connection string is not an error
to libpq — it falls back to its own defaults and connects to a local socket —
so a command that passed one on could migrate or seed a server the project has
nothing to do with. `--environment test` reads `.ggg/env/test.env` and never
`.env`. The resolved value reaches the tool through its environment
(`GOOSE_DBSTRING`, `DATABASE_URL`), never on the command line, because the
process list is public and a DSN carries a password.

A **declared default is not a configured value**. `DATABASE_URL`'s default,
`postgres://postgres:postgres@localhost:5432/gogogadget`, matches the
documented `docker compose up -d db` posture and the zero-account path depends
on it — but it is also a live address on any machine that has ever run
Postgres locally. So `ggg db status` reads through it, and `ggg db migrate`,
`seed` and `reset` **refuse** it, naming what to configure: a command that
mutates has to be told which database it is mutating. Supplying the same value
through the environment or the CLI-managed file is what makes it trusted.

## The full table

See the generated [Configuration reference](/docs/configuration-reference) for
every key, its owning module, whether production requires it, its default, and
its notes. It is rendered from the same records the parser is, so it cannot
drift from what the code actually reads.

Two more variables matter but are **not** read by `config.Load()`:

| Key | Read by | Default | Notes |
|---|---|---|---|
| `DB_PORT` | `compose.yaml` | `5432` | Host port for the local Postgres. Set it (and match the port in `DATABASE_URL`) when 5432 is taken |
| `TEST_DATABASE_URL` | `internal/db/testdb` | `postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable` | Server used by integration tests; each package gets its own database. See [Database](/docs/database) |

## Production validation rules

With `APP_ENV=production`, boot fails unless all of these hold:

- `DATABASE_URL` is set explicitly (no dev fallback DSN).
- All four `CLERK_*` keys are set: `CLERK_SECRET_KEY`, `CLERK_WEBHOOK_SECRET`,
  `CLERK_PORTAL_URL`, `CLERK_PUBLISHABLE_KEY`.
- `DEV_AUTH_BYPASS` is not `true` (refused outright).

Validated in every environment: `APP_ENV` must be one of the three values,
`PORT` must be a valid port, `POLAR_SERVER` must be `sandbox` or `production`,
and a malformed `TEST_NOW` under `APP_ENV=test` is an error.

## Degradation, not crashes

Everything optional degrades cleanly when unset: Clerk → `/app` 503s (or dev
bypass mode), Polar → billing routes 503, Resend → DevSender, PostHog and
Sentry → no-ops. You can adopt services in any order.

## Recipes

**Local development, zero accounts** (what `.env.example` ships):

```sh
APP_ENV=development
DEV_AUTH_BYPASS=true
DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable
```

**Local development with real Clerk:** add the four `CLERK_*` keys. When Clerk
is configured, `/login` goes to the hosted portal instead of `/dev/login`.

**Tests** (what `e2e/playwright.config.ts` uses):

```sh
APP_ENV=test PORT=18080 DEV_AUTH_BYPASS=true \
DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogogadget_e2e \
TEST_NOW=2026-01-15T00:00:00Z
```

**Production:** `APP_ENV=production`, a real `DATABASE_URL` (e.g. Neon), the
four `CLERK_*` keys, `CLERK_PORTAL_URL`, plus whatever optional services you
use. `LOG_LEVEL` defaults to `info` and logs go out as JSON. See
[Deployment](/docs/deployment).
