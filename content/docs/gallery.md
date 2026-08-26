---
title: Gallery and scenarios
description: The dev-only component reference and realistic product surfaces, why production refuses them, and how the visual and a11y matrices are generated from manifests.
section: Modules
weight: 35
---

The gallery is where the catalog becomes visible. It exists for three readers:
a developer choosing a component, a reviewer checking the design system holds,
and a coding agent that needs to see rendered output rather than infer it from
source. It is also the surface the visual and accessibility gates run against,
which is what stops it from decaying into a page nobody updates.

## The surfaces

| Route | What it is |
|---|---|
| `GET /dev/gallery` | The index: every family |
| `GET /dev/gallery/{family}` | One family's components, each with its summary and runtime note |
| `GET /dev/gallery/{family}/{component}` | One component's generated reference |
| `GET /dev/scenarios/{scenario}` | One realistic product surface |
| `GET\|POST\|DELETE /dev/ui/{component}/{action}` | Real fragment endpoints the examples call |

Ten families: `foundations`, `actions`, `forms`, `navigation`, `feedback`,
`overlays`, `data`, `communication`, `layout`, `advanced`. Twelve scenarios:
`analytics`, `billing`, `communication`, `content`, `dashboard`, `developer`,
`operations`, `planning`, `resource-list`, `settings`, `system-states`, `team`.

Unknown names return the ordinary dev 404, never a catalog-specific error — a
distinct response would let anyone enumerate what exists. The family segment must
also **agree** with the component's own family: accepting any family would make
the segment decorative and give every component two URLs to keep in sync. And the
family segment is checked with `Valid()`, not `Value()`: normalizing would render
`foundations` for every typo, and a URL that silently shows a different page
teaches the reader the wrong thing.

## Dev-only, and production refuses it

Every one of those routes carries `Scope: ScopeDev` and is registered only when
`DEV_AUTH_BYPASS` is on. It is not hidden, not behind a role check, not a
404-by-handler — it is **absent from the router**.

The second half is what makes that safe: `internal/config` refuses to boot at all
when `DEV_AUTH_BYPASS` is set under `APP_ENV=production`. So there is no
configuration in which a production deployment serves `/dev/gallery`: either the
flag is off and the routes do not exist, or the flag is on and the process does
not start.

None of these paths appear in `PublicNav` or `AppNav`. The interactive fragment
routes use narrow targets and real CSRF rather than no-op buttons, because an
example that does not exercise the real request teaches the wrong thing about the
real request.

## The component reference

`/dev/gallery/{family}` lists each component with its name, summary, and one
runtime note — the single fact that decides whether it can be used where
scripting is unavailable:

| Note | Meaning |
|---|---|
| `no script` | Declares no engine; works as-is |
| `enhanced — <fallback>` | Has an engine and a declared `native_fallback` |
| `needs <engine>` | Has an engine and no declared fallback |

Each family page renders its components twice: once at the page measure and once
deliberately squeezed, so a component that only works at a comfortable width is
visible as a problem rather than a surprise on someone's laptop.

`/dev/gallery/{family}/{component}` renders the generated reference in the order
a reader needs it: summary, exact signature, a copyable call, guidance, the
keyboard contract, the real states, and only then the installation facts (module,
family, `data-ui` attribute, engine, Alpine component, vendor, and what happens
without JavaScript). Every region is a named landmark with a stable
`data-testid`, so a screen-reader user can jump to the keyboard contract and the
runners can address one part of the reference rather than diffing the whole page.

All of it comes from the owning module's `runtime.ui` declaration — the same
records behind [Component reference](/docs/component-reference) and
`ggg info --json`. There is no second inventory to keep in sync.

`TestGalleryCoversEveryInstalledComponent` compares the `data-ui` markers
actually rendered by the gallery against the generated component registry. A
component installed but never rendered here is invisible to every reviewer and to
every visual and accessibility gate, so the coverage gap is a test failure rather
than an oversight.

## Scenarios

A component in isolation does not tell you whether it composes. Scenarios are
realistic product surfaces built from the catalog — `resource-list` with search,
filter, sort, selection, bulk actions, pagination, empty and error states;
`system-states` with skeleton, progress, 403, 404, 429, 500, 503 and maintenance;
`planning` with calendar, kanban, tree and resizable panels.

They render inside the **actual** shells. A dev-only context builder injects
fixed fake user, org, plan, and admin-write values and then calls the normal
`Server.Render` with the real `PublicLayout` / `AppLayout` / `AdminLayout`; it
does not copy shell markup. All scenario-specific layout stays inside `#content`,
which is the same chrome rule production pages follow.

Scenario data is explicit deterministic dev fixture data. It is never a
production fake and never randomised — a scenario that renders differently on two
runs cannot be a visual baseline.

State controls are query parameters, and every one of them is **validated
against the descriptor** rather than normalized:

```text
/dev/scenarios/resource-list?state=empty
/dev/scenarios/settings?state=error
/dev/scenarios/resource-list?content=long&dir=rtl
```

`state`, `dir`, `content`, `density`, and `page` are read from the query; a
`state` the scenario does not declare returns 404. Rendering the default for an
unrecognised state would quietly show the wrong thing, which is worse than a
missing page.

## The generated matrices

`e2e/generated/surfaces.ts` is emitted by `ggg sync` from the component,
scenario, and page manifests. Its shape is small on purpose:

```ts
export interface Surface {
  id: string;
  kind: 'family' | 'scenario' | 'page';
  path: string;
  fullPage: boolean;
  viewports: ('desktop' | 'mobile')[];
  persona: string;
  masks: string[];
}
```

Currently 40 surfaces: 10 families, 12 scenarios, and 18 production pages.
Family references are captured full-page; scenario baselines capture the
purposeful viewport at desktop and mobile.

**`e2e/visual.spec.ts`** walks that list across both themes and each surface's
declared viewports. Dark mode sets both `localStorage.theme` and
`prefers-color-scheme`, because the shell reads the former and the parts with no
class hook follow the latter. Masks are **per surface, by declaration** — a
blanket mask would hide a real regression everywhere in order to stabilise the
few surfaces that genuinely carry a nondeterministic value.

**`e2e/a11y.spec.ts`** walks the same list, in both themes, running axe on each.
Sharing one generated list is the point: a component family or scenario cannot be
pixel-checked while its accessibility goes unscanned. Two pages are scanned but
deliberately not pixel-compared — `/admin/jobs` mutates between scrapes and
`/admin/media` thumbnails are not stable pixels — and that short list is filtered
against the matrix so a surface declared later stops being scanned twice.

The severity floor is **moderate**: moderate, serious, and critical findings fail
the run, because an unlabelled control or insufficient contrast is a real barrier,
not a style note. Minor findings are attached to the report instead — visible to
whoever reads it, without turning the gate into noise.

`e2e/a11y-states.spec.ts` extends the scan past page load into declared
interaction states: opened dialogs, menus, popovers, tooltips, drawers, and the
command palette; selected tab, combobox, calendar, tree, and grid states.

## Running them

```sh
make e2e             # the full Playwright suite against a real server and db
make visual          # compare committed baselines in the pinned Playwright container
make visual-update   # regenerate baselines in that same container
```

`visual` and `visual-update` are siblings and differ only by
`--update-snapshots`. Both run inside the pinned Playwright Linux image, because
a baseline rendered on a developer's font stack is a baseline CI can never match.
When a pixel change is intended, run `make visual-update` once and then
`make visual` to prove the committed baselines compare cleanly.

Adding a component or a scenario adds its surface to both matrices with no test
edit, which is the whole reason the list is generated.

## Where to go next

- [Component usage](/docs/components) — reading a reference entry and the
  progressive-enhancement contract.
- [Component reference](/docs/component-reference) — the generated inventory.
- [Testing](/docs/testing) — the four test layers and when to use each.
- [Frontend](/docs/frontend) — the shell these surfaces render inside.
