---
title: Why HTMX beats an SPA for most SaaS dashboards
description: The interactivity a SaaS dashboard needs is fragments, not a virtual DOM.
date: 2026-01-22
author: The GoGoGadget Team
---

The median SaaS dashboard is a handful of tables, forms, and modals. It does
not need a virtual DOM, a bundler, or a hydration step — it needs partial HTML
updates and a sprinkling of client state.

## The fragment model

Every mutation in GoGoGadget follows one rule: a handler returns a **bare
fragment** only when the client asked for one, and a full page otherwise. htmx 4
states that outright in the request — `HX-Request-Type: full|partial`, plus
`HX-Target` naming the element it will swap — so the server reads the client's
intent instead of inferring it from `HX-Boosted`. That kills the classic
hx-boost bug, where a layout-less fragment gets swapped into the document
during navigation, at the source.

Validation failures come back as `422` with the re-rendered form. Successful
mutations either re-render the list with a toast, or hand back an `HX-Location`
that re-fetches the destination and swaps it into the content box — a real
navigation, with history and title, that never rebuilds the page shell. Long-lived
third-party widgets in that shell (a Clerk user menu, say) keep their exact DOM
nodes, so nothing flashes. The browser only does a full reload when it genuinely
has to: another origin, or an auth boundary.

## Where htmx 4 earns its keep

Two features do real work here rather than looking good in a changelog:

- **Morph swaps.** `innerMorph` patches a table in place instead of replacing it,
  matching rows by `id`. Filter a list and the rows that survive keep their DOM
  nodes — along with focus, selection, and scroll position.
- **View Transitions.** `hx-swap="… transition:true"` hands the swap to the
  browser's View Transitions API, so page changes cross-fade with no CSS or
  JavaScript of our own. It is opted into per-swap, so in-page updates stay
  instant.

## What stays on the client

Theme, dropdowns, modals, tabs, and toasts — about 5 KB of Alpine.js (CSP
build) registered as `Alpine.data` components in `static/app.js`. Strict CSP
with `script-src 'self'` holds because there is not a single inline script.

The result: a dashboard that feels like an SPA and ships like a static site.
