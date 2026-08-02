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
rule, enforced by `Server.Render` in `internal/web/htmx.go`:

> Return a **bare fragment** only when `HX-Request` is present AND
> `HX-Boosted` is absent. Boosted navigations and plain requests get the full
> layout.

Boosted links send `HX-Request: true` too — without the `HX-Boosted` check, a
layout-less fragment gets swapped into `<body>` during navigation. That is the
classic hx-boost bug, and the rule exists to make it structurally impossible.
Handlers branch explicitly when a page has both shapes:

```go
if IsHX(r) && !IsBoosted(r) {
	s.Render(w, r, pageData, templates.ProjectsTable(d)) // bare fragment
	return
}
s.Render(w, r, pageData, templates.ProjectsPage(d)) // full layout
```

Redirects go through `Redirect(w, r, url)`: htmx requests get an
`HX-Redirect` header (a full browser navigation in htmx 2.x — layout, head,
and all), plain requests get a `303 See Other`.

Validation failures return **422** with the re-rendered form fragment — an
error summary plus field-level errors — so htmx swaps the form in place
instead of navigating away.

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
  hx-target="#table-container" hx-push-url="true" hx-include="this"
/>
```

- `delay:300ms` debounces keystrokes; `hx-push-url` keeps the URL shareable
  and the back button honest.
- Pagination links carry the same `hx-get` + `#table-container` +
  `hx-push-url` triple, with real `href`s as the no-JS fallback.
- Forms use `hx-disabled-elt="this"` as a double-submit guard, and
  `.htmx-indicator` elements fade in while a request is in flight.
- `<body>` emits `hx-headers='{"X-CSRF-Token": "…"}'` so every htmx request
  carries the CSRF token — see [Security](/docs/security).

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
