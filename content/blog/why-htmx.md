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

Every mutation in GoGoGadget follows one rule: handlers return a **bare
fragment** only for non-boosted htmx requests, and a full page otherwise. That
one rule eliminates the classic hx-boost bug where a layout-less fragment gets
swapped into `<body>` during navigation.

Validation failures come back as `422` with the re-rendered form; successful
mutations either `HX-Redirect` or re-render the list with a toast. The browser
never sees a full reload unless it actually navigates.

## What stays on the client

Theme, dropdowns, modals, tabs, and toasts — about 5 KB of Alpine.js (CSP
build) registered as `Alpine.data` components in `static/app.js`. Strict CSP
with `script-src 'self'` holds because there is not a single inline script.

The result: a dashboard that feels like an SPA and ships like a static site.
