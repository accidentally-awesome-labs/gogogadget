# Task A report — `ggg create resource` emits the projects-pattern slice

## Outcome

`ggg create resource NAME --scope user|org|platform [--table T] [--route R] [--api] [--admin]
[--search] [--no-ui]` now writes one complete, compiling, request-serving vertical slice into
the mutable project registry. `--api`, `--admin`, `--search` and `--no-ui` are all live; two
combinations that could only produce broken or unsafe output are refused before any write.

The slice (kind `workflow`, id `<namespace>/workflow/<slug>`) carries the union of the shipped
`page/projects` + `workflow/projects` pattern:

| Emitted | Contents |
| --- | --- |
| `internal/db/queries/<table>.sql` | sqlc-annotated CRUD for the table; every UPDATE sets `updated_at = now()`; `DELETE` is `:execrows` so a no-match is a 404 |
| immutable migration (ledger + `payload/<snake>.sql`) | `id`, tenant column per `--scope` with `ON DELETE CASCADE`, `name`, `created_at`, `updated_at`, tenant index |
| `internal/web/workflow_<snake>.go` | read page (search + pager + `?edit=<id>`), create/update/delete, `wantsFragment` rule, 422 form re-render, `hx-delete` row removal, `Navigate`/`Toast`, `identity` guard per `--scope`; plus admin / API / search wiring when asked |
| `internal/web/templates/<slug>.templ` | table card, both empty states, shared create/edit form, `ui.ConfirmAction` delete, `data-testid` on every asserted element, every string through `i18n.T(ctx, "<slug>.key")`, `ui` components only |
| `internal/web/workflow_<snake>_test.go` | unit test for the shared name rule |
| declarations | `runtime.routes` + matching `claims.routes`, `runtime.queries` + `claims.queries`, `runtime.navigation`, `runtime.visual`, `locales` (full `en`+`es`), `openapi` (when `--api`), `data` + `claims.data`, `tests.go_packages`, `requires`, `dependencies` |

## Files changed

Added:
- `internal/gggcli/create_resource.go` — `resourceSpec` (name derivation + flag validation), `buildResourceModule`, and every manifest declaration builder (`requirements`, `routes`, `queries`, `navigation`, `visual`, `data`, `tests`, `claims`), plus `unexported`/`sentenceCase`.
- `internal/gggcli/create_resource_source.go` — the emitted bodies: `queriesSQL`, `migrationSQL`, `transportGo` (+ imports/validator/read-page/form-error/create/update/delete/admin/API/search parts), `transportTestGo`, `templatesTempl`, `locales`, `openapi`, `formatGo`.
- `internal/gggcli/create_resource_test.go` — 12 tests (below).
- `registry/testdata/registry/modules/workflow/example-resource/` — the compile-verified example closure (6 files: `module.json` + 5 payloads), byte-for-byte the generator's output for `--scope org --api --admin --search`.

Modified:
- `internal/gggcli/create.go` — the `case "resource":` arm now delegates to `buildResourceModule`; `materializeManifest` classifies `_test.go` payloads as `class: "test"`, rewrites module paths for them, and returns the completed manifest (previously the caller got a manifest with an empty `files` list, so the plan described a module that owned nothing).
- `internal/modkit/example.go` — `registry validate` now runs `go tool sqlc generate` when a closure installs an `internal/db/queries/*.sql`, and restores the captured `internal/db/sqlc/` bytes after removal (`readDirectorySnapshot`/`restoreDirectorySnapshot`, `sqlcOutputDir`, `isQueryPath`).
- `internal/modkit/example_test.go` — expected closure kinds gain the second `workflow`.
- `registry/testdata/registry/workflows.json` — publishes the new fixture.
- `registry/modules/system/modkit/module.json` — declares the three new `internal/gggcli` files.
- generated aggregates + `gogogadget.lock.json` + `registry.snapshot.json` — refreshed by `ggg registry build && ggg sync --offline` (lock-identity headers only).

## Decisions

1. **`registry validate` had to learn sqlc.** The brief requires the fixture to prove "the
   generated Go/templ/SQL compiles". The validator ran `templ generate` but never sqlc, so no
   module that owns a table could ever be compile-proved: its handlers would call query methods
   that do not exist as Go. The alternative — generating handlers that re-embed the SQL as Go
   strings — would bake a second query mechanism beside sqlc into every generated resource, which
   the house rule ("one query file per table") forbids. sqlc needs no database and runs in ~0.9s.
   `internal/db/sqlc/` is tool output (`isExternalToolOutput`), so snapshot-and-restore around the
   closure keeps the byte-for-byte claim a statement about authored source.

2. **The emitted Go never names an sqlc model type.** Only `sqlc.<QueryName>Params` and the
   method names appear, both derived from the `-- name:` annotations this generator itself writes.
   Rows are consumed by field access on an inferred `row` and mapped onto an explicit
   `templates.<Exp>Item` DTO. Consequence: `--table` may be any name without the generated Go
   depending on sqlc's pluralisation, and the `templates` package imports nothing generated.

3. **One tenant parameter name.** The SQL uses `sqlc.arg(tenant)` rather than
   `sqlc.arg(org_id)`/`sqlc.arg(user_id)`, so the generated Params field is `Tenant` for every
   scope and the handler bodies have one shape instead of three. The only remaining scope
   branch is sqlc's arity rule (no Params struct for a single-parameter query), which is why
   platform-scoped calls pass bare values.

4. **`--no-ui --admin` is a usage error.** The admin surface *is* a UI surface; accepting both
   would either emit the templates `--no-ui` promised to omit or declare admin routes with no
   renderer.

5. **`--api` with `--scope user` is a usage error.** `RequireAPIToken` puts only an organization
   in the context (`internal/api/tokens.go:136`), so a user-scoped table has no tenant on the
   JSON transport. Emitting it would list every user's rows to any token holder. Not in the brief,
   but the alternatives were a nil dereference or a cross-user data leak.

6. **`--admin` on a tenant-scoped table adds `ListAll…`/`CountAll…`.** A staff surface filtered to
   the operator's own organization is not a staff surface. A platform table already has no filter,
   so it reuses the ordinary list.

7. **`data` declares `export: false` and cascade only where the schema cascades.** `internal/jobs`
   and `internal/web` integration tests assert every `export: true` table has a collector, and
   `internal/db` asserts every declared `cascade` has a real FK path. A freshly generated table has
   no redaction DTO, so claiming exportability would break `make check` in the operator's project
   the moment they ran `ggg create resource`. Tenant scopes get the real `ON DELETE CASCADE` FK and
   declare cascade; platform retains.

8. **Route ids, query names, nav ids, locale keys and DOM ids all come from one `resourceSpec`.**
   They cannot disagree, and the tests assert the declarations match the emitted source (every
   declared sqlc method exists in the query file; every `i18n.T` key exists in both catalogs; every
   api route has an OpenAPI operation).

9. **Emitted Go is run through `go/format`.** A generator that ships non-canonical source makes
   the operator's first `gofmt` a diff. `TestCreateResourceEmitsCanonicalGo` parses and
   format-checks every reachable flag shape across all three scopes.

10. **Fixture update mode.** `GGG_UPDATE_RESOURCE_FIXTURE=1 go test ./internal/gggcli -run
    TestCreateResourceMatchesExampleFixture` rewrites the fixture after a deliberate generator
    change; otherwise the test asserts byte equality in both directions (no extra, no missing file).

## Tests added (`internal/gggcli/create_resource_test.go`)

- `TestCreateResourceDefaultsTableAndRoute` — `snake(NAME)+"s"` and `/app/<kebab>s`.
- `TestCreateResourceHonoursTableAndRouteOverrides` — `--table`/`--route` move the query file, every route pattern and every query's declared table.
- `TestCreateResourcePlainShapeIsAServableSlice` — full declaration set for the no-flag shape, and every declared sqlc method present in the emitted SQL with UPDATE setting `updated_at`.
- `TestCreateResourceLocalesCoverEveryTemplateKey` — every `i18n.T` key in the template has `en` and `es`, no unread key, matching placeholders.
- `TestCreateResourceAPIAddsTheJSONTransport` — four api routes, api scopes, CSRF exemption with a reason, an OpenAPI operation per route, the `api`/`openapi-contract` requirements, `api.WriteJSON`/`WriteError` in the transport.
- `TestCreateResourceAdminAddsTheStaffReadSurface` — admin route/scope/pattern, admin nav, `ListAll`/`CountAll` queries, admin page renderer, and that none of it leaks into the plain shape.
- `TestCreateResourceSearchWiresTheIndex` — index/delete helpers, `search.Document` collection, call sites, the search requirement, and that the plain shape does not require the seam.
- `TestCreateResourceNoUIDropsTheBrowserSurface` — no template/routes/nav/visual/locales, data kept, and `--no-ui --api` emits only the JSON transport.
- `TestCreateResourceRefusesNoUIWithAdmin` — refusal text + exit 2.
- `TestCreateResourceRefusesAPIForUserScope`, `TestCreateResourceRefusesUnknownScope`.
- `TestCreateResourceEmitsCanonicalGo` — 8 flag shapes × 3 scopes: emitted Go parses and is gofmt-canonical.
- `TestCreateResourceMatchesExampleFixture` — the fixture is exactly what the generator emits, and its manifest passes `modkit.ValidateManifest`.
- `TestCreateResourceWritesTheProjectRegistry` — end-to-end `runApp("create","resource","Widget","--scope","org","--api","--search")` against a temp project: every payload on disk, index rebuilt, `modkit.LoadCatalog` publishes `acme/workflow/widget`.

## Commands run

```
$ go tool sqlc generate            # determinism probe on the untouched tree
real 0m0.926s   → git status --porcelain internal/db/sqlc: (empty)

# generated slice installed by hand into the repo to compile-check before wiring the fixture
$ go tool templ generate -f internal/web/templates/example-resource.templ   → (no output)
$ go build ./internal/...                                                    → (no output)
$ go vet ./internal/web/...                                                  → (no output)
$ go test ./internal/web -run TestExampleResourceNameValidation -count=1
ok  	github.com/gogogadget/gogogadget/internal/web	0.369s
$ go test ./internal/web/templates -count=1        # design-system test over the emitted templ
ok  	github.com/gogogadget/gogogadget/internal/web/templates	0.348s
# (all six scratch files then removed and sqlc regenerated; tree clean)

$ GGG_UPDATE_RESOURCE_FIXTURE=1 go test ./internal/gggcli -run TestCreateResourceMatchesExampleFixture -count=1 -v
    create_resource_test.go:492: rewrote 6 fixture file(s) under registry/testdata/registry/modules/workflow/example-resource
--- PASS: TestCreateResourceMatchesExampleFixture (0.00s)

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 5190236e4be8539bac23cd90a2b2bef4f6772f7cffaff929e128e069b1083196
  update    lock       gogogadget.lock.json
$ go run ./cmd/ggg sync --check --offline
registry 5190236e4be8539bac23cd90a2b2bef4f6772f7cffaff929e128e069b1083196     # no drift

$ go test ./internal/gggcli ./internal/modkit -count=1
ok  	github.com/gogogadget/gogogadget/internal/gggcli	0.393s
ok  	github.com/gogogadget/gogogadget/internal/modkit	7.040s

$ go vet ./internal/gggcli ./internal/modkit                                  → (no output)
$ gofmt -l internal/gggcli internal/modkit                                    → (no output)

$ go run ./cmd/ggg registry validate
...
ggg/workflow/example-resource
  closure: ggg/workflow/example-resource
  installed 5 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/web
  removed; 1844 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 1 migration(s) retained
  info     example_closure_verified workflow closure ggg/workflow/example-resource: installed 5 file(s),
           regenerated 30, compiled, 1844 tree entries restored byte for byte,
           retained migration(s) internal/db/migrations/0027_ggg_example_resource.sql
  info     example_closure_verified element closure ...            (all 8 closures verified)
exit 0
```

That last run is the real proof: the emitted SQL went through sqlc, the emitted templ through
templ, the whole derivative through `go build ./...`, the emitted test through `go test`, and the
tree came back byte for byte after removal with only the immutable migration retained.

## Commit

`a1a8cfbb8091cabe266934f20b8c2bd2585701e8` — *feat(gggcli): make `ggg create resource` emit the
projects-pattern slice*. The report was amended in, so this is the hash `git log -1`
reports in the worktree; the pre-amend hash was `4114c9d`.

## Concerns

1. **`registry validate` now runs sqlc for query-owning closures.** It adds ~1s per such closure
   and one new failure mode (an sqlc error surfaces as `sqlc generate: …`). The generated package
   is snapshot-restored after removal, so a stale committed `internal/db/sqlc/` cannot make the
   byte-for-byte assertion fail — but it also means the validator no longer notices sqlc drift.
   `ggg sync --check` and `make check` still do.

2. **Only the `--scope org --api --admin --search` shape is compile-proved for real.** The other
   eleven reachable shapes are covered by parse + gofmt + declaration assertions, not by a
   compiler. Platform scope is the one whose sqlc call arity differs (bare args instead of Params
   structs); if a second example closure is ever affordable, that is the shape worth adding.

3. **`runtime.visual` declares a baseline the operator has not captured.** A generated resource
   adds a visual surface, so the first `make visual` after `ggg create resource` fails until
   `make visual-update`. That matches how every other new page behaves, but it is a sharp edge on
   a generated module.

4. **No `content/docs/` page describes `ggg create` yet**, so there was nothing to update; the
   flags are documented by the command table (`internal/gggcli/table.go`) that feeds `ggg help`.
   When the task-8/10 docs recast lands, the `--api`/`--admin`/`--search`/`--no-ui` semantics and
   the two refusals should go into it.

5. **`materializeManifest` now returns the completed manifest.** That fixed a real latent defect —
   `previewCreate` was validating a manifest with no files — but it changes what every `ggg create`
   kind reports in its plan, not just `resource`.
