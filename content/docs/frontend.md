---
title: Frontend
description: HTMX fragments, Alpine CSP components, and the design-token system.
section: Features
weight: 11
---

The frontend is server-rendered templ with htmx for partial updates and ~5 KB
of Alpine.js (CSP build) for client state. No bundler, no SPA, no hydration.

## The fragment rule

Every handler that serves both navigations and in-page updates follows ONE
rule, enforced by `Server.Render` in `internal/web/htmx.go`. htmx 4 states the
client's intent in the request, so the server reads it instead of guessing:

| Request says | Response |
|---|---|
| `HX-History-Restore-Request` | full page — htmx lifts the `hx-history-elt` out of it |
| `HX-Target` is the content box | full page — replacing `#content` outright *is* a navigation |
| `HX-Request-Type: full` | full page — the client will select what it needs |
| `HX-Request-Type: partial` | bare fragment |
| neither header (pre-4.0 client) | fragment unless `HX-Boosted` |

`HX-Request-Type` is new in htmx 4: `full` when an `hx-select` is in play or the
target is `<body>`, `partial` otherwise. Honouring it makes the classic
hx-boost bug — a layout-less fragment swapped into the document during
navigation — structurally impossible, and it lets a request ask for a full page
*without* being boosted, which is what server-driven navigation does.

Handlers branch explicitly when a page has both shapes:

```go
if wantsFragment(r) {
	s.Render(w, r, pageData, templates.ProjectsTable(d)) // bare fragment
	return
}
s.Render(w, r, pageData, templates.ProjectsPage(d)) // full layout
```

## Redirects: hard vs soft

Two helpers, and the choice matters:

- `Navigate(w, r, url)` — **soft**, for in-app destinations. htmx clients get
  `HX-Location` scoped to `#content`, so the destination is fetched over AJAX
  and swapped into the content box. History is pushed and the document title
  updates, but the shell is never re-created: clerk-js stays mounted, so there
  is no re-mount flash. Plain clients get a `303`. Project create/update/archive
  and the admin disable toggle use this.
- `Redirect(w, r, url)` — **hard**, for another origin (Polar checkout, the
  Clerk portal) or when the whole document must be rebuilt (auth boundary,
  layout change). htmx clients get `HX-Redirect`, plain clients a `303`. This
  costs a page load, which re-initializes clerk-js.

Pair `Navigate` with `Toast`, never `FlashToast`: the flash variant parks the
message in `sessionStorage` for the next document load, which never comes.

Validation failures return **422** with the re-rendered form fragment — an
error summary plus field-level errors — so htmx swaps the form in place
instead of navigating away.

## App navigation swaps only `#content`

The vendored runtime is **htmx 4** (`static/vendor/htmx.min.js`). Every nav link
— public, docs, and authenticated app — uses
`hx-boost="true" hx-target="#content" hx-select="#content" hx-swap="outerHTML transition:true show:top"`,
and each layout's `<main id="content">` carries `hx-history-elt="true"` so a
Back-navigation re-fetch is swapped into the same box.

The shell is deliberately **never** a swap target. clerk-js renders its dropdown
menus as portals appended directly to `<body>` — siblings of its mount roots —
so swapping or morphing `<body>` deletes them and the dropdowns go dead; the
shell's Alpine bindings break the same way. Scoping the swap to `#content`
leaves every other child of `<body>` untouched, which is the only reliable way
to host long-lived third-party widgets. The cost is that the persistent
sidebar's server-rendered `aria-current` would go stale, so `static/app.js`
re-syncs it from `data-nav-match` on `htmx:after:settle`. See
[Authentication](/docs/authentication).

`transition:true` routes the swap through the browser's **View Transitions API**
for a cross-fade between pages. It is opted into per-swap rather than globally
(`htmx.config.transitions`) so table search, row deletes and polls stay instant,
and `input.css` cancels the animation under `prefers-reduced-motion`.

`show:top` brings the top of the new content into view. It is required, not
decorative: htmx defaults only boosted *forms* to scrolling, so a boosted link
keeps the previous scroll offset and lands you mid-page. (`scroll:window:top`
reads like the same thing but is a no-op in 4.0.0-beta6 — verified, not assumed.)

### The chrome rule

Because a navigation replaces `#content` and nothing else, **every page reachable
by a boosted link must render identical chrome around it.** Anything that differs
belongs *inside* the swap target, or it strands itself on the next page.

That is why the docs table of contents lives inside `#content` (`docsBody`) while
`PublicLayout` and `DocsLayout` share one `publicShell`: with the sidebar outside,
navigating Docs → Pricing left the table of contents behind, and Home → Docs
never grew one. `TestPublicAndDocsLayoutsShareChrome` renders both layouts from
the same `Page` and diffs everything outside `#content`, so re-introducing
layout-specific chrome fails the build.

The layout families are `public`/`docs` (one shell) and `app`/`admin`
(`AdminLayout` *is* `AppLayout`). No boosted link crosses between families —
sign-in, sign-out and the billing portal are hard navigations — so a swap never
lands in a shell that cannot host it.

### Anchor links are not boosted

`hx-boost` is declared per link, and links with a `#` fragment deliberately
opt out (`isAnchorLink`). Boosting one makes htmx fetch the page, repaint
`#content` at the top of the document, and only then scroll — a visible flash of
the wrong section. Left alone, the browser scrolls natively with no request at
all when the page is already open, and lands on the fragment when it is not.

## Swap methods

htmx 4 ships morphing as a first-class swap, and this app uses it where content
is *updated* rather than *replaced*:

| Interaction | Swap | Why |
|---|---|---|
| Navigation | `outerHTML transition:true show:top` | a different page: cross-fade it and land at the top |
| Table search / pagination | `innerMorph` | patches rows in place, so a row that survives a filter keeps its DOM node — and any focus or selection in it |
| Billing status poll (`every 2s`) | `outerMorph` | re-rendering the same card twice a second; morphing avoids the flicker of replacing it |
| Row delete | `outerHTML` on `closest tr` | the row really is going away |

Morph matches elements **by `id`**, so every table row carries a stable one
(`project-42`, `user-<clerk id>`, `audit-99`). Without ids morph degrades to
replacement and buys nothing — the e2e suite asserts the surviving node is the
same DOM element to keep that honest.

## Request feedback

Mutating forms carry `hx-disable="this"` (htmx 4's rename of 2.x's
`hx-disabled-elt`) so the control is disabled for the duration of the request,
plus a `Spinner()` component that htmx fades in via the `htmx-request` class.
Search inputs name their spinner with `hx-indicator` because it is a sibling
rather than a child, and add `hx-sync="this:replace"` so a slow in-flight search
is aborted by the next keystroke instead of landing on top of a newer result.

## Configuration

htmx reads its config from a `<meta name="htmx-config">` tag in `headScripts`,
which is applied at init — before any of our JS could run:

- `includeIndicatorCSS: false` — htmx would otherwise inject a `<style>` element
  for `.htmx-indicator`. Those rules live in `input.css` instead, so there is
  one source of truth and one less inline style under CSP.

Everything else is left at the htmx 4 defaults on purpose:

- Attribute inheritance is **explicit**. The only inherited attribute this app
  needs is the body's CSRF header, declared as `hx-headers:inherited` — no
  `implicitInheritance` compatibility shim.
- `noSwap` stays `[204, 304]`, because the 422 validation and 503
  not-configured fragments must swap.
- `defaultTimeout` is 60s (htmx 2 had none), which aborts a hung request
  instead of leaving a form disabled forever.

Run the official upgrade checker against this tree with
`python3 upgrade-check.py --ext .templ internal/web static` (ships in the
`htmx.org` npm package). It reports only `hx-disable` hits, which are false
positives here: those are already the htmx 4 spelling of 2.x's
`hx-disabled-elt`.

## Toasts

`Toast(w, "success", "Project created")` sets an
`HX-Trigger: {"toast":{"type":"success","message":"…"}}` response header.
htmx dispatches a bubbling `toast` event; the `toastRoot` component in
`static/app.js` renders it and auto-dismisses after five seconds. Toasts carry
`role="status"` and `data-testid="toast"`.

## Row deletes

Deletes are row swaps, confirmed in the browser:

```html
<button
  hx-delete="/app/projects/42"
  hx-confirm="Delete this project? This cannot be undone."
  hx-target="closest tr"
  hx-swap="outerHTML"
>Delete</button>
```

The handler returns `200` with an empty body; htmx replaces the `<tr>` with
nothing.

## Search and pagination

```html
<input type="search" name="q"
  hx-get="/app/projects" hx-trigger="input changed delay:300ms, search"
  hx-target="#table-container" hx-swap="innerMorph" hx-push-url="true"
  hx-include="this" hx-sync="this:replace"
  hx-indicator="#projects-search-indicator"
/>
```

- `delay:300ms` debounces keystrokes; `hx-sync="this:replace"` guarantees the
  last keystroke wins even when a slow search is still in flight.
- `hx-push-url` keeps the URL shareable and the back button honest.
- Pagination links carry the same `hx-get` + `#table-container` + `innerMorph` +
  `hx-push-url` set, with real `href`s as the no-JS fallback.
- Forms use `hx-disable="this"` as a double-submit guard, and the `Spinner()`
  component (`.htmx-indicator`) fades in while a request is in flight.
- `<body>` emits `hx-headers:inherited='{"X-CSRF-Token": "…"}'` so every htmx
  request carries the CSRF token — see [Security](/docs/security). The
  `:inherited` suffix is required in htmx 4, where inheritance is explicit.

## Alpine components, CSP-safe

The content security policy is `script-src 'self'`: **no inline scripts and
no inline Alpine expressions**. All component logic is registered with
`Alpine.data` in `static/app.js`; templates only reference it:

```html
<div x-data="dropdown">
  <button type="button" @click="toggle">…</button>
  <div x-show="open" x-cloak @click.outside="close">…</div>
</div>
```

Shipped components: `themeToggle`, `dropdown`, `modal`, `tabs`, `mobileNav`,
`clipboard`, `checklist`, `selectOrg`, `toastRoot`. `x-cloak` hides pre-boot
markup (a CSS rule in `input.css`), and the vendored `@alpinejs/focus` plugin
is loaded for focus trapping on modal-style components. New client behavior
means a new `Alpine.data` registration — never an inline `x-data="{ … }"`
object literal.

## Dark mode

`static/app.js` loads **without `defer`** so the theme IIFE runs pre-paint:
`localStorage.theme` wins, falling back to `prefers-color-scheme`, and sets
the `.dark` class on `<html>`. `input.css` maps it with
`@custom-variant dark (&:where(.dark, .dark *));`. The toggle
(`data-testid="theme-toggle"`) flips the class and persists the choice.

## Design tokens

All styling flows through one `@theme` block in `input.css`:

- `--color-brand-50`…`--color-brand-950` — the brand scale (indigo by
  default). Rebranding is editing this one block.
- Semantic tokens — `--color-surface`, `--color-surface-raised`,
  `--color-border`, `--color-fg`, `--color-fg-muted` — with `.dark`
  overrides.
- Component classes — `.btn`, `.btn-primary`, `.btn-ghost`, `.btn-danger`,
  `.input`, `.label`, `.card`, `.badge`, `.table`, `.link`, `.alert-success`,
  `.alert-error`.

Templates use ONLY semantic tokens and component classes — never raw hex
values or ad-hoc pixel sizes.

## Adding a page or component

1. Write the templ component in `internal/web/templates/`, composed into a
   layout (`PublicLayout`, `AppLayout`, `AdminLayout`, `DocsLayout`).
2. Write the handler in the matching `internal/web/handlers_*.go`, returning
   fragments per the rule above.
3. Register the route in `internal/web/routes.go` inside the right chain
   (public, `appChain`, or `adminChain`).
4. `make generate` (templ + sqlc + tailwind), then `make check`.

Put `data-testid` on every element a test will assert on — Playwright never
selects by visible copy. See [Testing](/docs/testing).

## Theme

Three values: `system` (default), `light`, `dark` — stored on `users.theme`
for signed-in users and mirrored into a non-HttpOnly `theme` cookie.

The cookie is not a duplicate for its own sake: the pre-paint script in
`app.js` cannot query the database, and it runs before first paint precisely
to avoid a flash of the wrong theme. Resolution order there is **an explicit
`light`/`dark` cookie (the account's saved choice) → localStorage (this
browser's own choice) → OS `prefers-color-scheme`**.

A cookie of `system` does *not* outrank localStorage: it means the account
expressed no preference, so the per-browser choice is the more specific one.

The server also renders `class="dark"` on `<html>` itself when the resolved
theme is dark, so a fresh device is correct on the very first byte — before
any JavaScript executes. `system` deliberately renders **no** class: only the
browser knows the OS setting, and guessing server-side reintroduces the flash.

Two controls write it:

- **The topbar toggle** flips light↔dark. It `hx-post`s `/set-theme` with no
  value and the server flips from the same current value the client just
  flipped from, so repeated clicks stay in agreement. It cannot send the
  desired value: the vendored Alpine CSP build forbids the inline expression
  that would compute one, and a value baked in at render time goes stale after
  the first click. The response is `204` — the class is already correct.
- **Settings → Account → Appearance** sets a value exactly. Those forms carry
  `returnTo`, so the server answers with a hard redirect: the theme class
  lives on `<html>`, which boosted navigation never re-renders.

## Loading feedback

Navigation swaps `#content` and nothing else, so until the response lands the
old page sits there untouched — on anything slower than a local network, a
click looked like nothing happened. htmx's `.htmx-request` marks the element
that *issued* the request, which is right for a button's spinner and useless
for a whole-page change.

`app.js` therefore sets `data-navigating` on `<html>` while a swap of the
content box is in flight, and `input.css` draws everything from that flag: a
2px progress bar pinned to the top edge, and the stale content dimmed to 60%.

Three details carry the design:

- **A 150ms delay before anything appears.** A local navigation finishes well
  inside it, so the common case shows nothing at all and only a genuinely slow
  response produces a bar. An indicator that flickers on every click is worse
  than no indicator.
- **The flag clears before the swap, not after the request.** The swap
  replaces `#content` wholesale, and an element inserted while the flag is
  still set paints dimmed *from birth* — CSS transitions do not apply to
  initial values — so every fast navigation would flash grey. Clearing on
  `htmx:before:swap` (with `htmx:finally:request` as the fallback for aborts
  and errors) puts the new element into a clean document.
- **Only navigations count.** The flag keys off the resolved swap target being
  `#content`, so table search, pagination and the billing poll — which keep
  their content on screen and have their own indicators — never dim the page.

The event names are htmx 4's: colon-namespaced (`htmx:before:request`, not
`htmx:beforeRequest`), with a single `ctx` on the detail carrying the resolved
target.

### Skeletons

`.skeleton` is the placeholder for regions that load *after* first paint —
fragments with `hx-trigger="load"`, which are empty until their fetch returns.
It reserves the space the content will take so nothing shifts when it arrives;
the notification badge uses it.

There is deliberately no skeleton anywhere content already exists. Replacing a
rendered table with a shimmering grey copy of itself is a downgrade: the user
could read the old rows a moment ago. Skeletons are for empty space, and the
progress bar covers the rest.

Both respect `prefers-reduced-motion`: the bar still appears and the content
still dims, but the animation is dropped rather than the feedback.
