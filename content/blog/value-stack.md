---
title: The value-stack philosophy
description: Buy the undifferentiated 40%; own the product surface.
date: 2026-01-29
author: The GoGoGadget Team
---

Roughly 40% of a typical SaaS codebase is undifferentiated: password hashing,
OAuth state machines, email verification, sales tax, SMTP retries. None of it
wins you a customer, and all of it can lose you one when it breaks.

GoGoGadget's rule is simple: **if a managed service does it better, delegate
it; if it touches your product's identity, own it.**

| Delegate | Own |
| --- | --- |
| Auth, 2FA, invitations (Clerk) | Your pages and domain model |
| Sales tax and payments (Polar) | Your pricing and entitlements |
| Email delivery (Resend) | Your email content |
| Analytics infra (PostHog) | Your events and funnels |

Every delegated service hides behind one narrow Go interface
(`identity.Verifier`, `billing.Client`, `mail.Sender`, `analytics.Capturer`),
so swapping a provider means replacing one file — never a rewrite.

That is the whole philosophy: a stack small enough to hold in your head, with
the boring parts already solved.
