---
title: Component usage
description: How to find a component, how to read its reference entry, what native_fallback promises, and the progressive-enhancement contract.
section: Modules
weight: 34
---

There are 172 renderers. Nobody memorises them, and no hand-maintained list of
that size survives a month, so every path to finding one is generated from the
manifests.

## Finding a component

**From the terminal.** Filter the catalog by kind:

```sh
go run ./cmd/ggg catalog --kind component
go run ./cmd/ggg catalog --kind element --installed
```

Then ask for one:

```sh
go run ./cmd/ggg info component/badge
```

```text
component/badge  Badge
state          clean
revision       1 (contract 1)
removal_policy free
requires       element/ui-core
  file internal/web/templates/ui/badge.templ
  gallery  /dev/gallery/feedback
  gallery  /dev/gallery/feedback/badge
```

`--json` returns the whole manifest plus three derived fields: `state`, `links`,
and `verify`. `links` is the answer to "where can I look at this?" and `verify`
is the answer to "what do I run?" — both computed from the manifest rather than
stored in it, so neither can drift from the routes that serve the component or
the tests that cover it. A component with no declared tests reports
`"verify": null`, which is a stated absence rather than an omission.

**From this documentation.** The [Component
reference](/docs/component-reference) page is generated from the same
`runtime.ui` declarations: 172 rows grouped by gallery family, each with its
exact signature, owning module, engine, Alpine controller, and whether it
declares a no-JavaScript fallback.

**From the browser.** `/dev/gallery` renders every component, live, in both
themes. That is [its own page](/docs/gallery).

## Reading a reference entry

Every fact about a component is declared in its manifest's `runtime.ui` entry and
published in three places from that one source: `ggg info --json`, the generated
`ui.ReferenceRegistry`, and the gallery detail page at
`/dev/gallery/{family}/{component}`. The fields:

| Field | What it answers |
|---|---|
| `name` | The `data-ui` marker on the component's root |
| `family` | Which gallery family it appears under |
| `signature` | The exact declaration, e.g. `templ Badge(o BadgeOpts)` |
| `summary` | One line: what it is |
| `guidance` | When to reach for it, when not to, and why it is built this way |
| `keyboard` | The key contract it implements |
| `states` | The states it genuinely has |
| `engine` | The third-party runtime it needs, if any |
| `alpine` | The CSP-safe Alpine controller it registers, if any |
| `vendor` | The pinned vendored asset, if any |
| `native_fallback` | What it degrades to with no JavaScript |

The gallery detail page renders them in the order a reader needs them: what it
is, how it is called, a copyable call, when to use it, what the keyboard does,
which states it really has, and only then the installation facts. Each region is
a named landmark, so a screen-reader user can jump to the keyboard contract
instead of reading the page to find it.

Two absences are meaningful rather than missing:

- **No `keyboard`** means the component adds no key handling of its own. That is
  a different statement from "unknown", and the page says so in words rather than
  leaving a blank.
- **No `engine`** means it needs no script at all. The family list shows this as
  `no script`, and it is the single fact that decides whether a component can be
  used where scripting is unavailable.

Blank fields are omitted rather than rendered empty, because an empty row reads
as missing data instead of as a component that has no engine.

## `native_fallback` is a promise

`native_fallback` states, in the manifest, what the component does when
JavaScript never arrives. It is one short phrase and it is load-bearing:

| Component | `native_fallback` |
|---|---|
| `bar-chart`, `line-chart`, `area-chart`, `donut-chart`, `sparkline` | `data table` |
| `dialog`, `alert-dialog`, `confirm-action` | `dialog element` |
| `dropdown-menu`, `popover` | `popover attribute` |
| `date-picker`, `date-time-picker`, `date-range-picker` | `native date input` |
| `file-dropzone`, `tags-input`, `slug-input`, `char-counter`, `date-range-field` | `native input` |
| `markdown-editor` | `plain textarea` |
| `data-grid` | `semantic table with pagination` |
| `kanban` | `move buttons in every card's form` |
| `tab-panels` | `sequential panels` |
| `context-menu`, `hover-card` | `visible trigger` |

The full, current list is the **Fallback** column on the [Component
reference](/docs/component-reference) page.

The declaration follows one convention, which is worth knowing before you go
looking for a missing entry. No catalog item declares a `native_fallback`
without also declaring an `engine`: a component with no engine needs no
fallback statement, because `no script` already says everything. The reverse is
allowed and deliberate — `carousel`, `command-palette`, `column-picker`,
`panel-group`, `tree`, and `tree-grid` declare an engine and no fallback, and
their `guidance` is where the no-script story lives.

This is a promise in three concrete senses:

1. **The declaration drives what the gallery tells every reader.** A component
   with an engine and a fallback is presented as `enhanced — <fallback>`; one
   with an engine and no fallback is presented as `needs <engine>`; one with no
   engine is `no script`. Dropping the fallback declaration while keeping the
   behavior would change the advice the catalog gives.
2. **The a11y suite covers the no-JavaScript surfaces**, so a fallback that stops
   working fails a gate rather than becoming a bug report.
3. **The fallback is the primary artifact, not a consolation.** A chart renders
   its caption, summary, and semantic **table first** — always present, never
   hidden by default. The canvas becomes visible only after the engine
   initialises, and the adapter reads its data *out of that table* rather than
   from a duplicate payload, so the picture and the numbers cannot drift apart.
   If the engine never loads, the page is a complete, readable, printable table.

The corollary is the rule that keeps this honest: **no consequential action may
exist only inside an enhancement.** Kanban's drag-and-drop submits the same HTMX
form as the keyboard-operable "Move to…" menu in each card. The file dropzone
wraps a real `<input type="file">` inside a `<label>`, because a drop target that
is *only* droppable excludes every keyboard user. Drag is never the only path.

## The progressive-enhancement contract

**Content Security Policy is `script-src 'self'`.** No `unsafe-inline`, no CDN
origin. Everything below follows from that one line, and each rule rules out an
easier design that fails in a way you would not notice for weeks.

**No inline scripts, and Alpine runs in CSP mode.** Alpine's CSP build cannot
evaluate expression strings, so `x-data` must name a **registered** component.
`ui.Alpine` exposes only named directives for exactly this reason — there is no
place to put an expression. An unregistered name renders a control that looks
right and does nothing, which is why
`TestEveryAlpineComponentUsedIsRegistered` walks every template and fails on one.

**The shell load order is asserted, not assumed.** `/static/ui-engines.js` (the
engine registry the fragments read) must precede `/static/ui-components.js` (the
registrations), which must precede `alpine-csp.min.js` — a fragment registering
on `alpine:init` is too late if Alpine already initialised. Head assets must also
not depend on which page was requested first: the shell is persistent and the
entry URL is an accident, so conditioning head assets on it would give the same
app different capabilities depending on where the user landed.

**Third-party engines are self-hosted, pinned, and lazily loaded.** Four engine
names appear in the manifests. `alpine` is the first-party CSP controller layer,
declared by 21 components; the three vendored runtimes are:

| Engine | Vendored asset | Used by |
|---|---|---|
| `chartjs` | `static/vendor/chartjs-4.5.1.umd.min.js` | The five chart renderers |
| `cally` | `static/vendor/cally-0.9.2.js` | The three date pickers |
| `sortablejs` | `static/vendor/sortablejs-1.15.7.min.js` | Kanban |

Each is recorded in its module's `vendors` block with source URL, version, byte
count, SHA-256, and license, and `ggg registry build` verifies those bytes on
every run — a swapped third-party file fails the build instead of shipping. A
check that has to be remembered is a check that eventually is not run.

The loader fetches an engine only when a matching `data-ui-engine` root first
appears, so a project that never renders a chart never pays for the charting
library. Four constraints on it are asserted against the source, because each
failure mode is invisible until the exact wrong thing happens:

- Scripts are appended to `document.head`, never into `#content` — htmx replaces
  `#content` on every navigation, so a script injected there works exactly until
  the first navigation.
- Every lazily injected script carries `integrity`. Without it the file can be
  swapped unnoticed.
- One fetch per engine, not per widget. Ten charts on a page must not open ten
  requests for the same runtime.
- The loader is plain DOM code, not an Alpine component. An element carries only
  one `x-data`, and every widget needing an engine already uses that slot for its
  own behavior — as a component, a chart could be a chart *or* could request its
  engine, never both.

It also listens on `htmx:after:process`, because htmx-inserted content needs its
engines too, and claims each root with `uiEngineRequested` before requesting,
because htmx processes nested content more than once.

**A third-party instance is never stored in Alpine state.** Alpine wraps
component state in a reactive Proxy, and a library that walks its own deeply
nested internals recurses through that proxy until the stack overflows, then
corrupts whatever it did reach. The observed symptom pointed at Chart.js while
the cause was ours, which is why this is a test rather than a comment. Instances
are created in `init()` and released in `destroy()`, which Alpine invokes during
DOM cleanup — so navigating away and back initialises exactly once.

## Using a component

The convention is uniform, so knowing one call teaches you all of them:

```go
@ui.Badge(ui.BadgeOpts{
    Label: "Active",
    Kind:  ui.KindSuccess,
    Attrs: ui.Attrs{TestID: "project-status"},
})
```

One options struct, `Attrs` for identity and behavior, closed enums for variants.
Primary content goes in templ children; secondary regions are `templ.Component`
fields. See [UI foundations](/docs/ui-foundations) for `Attrs`, `HX`, `Alpine`,
and the enum normalization rules, and use the **Copy call** button on any gallery
detail page for a working starting point.

If a component is missing from your project, install it:

```sh
go run ./cmd/ggg add component/data-table
```

The dependency closure comes with it. If you need to change how one behaves, edit
its file — it is yours — and see [Module anatomy and
lifecycle](/docs/modules) for how `ggg diff` and `ggg update` treat that edit.

## Where to go next

- [Component reference](/docs/component-reference) — all 172, generated.
- [UI foundations](/docs/ui-foundations) — the conventions behind every call.
- [Gallery and scenarios](/docs/gallery) — the live surfaces.
- [Frontend](/docs/frontend) — the shell, htmx fragments, and Alpine.
