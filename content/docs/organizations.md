---
title: Organizations
description: Clerk organizations, mirror sync, roles, and the org switcher.
section: Features
weight: 7
---

Organizations (Clerk's name for teams) are the tenancy boundary: every
project, subscription, audit row, and API token belongs to exactly one org.
**Clerk is the source of truth; the local `orgs` and `org_members` tables are
a read-optimized mirror** kept in sync by webhooks. Org-scoped queries never
call the Clerk API on the request hot path.

## Mirror sync

`POST /webhooks/clerk` (signature-verified, idempotent via `webhook_events` —
see [Authentication](/docs/authentication)) processes nine event types:

| Event | Action |
|---|---|
| `user.created` | Upsert user; grant `is_admin` on `ADMIN_EMAIL` match; enqueue the welcome email; capture `user_signed_up` |
| `user.updated` | Upsert user; same admin grant |
| `user.deleted` | Delete the mirror row |
| `organization.created` / `organization.updated` | Upsert org (name, slug, image) |
| `organization.deleted` | **Revoke billing first**, then delete (below) |
| `organizationMembership.created` | Upsert membership; audit `member.joined` |
| `organizationMembership.updated` | Upsert membership; audit `member.role_changed` when the role changed |
| `organizationMembership.deleted` | Delete membership; audit `member.left` |

User display names are `first_name + last_name`, falling back to the email
prefix. Any processing error returns 500 so Clerk retries the delivery;
unknown event types are ACKed (200) and logged.

## Roles, including custom roles

`org_members.role` stores the Clerk role slug **verbatim — there is no CHECK
constraint**. The shipped roles are `org:admin` and `org:member`, but any
custom role you add in the Clerk dashboard flows through the webhook and
lands in the mirror untouched. A membership webhook can never wedge because
the app didn't anticipate a role you invented. Role-based branching in your
own handlers reads the raw string from the mirror or from
`Claims.OrgRole`.

## Invitations

Invitations are **Clerk-hosted**: members are invited from the Account
Portal's organization profile, which Settings → Organization links to
(`{CLERK_PORTAL_URL}/organization-profile`). There is no invitations table or
acceptance flow in the app — when the invitee accepts, Clerk fires
`organizationMembership.created` and the row appears in the mirror. The
member-count limit shown on the billing page is display-only for the same
reason.

## Switching orgs

A user can belong to many orgs; the **active org** is a claim on the session
JWT (`org_id`, `org_role`, `org_slug`). Two surfaces change it:

- **The sidebar switcher** — clerk-js mounts its prebuilt
  `OrganizationSwitcher` at `#org-switcher` on every app page. Switching
  rotates the JWT's org claim and all data re-scopes on the next request.
- **The SelectOrg page** — rendered by `RequireOrg` when the session has no
  active org but the user has at least one membership. In production each
  button calls `Clerk.setActive({ organization })` via the `selectOrg` Alpine
  component in `static/app.js`, then navigates to `/app`. In dev-bypass mode
  the buttons hit `GET /dev/switch-org?org=X` instead, which rewrites the
  synthetic `e2e:` cookie with the membership's role from the mirror.

A user with **zero** memberships is redirected to Clerk's hosted
`create-organization` page with `redirect_url={APP_URL}/app` — an invited
teammate whose invitation is still pending must never be told to found a
competing org.

## Org deletion revokes billing first

`organization.deleted` is the one event whose order matters:

1. Load the org's subscription row. If its status is `active`, `trialing`,
   or `past_due`, call `billing.Client.RevokeSubscription` against Polar.
2. **If the revoke fails, return 500** — Clerk retries the delivery. An org
   must never be deleted while the payment provider keeps charging it.
3. Only then delete the org mirror row. `ON DELETE CASCADE` removes its
   memberships and subscription.

If billing isn't configured the revoke is skipped with a warning log (there
is nothing to revoke against). The full subscription lifecycle is in
[Billing](/docs/billing).
