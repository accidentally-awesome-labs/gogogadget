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

**Impersonate** on `/admin/users` starts an in-app "view as" session: a
2-hour `impersonation_sessions` row + an opaque `ggg_imp` cookie, then a
redirect into the target's app view with an amber **Viewing as** banner
outside `#content` (it survives boosted navigation). Session semantics:

- `sessionLoad` applies the override **after** the real JWT verify and mirror
  upsert — impersonation never bypasses Clerk; the admin's own session must
  stay valid.
- Downstream guards are unchanged: `/admin` correctly **403s** while
  impersonating (the target is not a site admin), and `loadPlan` resolves the
  target org's plan.
- Validation is live: an ended/expired session, a demoted admin, or a
  missing membership clears the cookie on the next request.
- Both transitions are audited (`impersonation.start` / `.stop`), and the
  exit button ends the session, clears the cookie, and hard-redirects to
  `/admin`.
- Disabled targets and org-less targets are rejected (422); the org is the
  optional `org` form field, else the target's first membership.

## Audit, jobs, and announcements viewers

- `/admin/audit` — the platform-wide audit trail (every org, every actor),
  filterable by action or org id, paginated 20/page. The org-scoped
  `/app/activity` page remains the member view of the same table.
- `/admin/jobs` — the Postgres queue at a glance. The `status` column is a
  projection: `pending` (never claimed), `retrying` (claimed under the 5-min
  visibility lease **or** waiting out exponential backoff — deliberately
  indistinguishable, the lease IS a retry in effect), `running`, `done`, and
  `dead` (attempts exhausted). Dead rows offer a **Requeue** button:
  `done_at`/`attempts`/`last_error` reset and the job is claimable
  immediately; requeueing a non-dead row is a guarded no-op.
- `/admin/announcements` — create (inactive by default), activate, deactivate,
  delete platform banners. At most one banner is active at a time — the
  `announcements_one_active` partial unique index enforces it, and activating
  one deactivates the rest. The active banner renders under the app topbar
  (kind-colored: info = brand, warning = amber, critical = red) with an
  optional link; each visitor can dismiss it per-browser. Admin mutations
  invalidate a 30s server-side cache, so activation takes effect on the next
  render.
