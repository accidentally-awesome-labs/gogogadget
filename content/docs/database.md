---
title: Database
description: Postgres, goose migrations, sqlc codegen, and the conventions that keep queries honest.
section: Core
weight: 6
---

The database is Postgres 16, reached through `pgx/v5`'s pool (max 10
connections, opened by `internal/db.Open`). Schema changes are goose
migrations; queries are hand-written SQL compiled to typed Go by sqlc. There
is no ORM and no runtime query magic.

## Migrations

Migrations live in `internal/db/migrations/` and are embedded in the binary
with `go:embed`. **They run on boot** — `db.Migrate` is called before the
server accepts traffic — so deploys are always migrated, and goose's own
advisory lock handles multiple instances booting at once.

To add one, create the next numbered file with an `Up` and a `Down`:

```sql
-- internal/db/migrations/0002_add_widgets.sql

-- +goose Up
CREATE TABLE widgets (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS widgets;
```

It applies on the next boot (locally: restart `make dev`). `db.MigrateDown`
exists for test teardown. The goose CLI is pinned in `go.mod` (`go tool goose`)
if you ever need manual inspection.

## The sqlc loop

All queries are plain SQL files in `internal/db/queries/`, compiled by
`make generate` (which runs `go tool sqlc generate`) into typed methods in
`internal/db/sqlc/`. `sqlc.yaml` at the repo root:

```yaml
version: "2"
sql:
  - engine: "postgresql"
    schema: "internal/db/migrations"   # sqlc type-checks against your DDL
    queries: "internal/db/queries"
    gen:
      go:
        package: "sqlc"
        out: "internal/db/sqlc"
        sql_package: "pgx/v5"
        emit_json_tags: true
```

The loop: add a `-- name: DoThing :one` (or `:many` / `:exec`) query to the
right file → `make generate` → call `q.DoThing(ctx, params)` from handlers.
Because sqlc reads the migrations as its schema, a query that references a
missing column fails generation, not production. Generated `sqlc/` files are
never edited by hand (see [Architecture](/docs/architecture)).

## Conventions

- **One query file per table** — `users.sql`, `orgs.sql`, `memberships.sql`,
  `subscriptions.sql`, `webhook_events.sql`, `audit.sql`, `projects.sql`,
  `jobs.sql`, `api_tokens.sql`.
- **Every UPDATE sets `updated_at = now()`** — no silent staleness.
- **Emails are `citext`** — case-insensitive, with a unique constraint; the
  extension is created in `0001_init.sql`.
- **Org-scoped resources always filter by `clerk_org_id`** in the WHERE
  clause (`UPDATE projects … WHERE id = $1 AND clerk_org_id = $2`), so a
  cross-org ID is a 404, never a data leak.
- **Webhook idempotency** goes through the shared `webhook_events` table:
  insert `ON CONFLICT (id) DO NOTHING` — a conflict means the delivery is a
  replay and processing stops.

## Seeds and resets

```sh
make seed       # load internal/db/testdata/seed_dev.sql (demo user/org + 4 projects)
make db-reset   # compose down -v, up -d db, then cmd/seed -reset: drop/create
                # the DATABASE_URL database, migrate, load the fixture
```

`cmd/seed -reset` is also what the e2e harness uses to build its database
(see [Testing](/docs/testing)).

## Search

Full-text search is **Postgres**, not a vendor: a generated `tsvector` column
plus a GIN index. `projects.search_tsv` is the canonical pattern:

```sql
ALTER TABLE projects ADD COLUMN search_tsv tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', name)) STORED;
CREATE INDEX projects_search_idx ON projects USING GIN (search_tsv);
```

The `simple` dictionary is deliberate: user data may be any language, and no
wrong stemming beats wrong stemming. Queries combine FTS with an ILIKE
fallback so partial tokens (`check` → checklist) still match, and rank when
the FTS query scores:

```sql
WHERE ($2::text = '' OR search_tsv @@ websearch_to_tsquery('simple', $2) OR name ILIKE '%' || $2 || '%')
ORDER BY CASE WHEN $2::text = '' THEN 0
              ELSE COALESCE(ts_rank(search_tsv, websearch_to_tsquery('simple', $2)), 0) END DESC,
         created_at DESC
```

`websearch_to_tsquery` gives users Google-ish syntax for free (quotes,
`OR`, `-negation`). Postgres FTS is the documented answer until real scale;
swapping later means replacing one query file, not a pipeline.


## Test databases

Integration tests use `internal/db/testdb`, which gives **every package its
own database** named `gogogadget_test_<name>` on the server from
`TEST_DATABASE_URL` (default `postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable`).
The database is dropped, recreated, and migrated at `testdb.Open`, so runs
always start clean and `go test ./...` packages can run in parallel without
one package's teardown nuking another's fixtures. Tests self-skip when the
server is unreachable — CI provides it.

## The job queue, briefly

The `jobs` table doubles as the background-job queue. Claiming a job is a
single atomic query: `SELECT … FOR UPDATE SKIP LOCKED` picks the oldest
runnable job, and the same statement pushes its `run_at` five minutes into
the future — a **visibility timeout**. If the worker crashes mid-job, the job
reappears after five minutes instead of being lost or double-claimed
mid-flight; `SKIP LOCKED` makes concurrent workers (even on multiple nodes)
safe. Details: [Background jobs](/docs/background-jobs).
