---
title: GoGoGadget
description: An opinionated Go + templ + htmx + Postgres application framework, shipped as a source-module registry driven by the ggg CLI.
section: Start
weight: 1
---

GoGoGadget is an opinionated **Go + templ + htmx 4 + Alpine + Postgres**
application framework. It is not a template you copy: the catalog publishes
297 installable modules, and `ggg` resolves the ones you select into one
graph, writes them into your tree as ordinary source you own, and keeps them
updatable without ever overwriting an edit you made.

The promise, in order:

1. **Choose a profile** — `minimal`, `web`, `api`, `saas` or `full`.
2. **Choose one adapter and service target per required provider slot, per
   environment** — 18 slots, each with a zero-account local option and a
   maintained managed reference.
3. **Preview** — every command plans first and writes nothing while planning.
4. **Apply** — one journalled transaction that rolls back on failure.
5. **Own the source** — what lands is your code.
6. **Keep updating without losing local edits** — an upstream change to a file
   you edited is staged as a conflict, never applied over you.

Start at [Getting started](/docs/getting-started); the reasoning behind the
layering is in [Architecture](/docs/architecture).

## The stack

Every dependency is load-bearing, and every one is declared in a manifest.

| Layer | Choice | What you never build |
|---|---|---|
| Language | Go 1.26 | — |
| Templates | templ (type-safe, compiled) | String-concatenated HTML |
| Interactivity | htmx 4 + Alpine.js (CSP build) | A virtual DOM, a bundler, hydration |
| Styling | Tailwind CSS v4 (standalone binary, no node) | A JS toolchain |
| Database | Postgres + sqlc + goose | An ORM, runtime query magic |
| Everything else | A provider slot | A vendor lock-in you cannot change per environment |

Postgres is the substrate, not a slot you swap: sqlc, migrations,
transactional jobs, the audit ledger, notifications, schedules, usage,
webhooks, flags, content and default search are built on it. The
`ggg/database` slot chooses where Postgres runs, not whether it is Postgres.

## Where things are documented

| Section | Pages |
|---|---|
| **Start** | [Getting started](/docs/getting-started) |
| **Core** | [Architecture](/docs/architecture) · [Configuration](/docs/configuration) · [Database](/docs/database) · [Frontend](/docs/frontend) |
| **Features** | [Authentication](/docs/authentication) · [Organizations](/docs/organizations) · [Billing](/docs/billing) · [Email](/docs/email) · [Background jobs](/docs/background-jobs) · [Storage](/docs/storage) · [Notifications](/docs/notifications) · [Outbound webhooks](/docs/webhooks) · [Feature flags](/docs/feature-flags) · [AI](/docs/ai) · [API](/docs/api) · [Admin](/docs/admin) · [Content](/docs/content) · [Internationalization](/docs/i18n) · [SEO](/docs/seo) · [Observability](/docs/observability) |
| **Guides** | [Testing](/docs/testing) · [Deployment](/docs/deployment) · [Security](/docs/security) · [UI foundations](/docs/ui-foundations) · [Extending](/docs/extending) · [Roadmap](/docs/roadmap) · [Troubleshooting](/docs/troubleshooting) |
| **Modules** | [CLI and registry](/docs/cli) · [Module anatomy](/docs/modules) · [Module removal](/docs/module-removal) · [Components](/docs/components) · [Gallery and scenarios](/docs/gallery) · [Module reference](/docs/module-reference) · [Component reference](/docs/component-reference) · [Configuration reference](/docs/configuration-reference) |

The three reference pages are generated from the module manifests, so the
inventory cannot drift from what is installed.

## Philosophy

**Own the product surface; select the plumbing.** Password hashing, OAuth
handshakes, TOTP enrolment, sales tax, SMTP, object storage, a search index —
every application needs them and none of them differentiate one. GoGoGadget
puts each behind a seam with a typed capability, and makes the implementation
a per-environment choice rather than a rewrite.

Two rules make that work:

1. **Handlers never import a vendor SDK.** They hold the seam's capability;
   the adapter holds the SDK, its keys, its lifecycle and its health check.
   See [Architecture](/docs/architecture).
2. **Nothing degrades silently.** A managed adapter selected without its keys
   fails the boot — never a quiet fallback to the local implementation. Keys
   declared `production_required` are collected by the generated validator and
   reported as one joined error listing all of them; the adapters that check
   their own keys in their constructors fail on the first one reached. The
   local implementations are the *default* for development and test, so a new
   project runs end to end with zero accounts. See
   [Getting started](/docs/getting-started).
