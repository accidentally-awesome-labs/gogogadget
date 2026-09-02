# Task A review findings — fix round 1

Source: ReviewCreateResource of 25bb87f..b0efa97 (base 774af56). Spec compliance WARN, task
quality FAIL. Fix every item; the first two are blocking.

## Critical

- **C1 — `--scope platform` emits unguarded app-scope writes.** `create_resource.go:277-284`
  emits POST create, POST update and DELETE at `modkit.RouteApp` for every scope. For
  `platform`, `tenantValue()` returns `""` (`create_resource_source.go:275-283`) so the emitted
  `createArgs`/`updateArgs`/`deleteArgs` carry no tenant predicate, and the handler bodies
  (`create_resource_source.go:441-505`) carry no staff/role check. The app middleware group is
  `requireAuth → requireNotDisabled → requireOrg → loadPlan` (AGENTS.md) — no `requireStaff`.
  So `ggg create resource Banner --scope platform` hands every authenticated member of every
  organization create/rename/delete on a global table, and with `--api` the same over the JSON
  transport. No shipped platform-scoped table behaves that way (`announcements`,
  `content_entries`, `feature_flags`, `jobs`, `schedules` are written from `/admin` or system
  code only), and it contradicts your own `--api --scope user` refusal.
  Fix: when `tenantColumn == ""`, emit the three mutations at `modkit.RouteAdmin` with
  `Policy{AdminWrite: true}` and render them from the admin surface, or refuse the shape beside
  the two existing refusals. Add a test asserting that for `--scope platform` no `RouteApp`
  route has a non-GET method.

- **C2 — `--search` files documents under the acting organization for every scope.**
  `indexCall` hardcodes `org.OrgID` (`create_resource_source.go:546-558`) and the comment at
  `:544-545` claims the seam forces this. It does not: `search.Document.Fields` and
  `search.Query.Filters` exist (`internal/search/search.go:11-19`). Consequences: user-scoped
  private rows become org-wide discoverable, and platform rows are filed under whichever org
  wrote them (and `search_documents.tenant_id` has an FK to `orgs`, so no correct tenant
  exists). Fix: index user-scoped rows with a filterable `user_id` field; for platform either
  refuse `--search` or use an explicit documented tenant. Correct the false comment.

## Important / Minor

- **I1** — Record the `--api --scope user` refusal somewhere other than a code comment: add it
  to `.superpowers/sdd/framework-followups/task-a-brief.md`'s flag list and to your report, so
  the advertised flag matrix and the implemented one agree.
- **I2** — Locale coverage test only checks the plain shape, and its unread-key exemption
  `HasSuffix(key, ".nav")` never matches `admin_nav`, so the admin locale pair is unchecked.
  Extend the test across the flag shapes.
- **M1** — `<Slug>FormInput` and its validator are emitted for `--no-ui --api`
  (`create_resource_source.go:227-256`, gated at `:156-158`) where nothing decodes a form.
- **M2** — `sqlcOutputDir` (`internal/modkit/example.go:1024`) duplicates `sqlc.yaml`; make the
  restore fail loudly rather than silently no-op if the output directory moves.

## Not in scope (do not fix)

The pager locale gap (`ui_adapters.go` resolving `activity.*` keys) is pre-existing and shared
with shipped `page/projects`. Leave it.
