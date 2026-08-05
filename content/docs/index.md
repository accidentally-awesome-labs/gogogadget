---
title: GoGoGadget
description: A production-grade Go + HTMX SaaS boilerplate that delegates the commodity half to managed services.
section: Start
weight: 1
---

GoGoGadget is a full-featured SaaS boilerplate: marketing site, auth, teams,
subscription billing, transactional email, background jobs, admin dashboard,
blog, docs, and a public API — shipped as **one self-contained Go binary** you
can read end to end.

It is for Go developers and small teams who want to spend their first month on
the product surface, not on plumbing. Clone it, run `make setup && make dev`,
and you have a working SaaS — with zero SaaS accounts required for local
development.

## The stack

Every dependency is load-bearing; there are no duplicates and no ornamentation.

| Layer | Choice | What you never build |
|---|---|---|
| Language | Go 1.26 | — |
| Templates | templ (type-safe, compiled) | String-concatenated HTML |
| Interactivity | htmx 4 + Alpine.js (CSP build) | A virtual DOM, a bundler, hydration |
| Styling | Tailwind CSS v4 (standalone binary, no node) | A JS toolchain |
| Database | Postgres 16 + sqlc + goose | An ORM, runtime query magic |
| Auth, users, orgs, 2FA | Clerk (hosted Account Portal) | Password storage, OAuth flows, TOTP, invitations UI |
| Billing | Polar.sh (merchant of record) | Sales tax/VAT compliance, checkout plumbing |
| Transactional email | Resend | SMTP, TLS, deliverability |
| Product analytics | PostHog (optional, env-gated) | Analytics infrastructure |
| Error tracking | Sentry (optional, env-gated) | An error pipeline |

The pinned tool chain (templ, goose, sqlc, air, govulncheck) lives in `go.mod`
`tool` directives; Tailwind and the vendored frontend assets (htmx, Alpine,
clerk-js, Inter) are fetched by sha256-pinned scripts. No node/npm anywhere in
the shipped artifact.

## Feature map

| Feature | Where it lives | Docs |
|---|---|---|
| Auth with social login + 2FA | Clerk hosted portal + session middleware | [Authentication](/docs/authentication) |
| Teams/orgs with roles + invitations | Clerk orgs mirrored to Postgres | [Organizations](/docs/organizations) |
| Subscriptions, checkout, portal, entitlements | Polar.sh + `internal/billing` | [Billing](/docs/billing) |
| Transactional email | Resend behind `mail.Sender`, sent via jobs | [Email](/docs/email) |
| Background jobs | Postgres queue, `FOR UPDATE SKIP LOCKED` | [Background jobs](/docs/background-jobs) |
| Dashboard + CRUD example | Projects resource, HTMX fragments | [Frontend](/docs/frontend) |
| Public JSON API | Org-scoped Bearer tokens, `/api/v1` | [API](/docs/api) |
| Blog + RSS + SEO | Markdown in `content/blog` | [Content](/docs/content) |
| Docs (this site) | Markdown in `content/docs`, rendered at `/docs` | [Content](/docs/content) |
| Admin dashboard | Users, orgs, MRR, disable flow | [Admin](/docs/admin) |
| Audit log | Org-scoped activity feed | [Architecture](/docs/architecture) |
| Rate limiting, CSRF, strict CSP | `internal/web/middleware.go` | [Security](/docs/security) |

## Philosophy: buy the undifferentiated, own the product surface

Password hashing, email verification, OAuth handshakes, TOTP enrollment, sales
tax, SMTP — every SaaS rebuilds them, none of them differentiate one. GoGoGadget
delegates that half to managed services and keeps everything your users
actually see — pages, flows, data model, API — in plain Go code you own.

Two rules make that work in practice:

1. **Every external service hides behind one narrow interface** —
   `identity.Verifier`, `billing.Client`, `mail.Sender`, `analytics.Capturer`.
   Handlers never import an SDK, so swapping a provider means replacing one
   file. See [Architecture](/docs/architecture).
2. **Everything degrades cleanly.** An unconfigured service renders a 503
   "not configured" fragment or a log no-op — never a crash. A fresh clone
   runs the full app with zero accounts. See
   [Getting started](/docs/getting-started).

The result is a boilerplate small enough to hold in your head and complete
enough to charge money on day one.
