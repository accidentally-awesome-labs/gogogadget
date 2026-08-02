---
title: Extending GoGoGadget
description: The recipe hub — add resources, plans, emails, jobs, webhooks, endpoints, or swap a provider.
section: Guides
weight: 19
---

Every common change follows the same shape: edit the **source of truth**
(schema, plan list, interface), regenerate, wire, test. Recipes are ordered
file-path steps. The rule behind all of them: handlers never import an SDK —
they talk to the seams (`identity.Verifier`, `billing.Client`, `mail.Sender`,
`analytics.Capturer`).

## Add a CRUD resource

The canonical example is `projects`; copy its shape exactly.

1. **Migration** — `internal/db/migrations/0002_widgets.sql` with `-- +goose
   Up` / `-- +goose Down`. Org-scope it: `clerk_org_id TEXT NOT NULL
   REFERENCES orgs(clerk_org_id) ON DELETE CASCADE`, plus `created_at` /
   `updated_at` defaults. See [Database](/docs/database).
2. **Queries** — `internal/db/queries/widgets.sql` (one query file per
   table). Every UPDATE sets `updated_at = now()`; every WHERE clause carries
   `clerk_org_id = $1` so cross-org ids are 404s, never leaks.
3. **Regenerate** — `make generate`. sqlc emits typed methods into
   `internal/db/sqlc/` (never edit generated files).
4. **Templates** — `internal/web/templates/widgets.templ`: List/New/Edit
   pages plus a row fragment. Add `data-testid` hooks to every element a test
   will assert on. Follow the HTMX rules in [Frontend](/docs/frontend).
5. **Handlers** — `internal/web/handlers_widgets.go`. Validation failure →
   422 + re-rendered form fragment; success → `HXRedirect` or list fragment +
   `Toast`; row delete → 200 empty with `hx-target="closest tr"`.
6. **Audit** — `audit.Log(ctx, s.q, orgID, userID, "widget.created", …)` on
   every mutation; it appears on `/app/activity` for free.
7. **Routes** — register on `appMux` in `internal/web/routes.go` (the
   `RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan` chain already
   applies).
8. **Nav** — add the sidebar entry in `internal/web/templates/sidebar.templ`.
9. **Tests** — `internal/web/widgets_test.go`: cross-org access → 404;
   plan-limit branch → 422 if the resource is plan-limited. Handler behavior
   belongs at the integration layer; see [Testing](/docs/testing).
10. **Optional API surface** — the next recipe but one.

## Add a plan

1. **Plan truth** — append to the `Plans` slice in
   `internal/billing/plans.go`. Order is render order; keep `free` first
   (`PlanByKey` falls back to index 0).
2. **Polar product** — create the product in the Polar dashboard; copy its id.
3. **Env** — add `POLAR_PRODUCT_BUSINESS=…` to `.env.example`, your `.env`,
   and `internal/config/config.go` (a `PolarProductBusiness` field read in
   `Load`).
4. **Wiring** — extend `billing.SetPolarProductIDs` (called from
   `cmd/server/main.go`) with the new case.

The pricing page, upgrade CTAs, and usage meters render from `billing.Plans`
automatically — no template edits. Enforcement (`MaxProjects`) applies the
moment the plan exists. See [Billing](/docs/billing).

## Add annual pricing

1. Add an `Interval` field (`"month"` / `"year"`) to `billing.Plan` and set it
   on each entry.
2. Create a second Polar product per paid plan; add env keys
   (`POLAR_PRODUCT_PRO_YEAR`, …) and extend `SetPolarProductIDs`.
3. Decide the webhook mapping: the processor reverse-maps product id → plan
   key — teach it that the annual ids map to the same keys, and persist the
   interval on the subscription row (new column + migration).
4. Fix the math: `MRR` in `internal/billing/plans.go` divides annual
   subscriptions by 12.
5. Pricing page: group cards by plan, toggle by interval.

## Add an email kind

1. **Templates** — add the HTML and plain-text components to
   `internal/web/templates/emails.templ` (they share `EmailLayout`).
2. **Builder** — add a `XMessage(appURL, to, …) (mail.Message, error)`
   constructor in `internal/mail/mail.go`, next to `WelcomeMessage`. Bodies
   render to strings at enqueue time — workers never touch templates.
3. **Job kind** — add `KindX = "email.x"` to the consts in
   `internal/jobs/jobs.go` and add it to the email case in `dispatch` (all
   email kinds share one code path).
4. **Enqueue** at the trigger site with `jobs.EnqueueEmail(ctx, q,
   jobs.KindX, msg, orgID, runAt)`. Billing-triggered? Extend the
   `billing.EmailSink` interface in `internal/billing/webhook.go` and its
   implementation in `internal/web/email_sink.go` — billing must not import
   mail/jobs directly (import cycle).
5. **Verify locally** — DevSender writes `tmp/emails/*.html`; no Resend
   account needed. See [Email](/docs/email).

## Add a job kind

1. `internal/jobs/jobs.go`: add the `Kind…` const and a typed payload struct.
2. Add a `case` to `dispatch` that unmarshals the payload and does the work.
3. Enqueue with `jobs.Enqueue` / `EnqueueAt` from the call site.
4. Test in `internal/jobs/jobs_test.go`: claim → complete, and poison →
   attempts increment. Backoff (2^attempts minutes), the 5-minute visibility
   timeout, and dead-lettering at `max_attempts` come free. See
   [Background jobs](/docs/background-jobs).

## Add a webhook event

Unknown events are already ACKed (200 + log), so this is purely additive.

- **Clerk** — add a parser for the payload shape in
  `internal/identity/sync.go`, then a `case` in `processClerkEvent`
  (`internal/web/handlers_webhooks.go`). Test with the `signSvix` fixture
  (`internal/web/testhelpers_test.go`), which emits real `svix-*` headers.
- **Polar** — add a `case` to `Processor.ProcessSubscription`
  (`internal/billing/webhook.go`). Test with the `signStandard` fixture
  (`internal/web/billing_test.go`), which emits real `webhook-*` headers.

Idempotency (`webhook_events`) and the 400/500 retry semantics apply
automatically. Signature verification is not optional; the two header
families are explained in [Security](/docs/security).

## Add an OAuth provider

Clerk dashboard → SSO connections → enable Google/GitHub/…. **Zero code**:
the hosted Account Portal renders the buttons, and the mirror sync doesn't
care how a user authenticated. 2FA is the same story — enable it in Clerk.
See [Authentication](/docs/authentication).

## Swap the billing provider

1. New file `internal/billing/<provider>.go` implementing `billing.Client`
   (`CreateCheckout`, `CreatePortalSession`, `RevokeSubscription`) —
   `internal/billing/polar.go` is the template. This is the only SDK-touching
   file.
2. Replace verification + payload parsing in the webhook handler
   (`internal/web/handlers_billing.go`) and the `SubscriptionPayload` mapping
   in `internal/billing/webhook.go`. Keep the event→action table semantics —
   that state machine is the product, not the provider.
3. Rewire construction in `cmd/server/main.go`.
4. Handler tests survive untouched: they run against `billing.MockClient`.

`plans.go`, entitlements, and the pricing page are provider-agnostic and stay.

## Swap the auth provider

1. Implement `identity.Verifier` — `Verify(ctx, token) (*Claims, error)` — in
   one file, next to `internal/identity/verifier.go`.
2. Implement `identity.UserFetcher` (`internal/identity/fetcher.go`) for the
   lazy mirror upsert.
3. Wire both in `cmd/server/main.go`.
4. Replace the mirror-sync webhook: Clerk-shaped parsing in
   `internal/identity/sync.go` and `handlers_webhooks.go` become your
   provider's delivery format. Keep the mirror schema (`users`, `orgs`,
   `org_members`) — everything downstream reads it.
5. Point the `/login`, `/signup`, `/logout` redirects in
   `internal/web/handlers_auth.go` at the new hosted UI.

Every guard (`RequireAuth`, `RequireOrg`, …) and the e2e `FakeVerifier`
continue to work unchanged — that is what the seam is for.

## B2C mode (no organizations)

1. `internal/web/auth.go`: drop `requireOrg` from `appChain` (and the
   SelectOrg branch); make `loadPlan` resolve by user instead of org.
2. Migration: rekey `subscriptions`, `projects`, `api_tokens`, and
   `audit_log` from `clerk_org_id` to `clerk_user_id`; update the queries to
   match (one file per table, then `make generate`).
3. Delete the org machinery: `organizationMembership.*` webhook cases,
   `memberships.sql`, the SelectOrg page, Settings → Org, and the
   `OrganizationSwitcher` in the sidebar.
4. Checkout metadata carries the user id as the external id.

The Clerk webhook then only needs `user.*` events.

## Add an API endpoint

1. `internal/api/<resource>.go` — write the handler against the **same sqlc
   queries** the HTML handlers use. The API is a second transport, never
   parallel logic.
2. `internal/web/routes.go` — register with
   `apiMW.RequireAPIToken("read", …)` or `("write", …)`.
3. Errors go through `api.WriteError` (`{"error":{"code","message"}}`); the
   plan limit is 402 with code `plan_limit`.
4. Mutations audit with `metadata {"via":"api"}`.
5. Versioning: `/api/v1` is **additive-only**; a breaking change means a new
   `/api/v2` mount. See [API](/docs/api).
6. Test at the integration layer (`internal/web/api_test.go` shows token,
   scope, 402, and 404 coverage).

## Add an admin page

1. Handler in `internal/web/handlers_admin.go`.
2. Route on `adminMux` in `internal/web/routes.go` — `adminChain` already
   adds `RequireAdmin`.
3. Template in `internal/web/templates/admin.templ` plus a nav entry there.
4. Test the negative case: non-admin → 403 (`internal/web/admin_test.go`).

## Add a docs page

1. Create `content/docs/<slug>.md` with frontmatter `title`, `description`,
   `section`, `weight` (int) — plus `draft: true` to keep it out of
   production.
2. Nothing else: the sidebar groups by `section` and orders by `weight`,
   `/docs` redirects to the lowest-weight page, and the page joins
   `sitemap.xml` automatically.
3. Restart the server (content parses at boot; air does this on save).
4. Cross-link freely, but only to real slugs — the content package has a
   link-check test that fails the build on dead `/docs/...` links. See
   [Content](/docs/content).
