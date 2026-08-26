---
title: UI foundations
description: The three-layer design system, the one-options-struct convention, typed Attrs/HX/Alpine, closed enums, and the test that enforces all of it.
section: Modules
weight: 33
---

The UI is three layers with one home each. Every rule below exists because the
corresponding claim was once documented and false, and the fix was to make a
test own it rather than a paragraph.

| Layer | Home | What lives there |
|---|---|---|
| Tokens | `input.css` — `@theme`, and `:root` inside `@layer base` | Colour, spacing, radius, motion, control height, elevation, chart series |
| Component classes | `input.css` — `@layer components`, plus per-module fragments | `.btn`, `.input`, `.card`, `.badge-*`, `.k-*` |
| templ components | `internal/web/templates/ui` (`package ui`) | Every renderer, one per installable module |

Templates consume those and nothing else. `package ui` may import templ and
stdlib leaves but never `internal/web/templates`, billing, identity, or sqlc —
the direction is compiler-enforced, which is what keeps a presentation component
from acquiring a domain dependency. Page and domain templates stay in
`package templates` and import `ui`.

## The stylesheet entry

`input.css` is short and every line of it is load-bearing:

```css
@import "tailwindcss" source(none);

@source "internal/web/templates";

@import "./internal/web/styles/modules_registry_gen.css";

@custom-variant dark (&:where(.dark, .dark *));
```

`source(none)` plus one explicit `@source` means templates are the only class
source. Auto-detection would otherwise scan gitignored local artifacts and poison
the committed `static/app.css` with phantom utilities that CI, regenerating from
a clean checkout, would not produce.

`modules_registry_gen.css` is the generated import list of per-module CSS
fragments. Tailwind v4 bundles local imports, so each module owns its own
fragment and installing or removing one never patches this entry file by hand.

## Dark mode is token flipping

There is no `dark:` variant anywhere in a template, and the design-system test
rejects the string outright. Dark mode is a `.dark` block inside `@layer base`
that re-declares the token values every utility and component class is already
built on:

```css
.dark {
  --color-surface: #0b1120;
  --color-fg: #e2e8f0;
  --color-danger-text: #fca5a5;
  --color-focus-ring: var(--color-brand-400);
  --shadow-raised: 0 0 0 1px rgb(226 232 240 / 0.06);
  …
}
```

Restyling the whole product for dark is therefore editing one block, not auditing
every template. The comments in that block are worth reading for the cases where
a token *must* flip and why: the focus ring moves up the ramp because the light
value disappears against a dark surface, and elevation becomes a lifted border
because a drop shadow has nothing lighter to fall on.

`TestDarkModeFlipsInteractionTokens` asserts that the tokens whose light values
are unusable in dark actually do flip, so a new token cannot be added to the
light theme alone.

## One options struct

Every exported renderer has exactly this shape:

```go
templ Button(o ButtonOpts)
templ DataTable(o DataTableOpts)
templ MarkdownEditor(o MarkdownEditorOpts)
```

One parameter, named `o`, of a type named after the renderer. No positional
parameters, no variadic attribute lists, no overloads. Adding an option is a new
field, which is a source-compatible change for every existing caller — the
reason this convention is worth its verbosity.

Every `NameOpts` carries `ui.Attrs` as a field named `Attrs`. Primary content
arrives as templ children; named secondary regions are `templ.Component` fields
that omit their wrapper entirely when nil.

The full list of 172 renderers with their exact signatures is on the [Component
reference](/docs/component-reference) page, generated from the manifests.

## Typed attributes

```go
type Attrs struct {
	ID         string
	Class      string
	TestID     string
	Title      string
	Decorative bool
	Data       map[string]string
	Alpine     Alpine
	HX         HX
}
```

**There is no arbitrary-attribute map.** That absence is the design. Components
own their semantic attributes — `role`, every `aria-*`, `tabindex`, `type`, and
the base class — and a caller cannot reach them through `Attrs` at all. A
component that genuinely needs a caller-influenced semantic attribute takes a
typed field for it and decides the value itself.

Each component builds one `templ.Attributes` map and spreads it once on its root.
Three properties hold on every component, in one place:

- `data-ui="<name>"` is on every root. That marker is what lets the
  gallery-coverage test compare *rendered* components against the installed
  registry instead of trusting a hand-kept list.
- The component's own base class always survives. `Class` is **appended**, never
  substituted, so no caller can drop `badge` from a `Badge`.
- `TestID` is omitted when empty, with two preserved exceptions: `Metric`
  defaults to `data-testid="stat-card"` and `FieldError` to
  `data-testid="form-error"`, because existing Playwright contracts assert on
  them. A non-empty `Attrs.TestID` overrides either.

`Decorative` deserves its own note. It is deliberately not named `AriaHidden` and
deliberately not a bare `aria-hidden`: `aria-hidden` alone on a focusable element
leaves the control tabbable while hiding it from assistive technology, which
strands a screen-reader user on a control that announces nothing. `Decorative`
emits `inert` alongside it so the two statements cannot disagree.

### Alpine and HX

```go
type Alpine struct {
	Data, Show, Text, Model, Ref, Trap, If, For, Key string
	Cloak bool
	Bind, On map[string]string
}

type HX struct {
	Get, Post, Put, Patch, Delete string
	Target, Select, Swap, Trigger string
	Sync, Include, Indicator      string
	PushURL, Confirm, Encoding    string
	Vals, Headers                 map[string]string
	Boost, Disable, HistoryElt    bool
}
```

Both are closed sets of named directives, not attribute bags. Under
`script-src 'self'` Alpine's CSP build cannot evaluate expression strings, so
`Alpine.Data` must name a **registered** component — an unregistered name renders
a control that silently does nothing.
`TestEveryAlpineComponentUsedIsRegistered` walks every `x-data` name in every
template and fails on one that no fragment registers, which is why this is a test
and not a code-review item.

Where a component declares its own `HX` field beside `Attrs`, that field is
applied **last** and wins per attribute. A caller who wrote `HX:` on this button
is naming this button's request; losing to a value that arrived inside an `Attrs`
literal would be the surprise.

Four reflection guards make the field lists above trustworthy rather than
decorative. `attributes()`, `applyAlpine()`, and `applyHX()` each enumerate
their struct's fields **by hand**, so a field added to `Attrs`, `Alpine`, or
`HX` reaches nothing and says nothing — the caller sets an option and it is
silently dropped. `TestEveryHXFieldReachesTheElement`,
`TestEveryAttrsFieldReachesTheElement`, and
`TestEveryAlpineFieldReachesTheElement` in `ui/attrs_test.go` take the field
list from the type by reflection and pair each field with the attribute key that
proves it landed, so a new field with no emitter fails instead of being skipped.
`TestEveryOptsHXFieldReachesTheElement` does the same for the components that
declare their own `HX` field beside `Attrs`.

This is not hypothetical. Four `HX` fields shipped declared and never emitted,
and `MenuItem.Attrs` was declared and rendered nowhere. Both read as working
API. The general rule the guards encode: **if you hand-enumerate a struct's
fields anywhere — an attribute emitter, a digest seed, a serialiser — reflect
over the type or format the whole value.** A hand-written list is correct
exactly until the next field.

## Closed enums

Every variant axis is a distinct Go string type in `ui/enums.go` (and `ui/ui.go`
for `Kind`). Two properties make them worth having: passing a size where an
emphasis belongs does not compile, and an unrecognised value normalizes to a
defined default rather than rendering a class-less element.

| Type | Values | `Value()` normalizes to |
|---|---|---|
| `Kind` | `brand` `info` `success` `warn` `danger` `neutral` | `neutral` |
| `Size` | `xs` `sm` `md` `lg` | `md` |
| `Emphasis` | `subtle` `solid` `outline` | `subtle` |
| `Action` | `ghost` `primary` `danger` `inverse` `link` | `ghost` |
| `ButtonType` | `button` `submit` `reset` | `button` |
| `Live` | `""` `polite` `assertive` | `""` (off) |
| `Density` | `comfortable` `compact` | `comfortable` |
| `Orientation` | `horizontal` `vertical` | `horizontal` |
| `SortDirection` | `""` `asc` `desc` | `""` (none) |

Plus closed layout enums for gap, width, height, ratio, breakpoint, alignment,
placement, padding, and side. Every type exposes its complete ordered plural
slice (`ui.Kinds`, `ui.Sizes`, …) and a `Valid() bool`.

The defaults are chosen so a typo cannot *escalate* anything. `Emphasis` falls to
subtle because a control that shouts by accident is worse than one that whispers.
`Action` falls to ghost because defaulting to primary would let a typo promote a
button's apparent importance, and defaulting to danger would dress a harmless
action as destructive. `ButtonType` is a closed enum at all because HTML's own
default is `submit`, which is the classic accidental-submit bug.

Normalization deliberately does **not** trim whitespace or fix case. `"Danger"`
and `"danger "` are typos, and quietly accepting them would turn the closed set
into a suggestion.

`Value()` and `Valid()` are for different jobs, and the gallery routes show the
distinction: `/dev/gallery/{family}` calls `Valid()` and 404s an unknown family
rather than normalizing, because a URL that silently shows a different page
teaches the reader the wrong thing.

## What the design-system test rejects

`internal/web/templates/designsystem_test.go` is where the three-layer claim is
enforced. `TestDesignSystemLayering` applies seven pattern rules to every
`.templ` file in `internal/web/templates` with **zero exemptions** — an
exemption list is precisely how the previous version of this claim rotted. Each
failure prints the fix rather than only the sin:

| Rejected | Why | Fix it names |
|---|---|---|
| Raw hex colour (`#1e293b`) | A colour outside the token layer cannot be re-themed | Add a token to `@theme`; email colour belongs in `emailStyle` |
| `dark:` | Dark mode is token flipping | Declare the value under `.dark` |
| Palette ramp (`bg-red-500`, `text-slate-700`) | Bypasses semantic meaning | Use `success` / `warn` / `danger` / `info` and their `-fg`, `-text`, `-subtle`, `-subtle-fg`, `-border` slots |
| Numeric brand step (`bg-brand-600`) | The ramp belongs to `input.css` alone | Use `brand`, `brand-hover`, `brand-fg`, `brand-text`, `brand-subtle`, `brand-subtle-fg` |
| `!` utility override (`!p-0`, `!text-sm`) | An override means a missing variant | Add a size or variant class in `@layer components` |
| Arbitrary length (`w-[13px]`, `h-[4.5rem]`) | Off-scale values accumulate into drift | Use the spacing/text scales, or add a structural token |
| A templ expression inside a quoted attribute (`href="{ x }"`) | templ does not interpolate inside quotes, so this silently emits the literal | Write `attr={ expr }` or `class={ "a", expr }` |

The rest of the file closes gaps that were all real at some point:

- **`TestEveryKindIsStyledForEveryStateFamily`** — all six kinds must resolve to
  a real class in every family that accepts a `ui.Kind`. Before the matrix
  existed, `alert-brand`, `alert-neutral`, `banner-brand`, `banner-success` and
  `banner-neutral` had no rules at all, so a typed, reachable enum value rendered
  a box with no border and no background.
- **`TestEverySizeIsStyledForEveryControlFamily`** — and the subtler half: naming
  a class in another rule's selector list is not styling it. `.btn-lg` appeared
  in the base, disabled and focus lists the whole time while defining nothing, so
  the entire large size axis was dead. A size must contribute a class the default
  does not, *and* that class must head its own declaration block.
- **`TestBuiltStylesheetCarriesKindVariables`** and
  **`TestVarConsumedTokensSurviveTheBuild`** — a rule present in `input.css` and
  dropped by the build is still an unstyled component. Tailwind tree-shakes
  `@theme` entries no class references, which is exactly what happened to the
  chart series: read only through `var()`, they were kept alive by the gallery's
  swatch labels and vanished otherwise. Such tokens are declared in `:root`
  instead, and `TestVarOnlyTokensAreNotThemeEntries` keeps them there.
- **`TestFocusTreatmentIsSingleSourced`** — one focus treatment, applied through
  the token. Two components that both mean "focused" must not look different, and
  an interactive element with no focus style is unusable by keyboard.
- **`TestReducedMotionCollapsesMotionTokens`** — the OS setting is honoured once,
  at the token. A rule that forgets the media query is a rule that ignores the
  user.
- **`TestGalleryCoversEveryInstalledComponent`** — rendered `data-ui` markers
  versus the generated registry. A component installed but never rendered in the
  gallery is invisible to every reviewer and to every visual and accessibility
  gate.
- **`TestNoTemplateHandRollsAComponentRoot`** — a template must never rebuild a
  component's root by copying its Alpine hook and data attributes. The copy
  drifts the moment the component changes, and it silently loses whatever the
  component computes.
- **`TestIconRegistryIsComplete`** — an icon const with no switch arm renders
  nothing, which is a silently missing icon.

### Two things this page does not claim

The normalization contract is "unknown value renders the neutral or default
variant". Components do **not** currently emit a `data-ui-invalid` marker
alongside it; there is a source comment in the dev fragment handlers that
describes one, but no renderer produces it. Read `Value()` and `Valid()` as the
contract.

And the seven pattern rules are scoped to `internal/web/templates/*.templ`:
`templFiles` reads that one directory rather than walking into it, so the
renderers under `internal/web/templates/ui` are **not** covered by the raw-hex,
`dark:`, palette-ramp, `!`-override, or arbitrary-length checks. They are
covered by the rest of the file — the Alpine registration scan globs
`ui/*.templ` explicitly, and the kind/size matrices, gallery coverage, and
hand-rolled-root tests all reach them. Treat the seven rules as binding
convention everywhere and enforced automatically in one place, not as a
guarantee about `ui/`.

## Where to go next

- [Component usage](/docs/components) — finding a component and reading its
  reference entry.
- [Component reference](/docs/component-reference) — all 172 signatures,
  generated.
- [Gallery and scenarios](/docs/gallery) — the dev surfaces that render them.
- [Frontend](/docs/frontend) — htmx fragments, Alpine CSP, and the shell.
