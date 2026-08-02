---
title: Hello, GoGoGadget
description: Why we built a Go + HTMX SaaS boilerplate on a curated value stack.
date: 2026-01-15
author: The GoGoGadget Team
---

Every SaaS starts the same way: three weeks rebuilding auth, billing, and email
before a single pixel of the actual product exists. GoGoGadget exists to delete
those three weeks.

The stack is deliberately small: **Go, HTMX, Alpine.js, templ, and Postgres**.
The undifferentiated heavy lifting — auth, taxes, email delivery, analytics —
is delegated to managed services that do it better than you will.

## What you get

- Auth with social login and 2FA (Clerk hosted — zero auth code in your repo)
- Subscription billing with checkout, portal, and entitlements (Polar.sh)
- Transactional email, a Postgres job queue, and an audit log
- A markdown blog (you are reading it), docs, RSS, and SEO plumbing

Clone it, `make setup && make dev`, and start on the part that makes you money.
