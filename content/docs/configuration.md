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
4. **The derived value** — what *this project* resolves for that environment
   from its own selection: the selected database adapter's local service on the
   host port that environment publishes. `DATABASE_URL` is the one derived key
   today, and it is `localhost:5432` for development and `localhost:15432` for
   test out of the box, following any `ports` override in `gogogadget.json`.
5. **The owning module's declared default** — a documented guess, and the last
   resort. See below.

A key that is present but empty counts as unset, so the next layer supplies
it. **Production reads no file at all** — not `.env`, and not
`.ggg/env/production.env` even if one exists; production configuration comes
from the deployment environment. Production also **derives nothing**: it has no
generated Compose file and therefore no local service, so there is no host
address to name.

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
`postgres://postgres:postgres@localhost:5432/gogogadget`, matches the host port
the development stack publishes and the zero-account path depends on it — but
it is also a live address on any machine that has ever run Postgres locally,
and on such a machine loopback reaches that server rather than the container.
So `ggg db status` reads through it, and `ggg db migrate`, `seed` and `reset`
**refuse** it, naming what to configure: a command that mutates has to be told
which database it is mutating. `go run ./cmd/seed` refuses it for the same
reason — the generated parser records where each value came from, and seeding
migrates and writes.

A **derived value is trusted**, and that is the difference. It is not a guess:
it reflects the adapter this project selected and the port this project
publishes, so `ggg db migrate --environment test` reaches `15432` with nothing
configured at all. Supplying a value through the environment or the
CLI-managed file overrides it.

## The full table

See the generated [Configuration reference](/docs/configuration-reference) for
every key, its owning module, whether production requires it, its default, and
its notes. It is rendered from the same records the parser is, so it cannot
drift from what the code actually reads.

Three more variables matter but are **not** read by `config.Load()`. All are
overrides: with none exported, each consumer resolves the address the
project's own test stack publishes, through the same derivation `ggg db` uses.

| Key | Read by | Default | Notes |
|---|---|---|---|
| `TEST_DATABASE_URL` | `internal/db/testdb` | the derived test address (`localhost:15432` out of the box) | Server integration tests create their per-package databases on. See [Database](/docs/database) |
| `E2E_DATABASE_URL` | `e2e/playwright.config.ts` | the derived test address | Database the Playwright suite reseeds and drives. CI exports it to name its own service container |
| `VISUAL_DATABASE_URL` | `scripts/visual-run.sh` | the derived test address | Database the visual harness **drops** and reseeds. Its own key, passed explicitly to both children, so an ambient `DATABASE_URL` cannot redirect the one command allowed to write baselines |

`DB_PORT` is gone. Host ports are a project decision now: declare them under
`ports` in `gogogadget.json` and both the generated Compose files and every
derived address follow. See [Deployment](/docs/deployment).

## Production validation rules

With `APP_ENV=production`, boot fails unless all of these hold:

- `DATABASE_URL` is set explicitly (no dev fallback DSN).
- All four `CLERK_*` keys are set: `CLERK_SECRET_KEY`, `CLERK_WEBHOOK_SECRET`,
  `CLERK_PORTAL_URL`, `CLERK_PUBLISHABLE_KEY`.
- `DEV_AUTH_BYPASS` is not `true` (refused outright).

Validated in every environment: `APP_ENV` must be one of the three values,
`PORT` must be a valid port, `POLAR_SERVER` must be `sandbox` or `production`,
and a malformed `TEST_NOW` under `APP_ENV=test` is an error.

None of that is hand-written in `internal/config`. Each rule is declared by the
module that owns the key, and the parse is generated from those declarations:

- `production_required` makes a key a production boot requirement.
- `refused_in_production` makes a `true` bool a production boot refusal. That is
  what `ggg/system/identity-dev` declares for `DEV_AUTH_BYPASS`, so removing the
  dev adapter removes the key and its refusal together.
- `derivation` names a pure function in a leaf package the declaring module owns
  and the inputs to pass it, and fills the key when the operator supplies
  nothing. `ggg/system/identity-clerk` declares one for `CLERK_FRONTEND_API_URL`
  pointing at `internal/identity/clerkurl.FrontendAPIURL(APP_ENV, APP_URL)`.

A module that does not declare a key reads it with `cfg.Value("KEY")` /
`cfg.BoolValue("KEY")` rather than the typed field: the field belongs to the
declaring module and leaves with it, while the key-shaped read still compiles
and resolves to the empty value.

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

**Tests** (what `e2e/playwright.config.ts` uses). It sets no database at all —
the address comes from `e2e/generated/database.ts`, rendered by `ggg sync` from
the test environment's selected adapter and published port:

```sh
APP_ENV=test PORT=18080 DEV_AUTH_BYPASS=true \
TEST_NOW=2026-01-15T00:00:00Z
```

**Production:** `APP_ENV=production`, a real `DATABASE_URL` (e.g. Neon), the
four `CLERK_*` keys, `CLERK_PORTAL_URL`, plus whatever optional services you
use. `LOG_LEVEL` defaults to `info` and logs go out as JSON. See
[Deployment](/docs/deployment).
