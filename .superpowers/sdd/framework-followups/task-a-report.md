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

## Commits

- `25bb87f81373146962b7fbba8b1d0cd1e030f8aa` — *feat(gggcli): make `ggg create resource` emit the
  projects-pattern slice*. All code, tests, the example closure, the validator change and the
  refreshed generated aggregates.
- One follow-up commit recording these hashes in this report (a report cannot contain its own
  commit's hash).

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


---

# Fix round 1 — response to `task-a-review-findings.md`

Verdict was spec compliance WARN, task quality FAIL, with two Criticals in the *generated output*.
Both were real. Every item is fixed: C1, C2, I1, I2, M1, M2. The "not in scope" pager locale gap
was left alone.

## C1 — `--scope platform` emitted unguarded app-scope writes

**The finding was correct and it was the worse of the two holes I had reasoned about.** I refused
`--api --scope user` for exactly this class of problem and then shipped the same problem on the
browser transport: `POST`/`POST`/`DELETE` at `modkit.RouteApp`, no tenant predicate in the
statements because a platform table has none, and no staff check because the app middleware group
is `requireAuth → requireNotDisabled → requireOrg → loadPlan`. `ggg create resource Banner --scope
platform` handed every authenticated member of every organization create, rename and delete on a
global table.

Fixed by moving a platform table's mutations into the group that can authorise them rather than by
refusing the shape — a platform resource is a real thing (`announcements`, `feature_flags`,
`content_entries` all are), it just is not tenant-owned, so its writes belong to staff:

- New `resourceSpec.writeBase()` / `writeScope()` / `writePolicy()` / `writeLayout()`. For a
  tenant-scoped table they are the app route, `RouteApp`, `RoutePolicy{}`; for a platform table
  `/admin/<plural>`, `RouteAdmin`, `RoutePolicy{AdminWrite: true}`.
- The staff page is no longer optional on a platform table — it is the write surface — so
  `resolveResourceSpec` sets `spec.admin` for `platform && uiRead()`. `--no-ui --admin` still
  refuses on the operator's own flags, so the implication cannot fire behind that check.
- Exactly one surface owns the writes, and only that one carries the form and the `?edit=<id>`
  lookup. `transportReadPage`/`transportAdmin` collapsed into one `transportListSurface(admin
  bool)`; `writable := admin == (r.tenantColumn == "")`.
- The view struct's `ReadOnly` became `Writable` + `Admin`, and the templ gained one predicate,
  `<lows>Writable(ctx, d)`, that both pages and the table row actions use. Inside the admin layout
  it additionally requires `templates.AdminWrite(ctx)` — the shipped idiom from
  `admin_announcements.templ` / `admin_flags.templ` — so a support-role viewer gets the table and
  not controls that would 403. Default deny: an unset context renders read-only.
- `<Exp>FormData` gained `WriteURL`, set in one place (`render<Exp>FormError`, and the list
  surface) so the form action and the cancel link follow the write scope instead of the page URL.

**The same hole existed on the JSON transport and the review did not name it.** `api-write` is
token-authenticated with no staff check and no scope that could add one, so a platform table's API
writes were the identical unguarded global mutation. `--api --scope platform` now emits the read
route only (`apiWrite() = api && tenantColumn != ""`), and its OpenAPI slice documents one
operation. A global *read* is what "platform" means, so that stays.

Observed for `--scope platform --api`:

```
GET    /api/v1/banners      scope=api-read  admin_write=false -> handleAPIListBanners
GET    /app/banners         scope=app       admin_write=false -> handleBanners
GET    /admin/banners       scope=admin     admin_write=false -> handleAdminBanners
POST   /admin/banners       scope=admin     admin_write=true  -> handleBannerCreate
POST   /admin/banners/{id}  scope=admin     admin_write=true  -> handleBannerUpdate
DELETE /admin/banners/{id}  scope=admin     admin_write=true  -> handleBannerDelete
```

## C2 — `--search` filed documents under the acting organization for every scope

Also correct, including that my comment claiming the seam forced it was false —
`search.Document.Fields` and `search.Query.Filters` exist.

- **user scope**: `index<Exp>(ctx, tenantID, userID, id, name)` sets
  `Fields: map[string]string{"user_id": userID}`, and the call sites pass `user.UserID`. The tenant
  stays the organization because `search_documents.tenant_id` has a foreign key to `orgs` — that is
  the real constraint — and the owner is what a caller narrows on with `search.Query.Filters`.
- **platform scope**: `--search` is refused. There is no correct tenant, and filing a global row
  under whichever organization's staff member wrote it would both misattribute it and hide it from
  every other organization.
- The false comment is gone; `transportSearch`'s doc comment now states the FK, why the owner is a
  field, and why platform is refused.

## I1 — the refusal matrix is recorded, not just commented

`task-a-brief.md`'s flag list now carries all four behaviours (`--no-ui --admin`, `--api --scope
user`, `--api --scope platform` read-only, `--search --scope platform`) plus the platform
write-scope rule, each with its one-line reason. The report's decision list already had the
`--api --scope user` reasoning and now has the rest.

## I2 — locale coverage across the flag shapes

`TestCreateResourceLocalesCoverEveryTemplateKey` is replaced by
`TestCreateResourceLocalesCoverEveryUsedKey`, which runs 5 flag shapes × 3 scopes (skipping the
unreachable ones through a shared `reachableResourceShape` helper) and builds the used-key set as
*template keys ∪ navigation label keys*. That removes the `HasSuffix(key, ".nav")` exemption that
never matched `admin_nav`, so both `admin_title` and `admin_nav` are now checked in `en` and `es`,
and the "declared but unread" direction is exact rather than approximate.

## M1 — the form struct follows the form

`needsFormInput()` (UI writes) is now separate from `needsValidator()` (UI writes or API writes),
so `--no-ui --api` emits the shared validator both transports run and no `form:"name"` struct that
nothing decodes.

## M2 — the duplicated sqlc path fails loudly

`assertSQLCOutputDir` reads `sqlc.yaml` and refuses when it no longer names `sqlcOutputDir`
(`internal/db/sqlc`) or `sqlcQueryDir` (`internal/db/queries`), and the validator calls it — plus
asserts the captured baseline is non-empty — before running sqlc for a query-owning closure. A
moved output directory would otherwise have made the post-removal restore a silent no-op and let
the generated package leak past removal. Covered by
`TestExampleValidatorRefusesAMovedSQLCOutput`, which checks this repository passes and that a
moved output, a renamed queries directory and a missing config each fail.

## Tests added

- `TestCreateResourcePlatformScopeKeepsWritesOutOfTheAppGroup` — for `{}`, `{API}`, `{Admin}` on
  `--scope platform`: no `RouteApp` route has a non-GET method, no `RouteAPIWrite` route exists at
  all, the three mutations are `RouteAdmin` + `AdminWrite` under `/admin/banners`, the staff page
  and its nav entry exist however `--admin` was passed, `--api` documents only the list — and a
  tenant-scoped table still keeps its create in the app group.
- `TestCreateResourceSearchScopesUserRowsByOwner` — the user-scope helper signature, the `user_id`
  field, the call site passing `user.UserID`; and that the org-scope helper files no `user_id` it
  cannot mean.
- `TestCreateResourceRefusesSearchForPlatformScope` — refusal text + exit 2.
- `TestCreateResourceLocalesCoverEveryUsedKey` — as above.
- `TestExampleValidatorRefusesAMovedSQLCOutput` — as above.

`TestCreateResourceEmitsCanonicalGo` now skips through `reachableResourceShape`, so it covers
8 shapes × 3 scopes minus the four refused combinations.

## Commands run

```
# all three scopes compiled for real, not just parsed: emitted slice installed by hand,
# sqlc + templ generated, built, vetted, design-system test run over the emitted templ
$ go tool sqlc generate && go tool templ generate -f internal/web/templates/banner.templ
$ go build ./internal/...            → BUILD_OK      (--scope platform --api)
$ go vet ./internal/web/...          → VET_OK
$ go test ./internal/web/templates   → ok            (design-system rules over the emitted templ)
$ ... same for --scope user --search → BUILD_OK, ok
# both reverted, sqlc regenerated, tree clean

$ GGG_UPDATE_RESOURCE_FIXTURE=1 go test ./internal/gggcli -run TestCreateResourceMatchesExampleFixture
ok      # org-scope output did change (Writable/Admin/WriteURL, search comment), so the fixture
        # was regenerated; module.json changed in its two payload digests only — no org route moved

$ go test ./internal/gggcli ./internal/modkit -count=1
ok  github.com/gogogadget/gogogadget/internal/gggcli   0.439s
ok  github.com/gogogadget/gogogadget/internal/modkit   7.765s
$ go vet ./internal/gggcli ./internal/modkit           → clean
$ gofmt -l internal/gggcli internal/modkit             → clean

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline
registry 98b29a3b1afa7358798a03ee83661658ba7c8fa2c5893b83522ffbdb59c8f76b   # no drift

$ go run ./cmd/ggg registry validate
  info  example_closure_verified workflow closure ggg/workflow/example-resource: installed 5 file(s),
        regenerated 30, compiled, 1844 tree entries restored byte for byte,
        retained migration(s) internal/db/migrations/0027_ggg_example_resource.sql
  ... all 8 closures verified, exit 0
```

## Remaining concerns after this round

1. **The compile proof still covers one shape through `registry validate`.** Platform and user
   scope were compiled, vetted and design-system-tested by hand in this round (output above), which
   is stronger than the parse-only coverage I reported last time, but it is not a standing gate. A
   second example closure at `--scope platform` would make it one; it needs a second table and a
   second migration in the fixture registry.
2. **`--api --scope platform` now emits one route where the flag reads like four.** It is
   documented in the brief, the generated comment and the openapi slice, and the alternative was
   either an unguarded global write or refusing a useful read. Worth a line in the docs recast.
3. **`--scope platform` implies `--admin`.** Passing `--admin` explicitly there is a no-op rather
   than an error. Refusing it would be more pedantic than helpful, but it does mean the flag's
   effect depends on the scope.
4. Items 3–5 of the original concern list (visual baseline, no `content/docs` page for `ggg
   create`, `materializeManifest` now returning the completed manifest) are unchanged.


---

# Fix round 2 — response to the re-review's `new_breakage`

Round 1 addressed all six original findings but introduced six new ones. All six are fixed:
N1 (Critical), N2 (Important), N3, N4, N5, N6.

## N1 — `--no-ui --api --scope platform` emitted a slice that did not compile

Correct, and it was mine: the M1 split moved the validator onto `needsValidator()` and left the
emitted `_test.go` payload on `hasHandlers()`. For that one shape they disagree
(`hasHandlers()=true`, `needsValidator()=false`), so the emitted test called
`validate<Exp>Name` and nothing defined it.

- The test payload and `tests()` are both gated on `needsValidator()` now. The unit test exercises
  the validator, so it travels with the validator, not with the handlers.
- **The gate that let it through is closed.** `TestCreateResourceEmitsCanonicalGo` only parsed and
  gofmt-checked, and an undefined identifier is a type error. New
  `TestCreateResourceResolvesEverySymbolItEmits` walks the AST of every emitted Go payload for
  every reachable shape (8 flag combinations × 3 scopes minus the four refused) and asserts:
  - every identifier carrying the slice's own vocabulary — bare, or `s.<name>` on the server
    value — is declared by the slice; qualified names (`sqlc.…`, `templates.…`, `s.q.…`) belong to
    other packages and are excluded;
  - every `runtime.routes[].Handler` the manifest declares exists as an emitted method;
  - the `_test.go` payload and `tests.go_packages` are present together or absent together.

  Verified non-tautological: reverting only the gate reproduces the exact defect —
  `platform {…NoUI:true API:true}: …/workflow_widget_test.go.txt references validateWidgetName,
  which the slice never declares` — and it passes with the fix restored.
- **And the shape is now on a compile-checked path by hand:** installed into the repo, `sqlc
  generate`, `go build ./internal/...`, `go vet ./internal/web/...` — all clean. Its emitted
  transport imports exactly `net/http`, `strconv`, `time`, `internal/api`, `internal/db/sqlc`,
  declares no test package, no identity requirement and no pgx dependency.

## N2 — platform writes audited under the acting staff member's organization

Correct, and the reviewer's tracing of *why* the shipped convention exists (`/app/activity` is
org-scoped and reads the audit table, so a global action would land in one tenant's feed) is the
part I had missed.

- `auditOrg()` returns `""` when `tenantColumn == ""`, so the three platform mutations emit
  `s.logAudit(ctx, "", user.UserID, …)` — matching `workflow_admin_flags.go`,
  `workflow_admin_content.go`, `workflow_admin_media.go`, `workflow_admin_schedules.go` and
  `workflow_admin_user_governance.go`.
- `identityDeclarations` no longer declares `org` for an audit on a table with no tenant, so the
  now-unused local is gone rather than left to a compile error.
- A tenant-scoped row still audits under its organization, which is correct there.
- `TestCreateResourceAuditsPlatformWritesWithoutATenant` pins both directions and asserts the
  platform transport contains no `org.OrgID` and no `org := identity.OrgFrom(ctx)` at all.

## N3 — over-declared dependency and requirement on the same shape

Fixed at the root the finding named: the declaration and the import now read the *same*
predicate rather than two that can drift.

- New `usesIdentity() = uiWrite() || apiWrite() || (apiRead() && tenanted)` is read by both the
  import list and the `ggg/system/identity` requirement.
- The pgx dependency moved onto `needsRowLookup()`, which is what gates the `errors`/`pgx`
  imports.
- `ggg/system/security` and `ggg/system/server` stay on `hasHandlers()`, which is accurate: any
  route needs the mux and the middleware chain.

## N4 — the `--api --scope platform` narrowing is now visible

`resourceSpec.diagnostics()` emits one info `modkit.Diagnostic`
(`code: resource_api_read_only`, module id, `path: /api/v1/<plural>`) naming the dropped create,
update and delete, why they are dropped, and where the mutations are served instead. It rides the
planner's own `Plan.Diagnostics` slot, so it shows in the preview and in the JSON envelope.
`buildCreateFiles` gained a diagnostics return and `buildResourceModule` now returns a
`resourceSlice` bundle rather than four positional values.

## N5 — empty-state copy told the reader to use a form that is not there

The finding named the platform app page; the same string was also wrong on a tenant-scoped
table's read-only staff page, so the fix is render-time rather than scope-time.

- New `<lows>EmptyBody(ctx, d)` picks `<slug>.empty_body` when the surface is writable and
  `<slug>.empty_body_readonly` ("No <things> have been created yet." / "Todavía no se ha creado
  ningún <thing>.") when it is not.
- Both keys are in both catalogs and both are read by the template, so the locale test's exact
  used-vs-declared check still holds.

## N6 — dead wrapper

`payloadOf` deleted; all callers use `payload`.

## Report correction

The reviewer is right that round 1's report listed "search comment" among the org fixture's
changes. That change was generator-only; the org fixture's emitted search section was
byte-identical. Round 2's fixture diff is the templ payload (the empty-body helper) plus its
digest in `module.json` — the org transport is byte-identical again, since the audit change
affects platform only.

## Commands run

```
# the shape N1 broke, compiled for real
$ go tool sqlc generate && go build ./internal/... && go vet ./internal/web/...
N1_SHAPE_BUILD_OK                      # --no-ui --api --scope platform
   tests=[] diagnostics=1
   requires ['ggg/system/api','ggg/system/database','ggg/system/security','ggg/system/server','ggg/workflow/openapi-contract']
   deps []                             # pgx no longer declared

# platform and user scope re-probed after the audit and empty-body changes
$ ... --scope platform --api           → PLATFORM_BUILD_OK, vet clean, templates test ok
$ ... --scope user --search --admin    → USER_BUILD_OK, vet clean, templates test ok,
                                         go test -run TestNoteNameValidation ok
# every probe reverted, sqlc regenerated, tree clean

# the new gate reproduces N1 when the fix is reverted
$ (revert the test-payload gate) go test -run TestCreateResourceResolvesEverySymbolItEmits
--- FAIL: platform {…NoUI:true API:true}: …/workflow_widget_test.go.txt references
    validateWidgetName, which the slice never declares
$ (restore) → ok

$ GGG_UPDATE_RESOURCE_FIXTURE=1 go test ./internal/gggcli -run TestCreateResourceMatchesExampleFixture
ok    # templ payload + its digest only; org transport byte-identical

$ go test ./internal/gggcli ./internal/modkit -count=1
ok  github.com/gogogadget/gogogadget/internal/gggcli   0.429s
ok  github.com/gogogadget/gogogadget/internal/modkit   6.577s
$ go vet ./internal/gggcli ./internal/modkit; gofmt -l internal/gggcli internal/modkit   → clean

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline
registry 67888f4461e5e495f129de36fac62c56b044ddf10d893071c7fd0e9024da8ceb   # no drift

$ go run ./cmd/ggg registry validate
  info  example_closure_verified workflow closure ggg/workflow/example-resource: installed 5 file(s),
        regenerated 30, compiled, 1844 tree entries restored byte for byte,
        retained migration(s) internal/db/migrations/0027_ggg_example_resource.sql
  ... all 8 closures verified, exit 0
```

## Remaining concerns after round 2

1. **Type checking is still structural, not a real type check.** `TestCreateResourceResolvesEvery`
   `SymbolItEmits` catches the undefined-symbol and missing-handler classes across all 20
   reachable shapes, and it catches the exact defect that shipped — but it is an AST cross-check,
   not `go/types`. A wrong argument count or a wrong type on a call it can resolve would still
   need a compiler. Standing compiler coverage remains the org closure plus the by-hand probes
   recorded above; a second example closure at `--scope platform` is what would make it automatic.
2. **`--scope platform` implies `--admin`**, so passing `--admin` there is a no-op. Unchanged from
   round 1.
3. The `assertSQLCOutputDir` check is substring-based, as the reviewer noted, so a cosmetic
   `sqlc.yaml` requoting would false-positive. It fails closed, which is the right direction.
4. Unchanged from round 1: a generated resource declares a `runtime.visual` baseline nobody has
   captured; no `content/docs/` page describes `ggg create` yet; `materializeManifest` returning
   the completed manifest changes what every `ggg create` kind reports in its plan.


---

# Fix round 3 — standing compiler coverage for the narrowed shape

Scoped to the concern I named at the end of round 2: the undefined-symbol class had bitten twice
in this task, and standing compiler coverage was one shape (org) plus by-hand probes. It is now
automatic for two shapes.

## What was added

`registry/testdata/registry/modules/workflow/example-feed/` — a second installable closure,
byte-for-byte what the generator emits for `--scope platform --no-ui --api`. Four files: the
manifest, the query payload, the migration payload, the transport payload. It has its own table
(`example_feeds`) and its own migration, which was the work identified as blocking this.

That shape was chosen because it is the one that actually broke (N1) and because it exercises the
disjoint half of the generator:

| | `example-resource` (`--scope org --api --admin --search`) | `example-feed` (`--scope platform --no-ui --api`) |
| --- | --- | --- |
| templates | app page, staff page, table, form | none |
| app routes | GET + POST/POST/DELETE at app scope | none |
| staff routes | GET | none |
| JSON transport | list/create/update/delete | list only |
| validator + unit test | emitted | not emitted |
| audit organization | `org.OrgID` | n/a (no writes) |
| locales / navigation / visual | full en+es, app+admin nav, baseline | none |
| declared dependencies | pgx | none |
| declared requirements | database, i18n, identity, organizations, security, server, api, openapi-contract, search | database, security, server, api, openapi-contract |
| declared tests | `internal/web` | none |

The narrowed closure's compile proof is exactly the class N1 was: its transport calls
`s.q.ListExampleFeeds` and constructs `sqlc.ListExampleFeedsParams`, neither of which exists as Go
until sqlc has read the installed query file. `go build ./...` in the derivative now fails if that
chain breaks, instead of an AST cross-check being the only thing that notices.

## Collision check — no collision

The parent asked for a concrete collision if one existed. There is none. Every global name the
registry checks is disjoint between the two closures, and that is now asserted rather than
assumed: `TestExampleResourceFixturesDoNotCollide` builds both fixtures and refuses a duplicate
module id, table, sqlc method, route id, method+pattern, handler, navigation id, visual id, i18n
key, migration id, OpenAPI tag, OpenAPI schema or operationId. Concretely for `example-feed`:
table `example_feeds`, migration `ggg.example_feed` (installed as
`internal/db/migrations/0027_ggg_example_feed.sql`), route `api.example-feed.list` on
`GET /api/v1/example-feeds`, handler `handleAPIListExampleFeeds`, OpenAPI tag `example-feeds`,
schema `ExampleFeed`, operation `listExampleFeeds`, and no i18n keys at all.

## The org closure is unchanged

`git diff HEAD -- registry/testdata/registry/modules/workflow/example-resource` is empty after the
fixture regeneration: the update pass rewrote it byte-identically. Only `example-feed/` is new and
`registry/testdata/registry/workflows.json` gained one sorted item.

## Declared tests for the narrowed closure

`example-feed` declares no test packages, so `registry validate` runs none for it. That is correct
rather than a gap: the shape emits no validator — it has no write path and therefore no name rule
— and there is no pure logic left to unit-test. Pinned in both directions:
`TestCreateResourceResolvesEverySymbolItEmits` asserts the `_test.go` payload and
`tests.go_packages` are present together or absent together, and
`TestExampleFeedFixtureIsTheNarrowedShape` asserts this fixture has neither. Its value in the
validator is the install → sqlc → compile → remove → byte-for-byte-restore round trip.

## Tests added

- `TestCreateResourceMatchesExampleFixtures` — replaces the single-fixture test with a subtest per
  fixture over `exampleResourceFixtures`, so adding a shape is a table entry. Same update mode
  (`GGG_UPDATE_RESOURCE_FIXTURE=1`), same byte-equality in both directions, same
  `modkit.ValidateManifest` check per fixture.
- `TestExampleResourceFixturesDoNotCollide` — the collision matrix above.
- `TestExampleFeedFixtureIsTheNarrowedShape` — targets, empty `tests.go_packages`, the single read
  route, no Go dependencies, no identity requirement, no locales/navigation/visual, and that the
  emitted transport mentions none of `identity.`, `pgx.`, `logAudit`, `validateExampleFeedName`
  or `templates.`.
- `internal/modkit/example_test.go` — the expected closure-kind sequence gains the third
  `workflow` with a comment naming what the three are.

## Commands run

```
$ GGG_UPDATE_RESOURCE_FIXTURE=1 go test ./internal/gggcli -run TestCreateResourceMatchesExampleFixtures -v
    rewrote 6 fixture file(s) under registry/testdata/registry/modules/workflow/example-resource
    rewrote 4 fixture file(s) under registry/testdata/registry/modules/workflow/example-feed
--- PASS: TestCreateResourceMatchesExampleFixtures/example-resource
--- PASS: TestCreateResourceMatchesExampleFixtures/example-feed
$ git diff --stat HEAD -- registry/testdata/registry/modules/workflow/example-resource
(empty — the org closure is byte-unchanged)

$ go test ./internal/gggcli ./internal/modkit -count=1
ok  github.com/gogogadget/gogogadget/internal/gggcli   0.419s
ok  github.com/gogogadget/gogogadget/internal/modkit   7.079s
$ go vet ./internal/gggcli ./internal/modkit; gofmt -l internal/gggcli internal/modkit   → clean

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline
registry d47fe3e011f4d42c04b9c0f09424a66969a1e22a033e14ba9bbcbf5b0f35b2f7   # no drift

$ go run ./cmd/ggg registry validate
  info  example_closure_verified workflow closure ggg/workflow/example-feed: installed 3 file(s),
        regenerated 28, compiled, 1848 tree entries restored byte for byte,
        retained migration(s) internal/db/migrations/0027_ggg_example_feed.sql
  info  example_closure_verified workflow closure ggg/workflow/example-resource: installed 5 file(s),
        regenerated 30, compiled, 1848 tree entries restored byte for byte,
        retained migration(s) internal/db/migrations/0027_ggg_example_resource.sql
  ... all 9 closures verified, exit 0
```

## Remaining concerns after round 3

1. **Standing compiler coverage is two shapes of twenty.** The two are the extremes — the full
   slice and the narrowed one — and between them they cover every conditional branch in the
   import list, the audit organization, the sqlc call-arity split (struct vs bare argument, since
   platform's `Count`/`Get`/`Create`/`Delete` take bare values) and every optional declaration
   block. The eighteen in between are permutations of those branches, covered by the AST symbol
   and handler resolution gate plus the declaration assertions. A third closure would cost a third
   table and buy less than these two did.
2. **`example-feed` declares six sqlc methods and its transport calls one.** The query file is
   per-table and complete by design — an operator adding a write path should not have to author
   SQL — so the other five are generated by sqlc and unused. Consistent with shipped
   `projects.sql`, where `ListAllProjectsByOrg` exists only for the CSV export. Worth naming
   because `registry validate` compiles the package: unused *methods* are fine in Go, unused
   *imports* are not, which is why the import list is derived and the query set is not.
3. Unchanged from round 2: `--scope platform` implies `--admin`; `assertSQLCOutputDir` is
   substring-based and fails closed; a generated resource declares a `runtime.visual` baseline
   nobody has captured; no `content/docs/` page describes `ggg create` yet; `materializeManifest`
   returning the completed manifest changes what every `ggg create` kind reports in its plan.
