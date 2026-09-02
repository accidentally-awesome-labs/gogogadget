### A. Complete `ggg create resource` so it emits the projects-pattern slice

Binding plan requirement (verbatim, from `local://full-stack-framework-plan.md` task 8):

> Implement `ggg create module|resource|page|workflow|job|migration|component|provider`, writing complete
> local-registry source/manifest, refreshing, previewing, applying. Forms:
> `resource NAME --scope user|org|platform [--table TABLE] [--route PREFIX] [--api] [--admin] [--search] [--no-ui]`;
> … Resource defaults snake `NAME+"s"` and `/app/<kebab-name>s`, supports overrides, emits projects-pattern slice.

Current defect: `internal/gggcli/create.go:151-178` emits a single placeholder file
(`internal/<name>/resource.go` holding `type Resource struct { ID string }` plus a `Route`
const) and one CREATE TABLE migration. `CreateMutation.API`, `.Admin`, `.Search` and `.NoUI`
(`internal/gggcli/types.go:179-182`) are parsed by the flag layer and then discarded — four
user-facing flags that do nothing. No routes, queries, handlers, templates, navigation,
locales or tests are produced, so the "slice" does not serve a request.

#### What to build

`create resource NAME` must write a complete, compiling, request-serving vertical slice into
the mutable project registry, modelled exactly on the shipped projects pattern:

- `registry/modules/page/projects/module.json` + `internal/web/page_projects.go` +
  `internal/web/templates/projects.templ` (read surface, navigation, visual entry, locales)
- `registry/modules/workflow/projects/module.json` + `internal/db/queries/projects.sql` +
  `internal/web/workflow_projects.go` (mutations, queries, migration, data declaration)

Emit ONE module (kind `workflow`, id `<namespace>/workflow/<slug>`) that carries the union:

1. `internal/db/queries/<table>.sql` — one query file for the table, sqlc-annotated, in the
   house style of `internal/db/queries/projects.sql`. Every UPDATE sets `updated_at = now()`.
   Declare each query in `runtime.queries` with its table.
2. The immutable migration (already implemented) — keep the existing ledger/`payload/` shape,
   but the table must carry the columns the generated queries and handlers use
   (`id`, tenant column per `--scope`, `name`, `created_at`, `updated_at`), not just `id`.
3. `internal/web/workflow_<slug>.go` — list/create/update/delete handlers plus the read page
   handler, following `internal/web/workflow_projects.go` and `page_projects.go`: fragment
   rule via `wantsFragment`, 422 re-render for invalid form input, `hx-delete` row removal,
   `Navigate`/`Toast` for in-app redirects, `identity` scope guard matching `--scope`.
4. `internal/web/templates/<slug>.templ` — table card, empty state, form, delete confirm via
   `ui.ConfirmAction`; every asserted element carries `data-testid`; all strings go through
   `i18n.T(ctx, "<slug>.key")`; only `ui` package components; no raw hex, no `dark:` variants,
   no inline scripts (the `templates` package design-system test enforces this).
5. `runtime.routes` entries with ids matching `claims.routes`, `runtime.navigation` for the app
   surface, `runtime.visual` for the read page, and `locales` with the full `en` + `es` key set
   (generation refuses a key missing from a locale or with mismatched placeholders).
6. `data` + `claims.data` declarations for the table (already implemented — keep).
7. `requires` must name every module the generated source actually imports/needs
   (`ggg/system/database`, `i18n`, `identity`, `security`, `server`, plus `organizations` for
   `--scope org`, `api` for `--api`, `search` for `--search`).

Flag behavior (all four are currently dead):

- `--api` adds the `/api/v1/<plural>` JSON transport following `internal/api` conventions
  (Bearer token auth, same authorization rules) and its route/OpenAPI declarations.
- `--admin` adds the admin read surface (`scope: "admin"` route + nav entry) following the
  shipped admin pages.
- `--search` adds the search-document wiring for the table following the installed search
  module's contract.
- `--no-ui` omits the app UI surface entirely: no templ file, no app routes, no navigation, no
  visual entry, no locales — queries, migration, data and (when asked) API only.
- `--no-ui` with `--admin` is a usage error (admin IS a UI surface); refuse before any write.
- `--api` with `--scope user` is a usage error. `RequireAPIToken` puts an organization in the
  request context and nothing else (`internal/api/tokens.go`), so a user-scoped table has no
  tenant on the JSON transport; emitting it would list every user's rows to any token holder.
- `--api` with `--scope platform` emits the read route only. No route scope can impose a staff
  check on a token-authenticated write, and a platform table has no tenant predicate that could
  scope one, so its mutations stay in `/admin`.
- `--search` with `--scope platform` is a usage error. `search_documents.tenant_id` has a foreign
  key to `orgs`, so every document belongs to one organization and a platform row belongs to
  none. A user-scoped row is indexed under the acting organization with its owner in
  `search.Document.Fields` (`user_id`), which `search.Query.Filters` narrows on.
- `--scope platform` puts the three mutations at `scope: "admin"` with `policy.admin_write`, and
  renders them from the staff surface, which is therefore not optional there. The app middleware
  group has no `requireStaff`, and a global table has no tenant predicate, so an app-scope write
  would hand every authenticated member of every organization create/rename/delete on it.
- Defaults stay as documented: table `snake(NAME)+"s"`, route `/app/<kebab(NAME)>s`, both
  overridable by `--table` / `--route`.

#### Acceptance

- Unit tests in `internal/gggcli` covering: default table/route; `--table`/`--route` overrides;
  the manifest declaration set (routes/queries/navigation/visual/locales/data/requires) for the
  plain, `--api`, `--admin`, `--search` and `--no-ui` shapes; and the `--no-ui --admin` refusal.
- A compile-verified example closure: add `registry/testdata/registry/modules/workflow/example-resource`
  whose payloads are byte-for-byte what the generator emits for one representative invocation
  (`--scope org --api --admin --search`), plus a test that runs the generator into a temp
  registry and asserts equality with that fixture. `go run ./cmd/ggg registry validate` then
  installs, compiles, tests, removes and restores the emitted slice for real — that is the proof
  the generated Go/templ/SQL compiles, and it must pass.
- `go test ./internal/gggcli ./internal/modkit -count=1`, `go vet` and `gofmt` clean on touched
  packages. `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline &&
  go run ./cmd/ggg sync --check --offline` clean.
- Do NOT run project-wide suites (`make check`, `go test ./...`, e2e, visual); the parent runs
  the final gate.
