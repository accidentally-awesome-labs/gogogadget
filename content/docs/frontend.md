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

Shipped components: `themeToggle`, `mobileNav`, `dismissible`, `copy`,
`slugify`, `selectOrg`, `toastRoot`, `phConsent`. `x-cloak` hides pre-boot
markup (a CSS rule in `input.css`), and the vendored `@alpinejs/focus` plugin
is loaded for focus trapping on modal-style components. New client behavior
means a new `Alpine.data` registration — never an inline `x-data="{ … }"`
object literal.

Two rules the CSP build forces:

- **`$el` and `$refs` are only readable from `init()`**, never from an
  expression. A per-row control therefore carries its own `x-data`, so
  `this.$el` IS that control (`copy` reads `data-copy` this way).
- **Never interpolate a value into an expression.** `@click="copy('<token>')"`
  put a live API secret in the page's script surface; the value belongs in a
  `data-` attribute the component reads.

`dismissible` is the one dismiss-and-remember component: `data-key` names the
`localStorage` entry, so the announcement banner and the dashboard checklist
share it instead of each shipping their own.

## Dark mode

Dark mode is **token flipping, not `dark:` variants**. `static/app.js` loads
**without `defer`** so the theme IIFE runs pre-paint: an explicit `light`/`dark`
`theme` cookie wins, then `localStorage.theme`, then `prefers-color-scheme`; it
sets the `.dark` class on `<html>`. `input.css` maps it with
`@custom-variant dark (&:where(.dark, .dark *));`. The toggle
(`data-testid="theme-toggle"`) flips the class and persists the choice.

Every colour a template can name is a token, and the `.dark` block in
`input.css` re-declares the ones that change. That is why a `dark:` class in a
`.templ` file is a build failure (see [Enforcement](#enforcement)): it means a
token is missing, and the fix is to add the token — not the variant.

Open `/dev/gallery` and toggle the theme: nothing on that page carries a
per-theme class, so everything that changes changed because a token did.

## Design system

Three layers, one home each. Nothing lives in two places.

| Layer | Home | What belongs there |
|---|---|---|
| **Tokens** | `input.css` `@theme` + `.dark`; `theme.go` for email | every colour, the layout dimensions, the z-index scale |
| **Component classes** | `input.css` `@layer components` | every recurring visual: `.btn`, `.badge`, `.alert`, `.card`, `.table-card`, `.page-title` … |
| **templ components** | `components.templ`, `icons.templ` | every recurring *structure*: `@PageHeader`, `@TableCard`, `@Pagination`, `@Icon` … |

Templates consume those and nothing else — no raw hex, no palette ramp, no
`dark:` variant, no `!` override, no arbitrary length.

### Colour tokens

The brand **ramp** (`--color-brand-50` … `--color-brand-950`) is the palette.
Templates never name a ramp step; they name a semantic alias drawn from it.

| Token | Role | Light | Dark |
|---|---|---|---|
| `brand` | solid brand fill (buttons, meters, progress) | `brand-600` | `brand-600` |
| `brand-hover` | that fill on hover | `brand-500` | `brand-500` |
| `brand-fg` | text on the brand fill | white | white |
| `brand-fg-muted` | secondary text on the brand fill | `brand-100` | `brand-100` |
| `brand-text` | brand-coloured text on a normal surface | `brand-600` | `brand-400` |
| `brand-subtle` | brand-tinted background | `brand-50` | `brand-950` |
| `brand-subtle-fg` | text on that tint | `brand-700` | `brand-300` |
| `surface` | page background, cards | white | `#0b1120` |
| `surface-raised` | sidebars, table stripes, chips | `#f8fafc` | `#111a2e` |
| `border` | every border and divider | `#e2e8f0` | `#1e293b` |
| `fg` / `fg-muted` | body text / secondary text | `#0f172a` / `#475569` | `#e2e8f0` / `#94a3b8` |

The brand FILL deliberately does not flip: a primary button is the same colour
in both themes. Only the text and tint slots do.

State colour comes in four kinds — `success`, `warn`, `danger`, `info` — each
with the same six slots, so any kind substitutes for any other:

| Slot | Role |
|---|---|
| `{kind}` | solid fill |
| `{kind}-fg` | text on the solid fill |
| `{kind}-text` | `{kind}`-coloured text on a normal surface |
| `{kind}-subtle` | tinted background |
| `{kind}-subtle-fg` | text on the tinted background |
| `{kind}-border` | border on the tinted background |

### Structural tokens

Layout dimensions are tokens so a shell restyle is a value edit:
`max-w-page` (72rem), `max-w-narrow` (48rem), `w-sidebar`, `w-docnav`,
`h-topbar`/`top-topbar`, `h-navbar`/`top-navbar`, and `text-micro` (one step
below `text-xs`, for the notification counter).

Stacking order is three plain custom properties, because Tailwind has no
`--z-*` namespace: `z-(--z-nav)` (40, sticky header and drawer),
`z-(--z-overlay)` (50, toasts, dropdowns, consent, skip link) and
`--z-progress` (60, the navigation bar, used from CSS only).

### Component classes

Every variant family lists its base selector alongside its variants, so a bare
variant class still renders the whole component (`class="btn-primary"` works;
`class="btn btn-primary"` is the house style).

- **Buttons** — `.btn` + `.btn-primary` / `.btn-ghost` / `.btn-danger` /
  `.btn-inverse`, crossed with `.btn-sm` / `.btn-xs` / `.btn-icon`. The size
  axis is what removed 26 `!padding` overrides.
- **Inputs** — `.input` + `.input-sm` / `.input-xs`, `.label`, `.field-error`.
- **State** — `.badge-*` (six kinds), `.alert-*`, `.banner-*` (full-bleed shell
  notices), `.toast-*`.
- **Structure** — `.page-section`, `.page-narrow`, `.page-header`,
  `.table-card`, `.table-empty`, `.card`, `.card-actions`, `.toolbar`,
  `.form-actions`, `.meter` / `.meter-fill`, `.count-badge`, `.code-chip`.
- **Typography** — `.hero-title`, `.display-title`, `.page-title`,
  `.section-title`, `.error-code`, `.eyebrow`, `.prose`.
- **Navigation** — `.nav-link`, `.doc-link`, `.tab` / `.tab-bar`. All three key
  off `aria-current="page"`, set server-side by `navCurrent` and re-derived
  client-side by `syncAppNavigation` (the shell never swaps, so the server
  cannot update it after a navigation).

Utility layer beats component layer in Tailwind v4, so `text-danger-text` on a
`.btn-ghost` wins with no `!` needed. That is why the `!` rule can be absolute.

### templ components

`Kind` is the semantic colour axis shared by `Badge`, `Alert` and `Banner`:
`KindBrand`, `KindInfo`, `KindSuccess`, `KindWarn`, `KindDanger`,
`KindNeutral`. Domain values map onto it next to their data (`jobKind`,
`subKind`, `announcementKind`, `contentStatusKind`, `onOffKind`) — that mapping
is the only domain-specific part; the shades are not.

```go
templ Badge(text string, kind Kind)
templ BadgeTagged(text string, kind Kind, testID string)
templ Alert(kind Kind, role, class, testID string)   // children
templ Banner(kind Kind, role, testID string)         // children
templ PageHeader(title string)                       // children = right-hand actions
templ SectionHeading(text, class string)
templ TableCard(testID string)                       // children
templ TableEmpty(text string)
templ Pagination(page, totalPages int, baseURL, target string)
templ SearchToolbar(name, value, placeholder, ariaLabel, getURL, target, indicatorID, testID string)
templ EmptyState(title, body string)                 // children = CTA
templ StatCard(title, value, sub string)
templ PlanCard(plan billing.Plan, current bool)      // children = CTA
templ FormField(name, label, inputType, value, err, placeholder string, required bool)
templ FieldError(err string)
templ Meter(pct int, testID string)
templ CopyRow(value, copyLabel, copiedLabel string)
templ DefinitionList(rows []DefRow)
templ Tabs(items []NavItem, activePath string)
templ ErrorPage(code, message, detail string, showHome bool)
templ Icon(name IconName, class string, attrs ...templ.Attributes)
templ Spinner()
```

`role` on `Alert`/`Banner` is `"alert"` for something the user must act on and
`"status"` for a confirmation: `role="alert"` is assertive and interrupts a
screen reader mid-sentence, which a success message has not earned.

`testID` parameters emit `data-testid` only when non-empty, so a component can
be adopted without inventing test ids for sites that never had them.

### Chrome configuration

`templates/chrome.go` holds the product identity and navigation as data, not
markup: `BrandName`, `DocsEditBase`, `PublicNav`, `AppNav`, `AdminNav`,
`FooterColumns`. Labels are **i18n keys**, not resolved strings — these are
package-level values and `i18n.T` needs the request context. Overriding them at
startup restyles the shell without touching a template.

### Enforcement

`internal/web/templates/designsystem_test.go` reads every `*.templ` in the
package and fails the build on any of these, with **zero exemptions**:

| Rule | Because |
|---|---|
| no raw hex | colour lives in tokens; email colour lives in `emailStyle` |
| no `dark:` variant | a `dark:` in a template means a missing token |
| no non-brand palette ramp (`text-red-600` …) | use a state token |
| no numeric brand step (`bg-brand-600` …) | the ramp is `input.css`'s alone |
| no `!` utility override | an override means a missing variant |
| no arbitrary length (`text-[10px]`) | sizes come from the scales |
| no templ expression inside a quoted attribute | `class="badge { f(x) }"` is emitted literally and renders unstyled — this rule exists because it happened |

A companion test walks every `IconName` and asserts it renders an `<svg>`, so a
new const cannot be added without its switch arm.

### Which layer does my change belong to?

1. **A new colour** → a token in `@theme` (and `.dark` if it flips).
2. **A recurring visual** → a component class in `@layer components`.
3. **A recurring structure** → a templ component in `components.templ`.
4. **A one-off composition** → utilities, drawn from the token scales.

If you are about to type the same utility string a third time, it is step 2 or 3.

## Component gallery

`/dev/gallery` renders every token and every component in every variant on one
page. It is the reference to read before adding UI, and it is a visual baseline
(`gallery-light` / `gallery-dark`) plus an axe sweep, so a regression anywhere
in the component layer fails `make e2e`.

The route is registered only when `DEV_AUTH_BYPASS` is on, which
`internal/config` refuses under `APP_ENV=production`.

## Rebranding

In order, then `make generate`:

1. **The brand ramp** — `--color-brand-50` … `--color-brand-950` in `@theme`.
   Replace all eleven: the semantic aliases point at 600/500/400/50/950, and the
   `.dark` tints use the far ends.
2. **Semantic aliases** — only if the mapping changes (e.g. a light brand needs
   `--color-brand-fg: #000`).
3. **State triads** — only if the product's success/warn/danger hues differ.
4. **Structural tokens** — layout dimensions, if the shell proportions change.
5. **`emailStyle`** in `templates/theme.go` — mail clients strip `<style>` and
   cannot read custom properties, so email is the one surface that inlines hex.
   Mirror the light-mode token values.
6. **`chrome.go`** — `BrandName`, `DocsEditBase`, and the nav/footer lists.
7. **The catalogs** — `grep GoGoGadget internal/i18n/catalog_*.go`. The product
   name also appears inside translated prose (`email.footer`,
   `email.*.subject`), which belongs to the catalogs: word order around a brand
   differs per language, so it is not a template variable.
8. `make generate`, then `make visual-update` to regenerate the baselines.

The logo mark itself is `IconLogo` in `icons.templ` and
`static/favicon.svg`.

## Adding a page or component

1. Decide the layer (see [Which layer does my change belong to?](#which-layer-does-my-change-belong-to)).
   New markup is a templ component in `internal/web/templates/`, composed into a
   layout (`PublicLayout`, `AppLayout`, `AdminLayout`, `DocsLayout`) — reuse
   `@PageHeader`, `@TableCard`, `@SearchToolbar`, `@Pagination` rather than
   retyping their utility strings.
2. Write the handler in the matching `internal/web/handlers_*.go`, returning
   fragments per the rule above.
3. Register the route in `internal/web/routes.go` inside the right chain
   (public, `appChain`, or `adminChain`).
4. `make generate` (templ + sqlc + tailwind), then `make check`. The
   design-system test runs there, so a raw hex or a `dark:` fails the gate.

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

- **The topbar toggle** flips light↔dark locally, then `persistTheme` in
  `app.js` POSTs the RESULTING value to `/set-theme` with a plain `fetch`. It
  has to send the resolved value: when the stored preference is `system`, only
  the browser knows which way the OS points, so a server that flipped on its
  own would disagree with what the user just saw. The response is `204` — the
  class is already correct, there is nothing to swap.
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
