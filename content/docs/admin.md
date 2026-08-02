---
title: Admin
description: The /admin dashboard, the ADMIN_EMAIL grant, and the disable flow.
section: Features
weight: 14
---

Site admin is a flag on the local user mirror (`users.is_admin`), enforced by
`requireAdmin` at the end of the `/admin` chain:
`requireAuth → requireNotDisabled → requireOrg → loadPlan → requireAdmin`.

## Becoming admin

Set `ADMIN_EMAIL` in `.env`. The first time that address is seen — either by
the sign-in lazy upsert or by the `user.created` webhook — the app sets
`is_admin` via `SetUserAdminByEmail`, and the account is admin from then on.
No seed script, no manual SQL.

## Dashboards

- **`/admin`** — stat cards: total users, total orgs, active subscriptions,
  and MRR (sum of `PriceUSDMonthly` from the plan truth over subscriptions in
  `active`, `trialing`, `past_due` — see [Billing](/docs/billing)), plus the
  ten most recent signups.
- **`/admin/users`** — email search + pagination (20 per page), with a
  disable toggle per row.
- **`/admin/orgs`** — every org with member counts and plan badges.

## Disabling a user

`POST /admin/users/{id}/disable` toggles `users.disabled_at` and writes an
`admin.user_disabled` / `admin.user_enabled` audit event. The effect is
immediate: the target's **next request** hits `requireNotDisabled` and gets
the Disabled page with `403`. Re-enabling is the same button.

## Impersonation

There is no in-app impersonation UI — use the Clerk dashboard's
**Impersonate user** feature. It is zero application code, and the audit
trail stays in Clerk. See [Authentication](/docs/authentication).
