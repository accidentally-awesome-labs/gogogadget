# Task B — every e2e spec has a feature-module owner, and the no-orphan rule is mechanical

## Outcome

`ggg/system/e2e` no longer owns 20 specs. Ownership is now spread across 23 modules,
`ggg/system/e2e` keeps only the 9 cross-cutting sweeps, and two Go tests in
`internal/modkit` refuse an orphan spec on every run.

## Final spec → owner table

The enforcement rule is "every literal app/admin/public path the spec navigates must
resolve to a route declared by the owner or one of its transitive `requires`". The
evidence column therefore names the literal navigation targets and why that owner
reaches them.

### Cross-cutting sweeps — `ggg/system/e2e` (unchanged owner)

| spec | literal app/admin/public targets | evidence |
|---|---|---|
| `e2e/a11y.spec.ts` | none (drives `e2e/generated/surfaces.ts`) | platform sweep over the generated surface inventory |
| `e2e/a11y-states.spec.ts` | none (`/dev/scenarios/${slug}`, dev scope) | scenario/state sweep over the dev gallery |
| `e2e/keyboard.spec.ts` | none (`/dev/gallery`, dev scope) | component keyboard contract, not a feature page |
| `e2e/progressive.spec.ts` | none (`/dev/gallery`, `/dev/scenarios/*`, dev scope) | no-JS behaviour of the component layer |
| `e2e/visual.spec.ts` | none (drives generated surfaces) | screenshot sweep; also `tests.visual` |
| `e2e/csp.spec.ts` | `/` | policy holds for the whole shell |
| `e2e/loading.spec.ts` | `/app`, `/app/projects` | navigation indicator + skeleton, a shell behaviour |
| `e2e/mobile.spec.ts` | `/app` | drawer/hamburger chrome in the app shell |
| `e2e/public.spec.ts` | `/`, `/pricing`, `/blog`, `/changelog`, `/docs`, `/docs/index`, `/docs/getting-started`, `/docs/search`, `/rss.xml`, `/sitemap.xml`, `/robots.txt` | public-site chrome, anchors, feeds, 404 |

Because those four last suites do navigate real pages, `ggg/system/e2e` gained the
`requires` that make the claim honest (see "requires added"). `ggg/system/e2e` is a
member of `saas` and `full` only, so nothing lighter is affected.

### Feature specs

| spec | owner | evidence |
|---|---|---|
| `e2e/billing.spec.ts` | `ggg/workflow/billing-checkout` | after the split it navigates only `/app/settings/billing`; the module already `requires ggg/page/settings-billing` and declares `POST /app/billing/checkout` and the portal routes the tests exercise |
| `e2e/project-plan-limits.spec.ts` **(new, split from billing)** | `ggg/workflow/projects` | the two moved tests navigate `/app/projects/new` and create a project; the `plan-limit` notice they assert is rendered by `internal/web/templates/projects.templ` from `handleProjectCreate` (`workflow_projects.go:62`), i.e. the projects workflow's own 422 fragment — not the billing page |
| `e2e/webhooks.spec.ts` | `ggg/workflow/outbound-webhooks` | navigates only `/app/settings/webhooks`; module declares the endpoint create/rotate/disable/replay routes and already `requires ggg/page/settings-webhooks` |
| `e2e/notifications.spec.ts` | `ggg/workflow/notification-inbox` | remaining tests drive the bell/badge/read-all/SSE stream: `/app` (shell carrier) and `GET /app/notifications/badge`, both this module's routes plus the app home |
| `e2e/notification-preferences.spec.ts` **(new, split from account + notifications)** | `ggg/workflow/notification-preferences` | both moved tests only navigate `/app/settings/notifications` and submit `notification-prefs-form` → `POST /app/settings/notifications`, this module's single route |
| `e2e/files.spec.ts` | `ggg/workflow/files` | navigates `/app/files`; module declares `POST /app/files`, `GET/DELETE /app/files/{id}` and already `requires ggg/page/files` |
| `e2e/projects.spec.ts` | `ggg/workflow/projects` | create/search/archive/delete/cancel drive `/app/projects` and `/app/projects/new`; module declares every projects mutation |
| `e2e/activity.spec.ts` **(new, split from projects)** | `ggg/page/activity` | the moved test navigates `/app/activity` only — the sole route `ggg/page/activity` declares; it had nothing to do with the projects workflow |
| `e2e/impersonation.spec.ts` | `ggg/workflow/impersonation` | drives `/admin/users` (where `admin-impersonate` renders), the impersonate interstitial and `POST /app/impersonation/exit`, then asserts `/admin` is 403 mid-impersonation and reachable after exit |
| `e2e/auth.spec.ts` | `ggg/workflow/auth-session` | every test is the guard chain and shell/Clerk mount behaviour behind `/login`; the only literal app path is `/app`, the destination `/login` protects |
| `e2e/locale.spec.ts` | `ggg/workflow/appearance` **(deviates from the brief's table — see below)** | the spec clicks `locale-switcher` → `POST /set-locale`, whose handler is `internal/web/handlers_locale.go`, a file `ggg/workflow/appearance` owns; it navigates `/` and `/pricing` |
| `e2e/appearance.spec.ts` **(new, split from account)** | `ggg/workflow/appearance` | the moved test drives `theme-dark` / `locale-pref-*` on `/app/settings/account` and asserts the result on `/app`; both controls post to `/set-theme` and `/set-locale` |
| `e2e/export.spec.ts` | `ggg/workflow/project-export` | clicks `projects-export` on `/app/projects` (`projects.templ:19` posts `/app/projects/export`, this module's route) and polls `/app/files` for the CSV the job writes |
| `e2e/account-export.spec.ts` **(new, split from account)** | `ggg/workflow/account-export` | the moved test clicks `export-data` on `/app/settings/account` → `GET /app/settings/account/export`, this module's only route |
| `e2e/account-delete.spec.ts` **(new, split from account)** | `ggg/workflow/account-delete` | the two moved tests drive the danger zone on `/app/settings/account` → `POST /app/settings/account/delete`; module already `requires ggg/page/settings-account` |
| `e2e/admin-overview.spec.ts` **(new, split from admin)** | `ggg/page/admin-overview` | `admin home renders stat cards` and `non-admin gets 403` navigate `/admin` only, the route this module declares |
| `e2e/admin-users.spec.ts` **(new, split from admin)** | `ggg/page/admin-users` | `user search filters` navigates `/admin/users` only |
| `e2e/admin-user-governance.spec.ts` **(new, split from admin)** | `ggg/workflow/admin-user-governance` | `disable toggle flips state and audits` drives `admin-disable-toggle` → `POST /admin/users/{id}/disable`; the support-staff case asserts the same page's write controls are absent and that the disable POST is 403 |
| `e2e/admin-organizations.spec.ts` **(new, split from admin)** | `ggg/page/admin-organizations` | `/admin/orgs` only |
| `e2e/admin-audit.spec.ts` **(new, split from admin)** | `ggg/page/admin-audit` | `/admin/audit` only |
| `e2e/admin-jobs.spec.ts` **(new, split from admin)** | `ggg/page/admin-jobs` | `/admin/jobs` only |
| `e2e/admin-announcements.spec.ts` **(new, split from admin)** | `ggg/workflow/admin-announcements` | create/activate/deactivate/delete are this module's four routes; the banner it activates is asserted on `/app` |
| `e2e/admin-flags.spec.ts` **(new, split from admin)** | `ggg/workflow/admin-flags` | create/override/delete/cancel drive `/admin/flags` and `/admin/flags/{key}`; already `requires ggg/page/admin-flags`. Also holds the flags half of the support-staff case (`flags-table` present, `flag-create-form` absent) |
| `e2e/admin-schedules.spec.ts` **(new, split from admin)** | `ggg/workflow/admin-schedules` | create/run/toggle/delete on `/admin/schedules`; already `requires ggg/page/admin-schedules` |
| `e2e/admin-content.spec.ts` **(new, split from admin)** | `ggg/workflow/admin-content` | the CMS lifecycle drives `/admin/content`, `/admin/content/{id}` and then asserts the published post on `/blog` — the workflow's whole point |

34 spec files, 34 declarations, one owner each.

## Deviation from the brief's owner table: `e2e/locale.spec.ts`

The brief assigns it to `ggg/system/i18n`. That is not implementable together with the
brief's own enforcement rule: the spec navigates `/` and `/pricing`, so the owner must
reach `ggg/page/home` and `ggg/page/pricing` — and both of those modules already
`requires ggg/system/i18n`, so the needed edge would be a dependency cycle
(`i18n → page/home → i18n`). `ggg/system/i18n` is a leaf seam (`requires` only
`ggg/system/apphost`) and can never own a spec that navigates a page.

`ggg/workflow/appearance` is the correct owner and no weaker a claim: it owns
`internal/web/handlers_locale.go` and `internal/web/appearance.go`, declares
`POST /set-locale` and `POST /set-theme`, and `requires ggg/system/i18n`. The spec
drives exactly those controls. It now also owns `appearance.spec.ts`, so the whole
locale/theme workflow is one owner.

## Specs split, and why

| original | split into | reason |
|---|---|---|
| `e2e/account.spec.ts` (deleted) | `account-export`, `notification-preferences`, `account-delete`, `appearance` | four modules: `GET /app/settings/account/export`, `POST /app/settings/notifications`, `POST /app/settings/account/delete`, `POST /set-theme`+`/set-locale`. Picking one owner would have left three modules' assertions behind |
| `e2e/admin.spec.ts` (deleted) | `admin-overview`, `admin-users`, `admin-user-governance`, `admin-organizations`, `admin-audit`, `admin-jobs`, `admin-announcements`, `admin-flags`, `admin-schedules`, `admin-content` | ten admin modules, one page or workflow per test. No module's closure covers the whole file (verified: zero candidate owners) |
| `e2e/billing.spec.ts` | kept + `project-plan-limits` | two of five tests never touch the billing page; they create projects and assert the projects workflow's `plan-limit` fragment |
| `e2e/notifications.spec.ts` | kept + (into) `notification-preferences` | `digest cadence persists across reloads` is a preferences test on `/app/settings/notifications`, not an inbox test |
| `e2e/projects.spec.ts` | kept + `activity` | `activity page paginates` drives `/app/activity`, owned by `ggg/page/activity` |

Test titles are preserved: each moved test keeps its enclosing `test.describe(...)`
name from the original file (with a comment saying why), so the enumerated suite is
title-identical.

One test was split rather than moved: `support staff read the admin area without the
controls` asserted `/admin/users` write controls *and* `/admin/flags` write controls,
and no module reaches both. Its flags assertions moved verbatim into
`admin-flags.spec.ts` as `support staff read the flags admin without the controls`;
everything else stayed under the original title in `admin-user-governance.spec.ts`.
This is the only title added to the suite (668 → 669 tests).

## `requires` added

Every addition follows the convention already in the catalog — a workflow requires the
page module whose surface it mutates or whose surface renders its effect
(`admin-announcements → page/admin-announcements`, `billing-checkout →
page/settings-billing`, `account-delete → page/settings-account`, …). Several of these
were simply missing before.

| module | added | why |
|---|---|---|
| `ggg/system/e2e` | `page/home`, `page/pricing`, `page/blog`, `page/changelog`, `page/docs`, `page/docs-index`, `page/docs-search`, `page/dashboard`, `page/projects`, `workflow/seo-discovery` | the retained cross-cutting gates sweep exactly these surfaces; you cannot install the sweep without them |
| `ggg/workflow/projects` | `page/projects`, `page/project-new` | `handleProjectCreate`/`renderProjectFormError` render `projects.templ` and redirect to `/app/projects` |
| `ggg/workflow/project-export` | `page/projects`, `page/files` | the `projects-export` trigger renders on the projects page; the CSV lands in the files surface |
| `ggg/workflow/impersonation` | `page/admin-users`, `page/admin-overview` | the impersonate control renders in the admin users table; exit returns to `/admin` |
| `ggg/workflow/auth-session` | `page/dashboard` | `/app` is the destination the login guard protects (mirrors the existing `page/settings-account → workflow/auth-session` edge) |
| `ggg/workflow/notification-inbox` | `page/dashboard` | the bell, badge fragment and SSE carrier live in the app shell served at `/app` |
| `ggg/workflow/appearance` | `page/home`, `page/pricing`, `page/settings-account`, `page/dashboard` | the switcher renders in the public footer and the account settings prefs; the applied theme/locale is observed on the app shell |
| `ggg/workflow/account-export` | `page/settings-account` | the `export-data` control renders there |
| `ggg/workflow/admin-user-governance` | `page/admin-users` | its role and disable controls render on that page |
| `ggg/workflow/admin-announcements` | `page/dashboard` | an activated announcement renders in the app shell |
| `ggg/workflow/admin-content` | `page/admin-content`, `page/blog` | it mutates the content table and publishes to the public blog |

No cycles (verified over the whole 295-module graph). Every affected owner is a member
of both `profile/saas` and `profile/full`. Route-registry output is unchanged apart
from its digest header, so no ordering or behaviour moved.

## Enforcement test

`internal/modkit/e2e_ownership_test.go` (package `modkit`, so it can reuse
`selectedRoutes`):

- **`TestEveryDeclaredE2ESpecIsReachableFromItsOwner`** — for every manifest-declared
  spec, extracts literal navigation targets (`.goto('…')` and
  `request.{get,post,put,patch,delete,head,fetch}('…')`; template literals containing
  `${` and non-literal arguments are skipped) and resolves each one through an
  `http.ServeMux` carrying every `app`/`admin`/`public` pattern from
  `selectedRoutes(Lock{}, catalog.Modules)` — the same table the router is generated
  from, including content-type expansion (`/blog`, `/blog/{slug}`, `/changelog`). A
  path that matches no catalog route is skipped (404 assertions, `page.route()` fixture
  URLs such as `/__clerk-retry`). Otherwise the declaring module must reach a
  declaring module through itself or its transitive `requires`, or the test fails
  naming the spec, the path, the matched pattern and the owner.
- **`TestEveryE2ESpecOnDiskHasExactlyOneOwner`** — every `e2e/*.spec.ts` on disk is in
  exactly one manifest's `tests.e2e`, every declared spec is a file that manifest owns,
  and every declared spec exists.

Deliberate limitation, documented in the test: computed targets are not guessed at.
Those paths come from the generated Playwright inventory, which is validated where it
is emitted.

## Files changed

- **Deleted:** `e2e/account.spec.ts`, `e2e/admin.spec.ts`
- **New specs (16):** `e2e/{account-delete,account-export,activity,admin-announcements,admin-audit,admin-content,admin-flags,admin-jobs,admin-organizations,admin-overview,admin-schedules,admin-user-governance,admin-users,appearance,notification-preferences,project-plan-limits}.spec.ts`
- **Modified specs:** `e2e/billing.spec.ts`, `e2e/notifications.spec.ts`, `e2e/projects.spec.ts`
- **New test:** `internal/modkit/e2e_ownership_test.go`
- **Manifests (25):** `registry/modules/system/{e2e,modkit,content-assets}/module.json`, `registry/modules/page/{activity,admin-audit,admin-jobs,admin-organizations,admin-overview,admin-users}/module.json`, `registry/modules/workflow/{account-delete,account-export,admin-announcements,admin-content,admin-flags,admin-schedules,admin-user-governance,appearance,auth-session,billing-checkout,files,impersonation,notification-inbox,notification-preferences,outbound-webhooks,project-export,projects}/module.json`
- **Docs:** `content/docs/testing.md` (spec ownership + the enforcement rule)
- **Generated (via `registry build` + `sync --offline`):** `gogogadget.lock.json`, `registry.snapshot.json`, `e2e/generated/inventory.ts`, `content/docs/module-reference.md`, and the `*_registry_gen.*` set (digest headers only, plus `static/ui-*.js` re-emission)

## Commands run

```
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline
registry dda598640121b19a95c256965d5357fdc9015baf9f31457f7239606d965e8ddc
  update    lock       gogogadget.lock.json
registry dda598640121b19a95c256965d5357fdc9015baf9f31457f7239606d965e8ddc
EXIT=0

$ go test ./internal/modkit ./internal/gggcli -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	7.274s
ok  	github.com/gogogadget/gogogadget/internal/gggcli	0.513s

$ gofmt -l internal/modkit/e2e_ownership_test.go   # no output
$ go vet ./internal/modkit                          # no output
```

Mutation proof — `e2e/admin-flags.spec.ts` temporarily reassigned to
`ggg/page/admin-audit` (manifest `files` + `tests.e2e` moved), then reverted:

```
$ go test ./internal/modkit -run TestEveryDeclaredE2ESpecIsReachableFromItsOwner -count=1
--- FAIL: TestEveryDeclaredE2ESpecIsReachableFromItsOwner (0.10s)
    e2e_ownership_test.go:64: orphan e2e test: e2e/admin-flags.spec.ts is owned by
    ggg/page/admin-audit but navigates /admin/flags, which route "/admin/flags"
    declares in [ggg/page/admin-flags ggg/workflow/admin-flags] — no module
    ggg/page/admin-audit can reach. Move the spec to an owner that reaches that
    surface, split it along module lines, or declare the missing requires.
FAIL
FAIL	github.com/gogogadget/gogogadget/internal/modkit	0.306s
```

Suite structure — original (`git archive HEAD e2e`) vs. now:

```
$ (original) npx playwright test --list | tail -1
Total: 668 tests in 20 files
$ npx playwright test --list | tail -1
Total: 669 tests in 34 files
$ diff orig-titles.txt new-titles.txt
347a348
> chromium | support staff read the flags admin without the controls
```

Split files against the running stack
(`docker compose up -d ggg-system-database-postgres-docker-postgres`; Playwright's
`webServer` boots `cmd/server` on :18080 and `globalSetup` reseeds `gogogadget_e2e`):

```
$ cd e2e && npx playwright test --project=chromium --reporter=line \
    account-delete account-export appearance notification-preferences notifications \
    billing project-plan-limits projects activity
  20 passed (16.1s)

$ cd e2e && npx playwright test --project=chromium --reporter=line \
    admin-overview admin-users admin-user-governance admin-organizations admin-audit \
    admin-jobs admin-announcements admin-flags admin-schedules admin-content
  14 passed (8.6s)

$ cd e2e && npx playwright test --project=chromium --reporter=line \
    auth locale export files impersonation webhooks
  16 passed (11.1s)
```

(The third run covers the reassigned-but-unsplit specs; not required by the brief,
run for confidence.)

## Concerns / notes for the parent

1. **Ownership transfer needed a two-phase sync.** `classifyAuthoredTarget`
   (`internal/modkit/target_plan.go:57`) refuses `target X is owned by A, not B`
   whenever the lock still records A as owner, and `reconcile.go:445` refuses to drop a
   locally modified file from a manifest. Moving a spec's owner *in place* — which is
   exactly what the brief asked for — is therefore not expressible in one `sync` against
   an existing lock. I did it without touching the engine: phase 1 moved the transferred
   specs out of the tree and out of every manifest and synced (a dropped file that is
   missing is silently released, `reconcile.go:442`), phase 2 restored the files, added
   them to their new owners and synced (identical bytes → `ChangeUnchanged`). The
   committed end state is reproducible in one pass from a clean checkout, because a
   fresh install has no prior lock ownership; only in-place transfers on an existing
   lock need the dance. If the framework wants catalog refactors to be a single
   `sync`, `classifyAuthoredTarget` should compare against the *new* graph's ownership
   rather than the lock's — a core engine change I deliberately left out of this slice.
2. **`ggg registry validate` was not run** (not in the acceptance list, and expensive).
   No `registry/testdata` or `registry/external-testdata` fixture references any module
   whose `requires` I changed, so the example closures should be unaffected — but the
   final gate is the place to confirm it.
3. **`e2e/node_modules` was installed** with `npm ci` to run Playwright;
   `e2e/package.json` and `e2e/package-lock.json` are unchanged.
4. `.superpowers/sdd/framework-followups/task-c-brief.md` is untracked in this worktree
   and belongs to another slice; it is deliberately not part of my commit.
