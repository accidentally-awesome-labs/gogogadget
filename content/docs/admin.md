---
title: Admin
description: The /admin dashboard, staff roles, the ADMIN_EMAIL grant, and the disable flow.
section: Features
weight: 15
---

Staff access is a role on the local user mirror (`users.admin_role`), with two
tiers:

| Role | Reads `/admin` | Changes platform state |
|---|---|---|
| `support` | yes | **no** |
| `admin` | yes | yes |

Anything else (the empty default) gets the 403 page.

## The read/write boundary

The `/admin` chain is
`requireAuth → requireNotDisabled → requireOrg → loadPlan → requireStaff → requireAdminWrite`.

`requireStaff` admits both roles. `requireAdminWrite` then refuses any request
that is **not a GET or HEAD** unless the caller is a full `admin`.

That rule is by method rather than by route list on purpose: the admin surface
is fifteen mutating routes today and will be more tomorrow, and a per-handler
annotation is a thing you forget exactly once. Every read in the admin area is
a GET; a GET that mutated would already be a CSRF bug.

Support-visible pages hide the controls they cannot use — a button that always
403s is worse than no button. Templates read the capability from the request
context (`templates.AdminWrite(ctx)`, set in `Render`), which defaults to
**false**, so a new admin page that forgets the gate renders read-only rather
than offering actions that fail. State stays visible: support sees *whether* a
flag is on, just not the toggle.

Impersonation is not a read. A session ends the moment its owner drops below
`admin`, mid-session, on the next request.

## Becoming staff

Set `ADMIN_EMAIL` in `.env`. The first time that address is seen — either by
the sign-in lazy upsert or by the `user.created` webhook — the app grants the
full `admin` role, and the account is admin from then on. No seed script, no
manual SQL.

Everyone else is promoted from **`/admin/users`**: a per-row role select
(`—` / Support / Admin) that writes an `admin.role_changed` audit row with the
old and new values.

**The last full admin cannot be demoted.** Without that guard a platform can
be left with staff who can read everything and change nothing — including the
roles required to fix it. Promote a second admin first; the guard counts only
enabled accounts.

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

**Impersonate** on `/admin/users` opens an interstitial first: pick the
target org, state a **reason** (10–280 chars), then start. There is no
one-click path — the reason is stored on the session row and repeated in
both audit entries, so the trail reads standalone. Starting then creates a
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

## Content

`/admin/content` is one table over **every** registered content type — blog
posts, changelog releases, and anything else declared in
`internal/content/types.go` (see [Content](/docs/content)). There are no
per-type pages: the kind is a query parameter (`?kind=post`), so a newly
registered type appears here with no routing or template change. Above the
table sit a filter link and a New button per type; the list carries search and
pagination (20 per page). Columns are Title / Type / Language / Status /
Published / Actions.

The **Status** badge is computed, never stored: `draft`, `scheduled` (published
with a `published_at` still in the future), `expired` (`unpublish_at` has
passed), or `live`. Because it derives from the same predicate the public
pages use, it cannot drift from what a visitor sees — and an expired entry
stays listed and editable here while being gone from the site. The
**Language** column shows *All languages* for the shared `''` row and the bare
code for a variant.

The editor is one form per entry: title, a slug that auto-fills from the
title, language, summary, the type's own declared fields, `published_at` and
an optional `unpublish_at`, and a markdown body with a **live preview pane**
rendered by the same code that renders the published page. Publish, unpublish,
and delete are buttons; scheduling is just a date in either field. Every save
snapshots a revision, so the history list offers **Restore** on any earlier
version (a restore is itself a save, so it snapshots too). **Preview** is a
plain link that opens the real public view in a new tab, from the stored row,
whatever its status or dates — preview and publication cannot diverge because
they render through the same view lookup.

Every mutation writes an audit row (`content.created` / `updated` /
`published` / `unpublished` / `deleted` / `restored`) and invalidates the 30s
content cache, so a publish is visible on the next request.

Reads are GET and mutations are POST, which is all `requireAdminWrite` needs:
support staff get the full read-only table with the create, publish, and
delete controls hidden, and no per-route wiring made that true.

## Media

`/admin/media` — reached from the Content page header rather than its own nav
entry — uploads images for use in content bodies. The allowlist is PNG, JPEG,
GIF, and WebP, and the content type is **sniffed from the file's first bytes**,
never taken from the client's part header: media is the one thing served
inline instead of as an attachment, so the bytes are the only trustworthy
source. Anything else is a 422 with no row and no stored object. SVG is
excluded on purpose — it can carry script that would run same-origin.

Each row offers a **copy** button that yields the markdown to paste into a
body (`![alt](/media/{id}/{filename})`) and a delete. Uploads are
platform-scoped, not org-scoped, and go through the storage seam, so DevStore
covers local work with no account (see [File storage](/docs/storage)). Upload
and delete are audited like every other admin mutation.

## Schedules

`/admin/schedules` is the UI over the `schedules` table the worker's
scheduler pass claims each poll cycle. Create takes a name, a **schedulable
kind**, an interval (60s–30 days), an optional JSON payload, and a scope
(system-wide or one org). Only kinds in `jobs.SchedulableKinds` are offered:
handlers whose payloads are job-specific (`webhook.deliver` carries a
delivery id, the email kinds carry a recipient) can't be scheduled
generically — extend that list as you add recurring handlers.

Rows toggle enabled/disabled, delete, and offer **Run now**, which sets
`next_run_at = now()` so the schedule fires on the next worker pass (~2s).
Run-now is guarded on `enabled` — a disabled schedule cannot be sneak-fired.
Missed ticks are skipped by design: `ClaimDueSchedules` advances
`next_run_at` to `now() + interval` in the claiming statement, so an outage
never produces a catch-up stampede. Every mutation writes an audit row
(`schedule.created` / `schedule.updated` / `schedule.run_now` /
`schedule.deleted`).
