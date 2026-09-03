---
title: Billing
description: Plans, Polar checkout and webhooks, entitlements, and dunning.
section: Features
weight: 9
---

Billing is [Polar](https://polar.sh) as **merchant of record** (sales tax and
VAT are Polar's problem, not yours), a single Go file as the plan truth, and
a webhook-driven subscription mirror. The freemium model has **no route-level
paywall**: enforcement is per-action limits plus persistent banners.

`PolarClient` (`internal/billing/polar.go`) talks to the Polar REST API
(version `2026-04`) with raw `net/http` behind the `billing.Client` seam —
the former `polarsource/polar-go` SDK is archived upstream. Besides checkout,
portal, and revoke, the seam exposes `IngestUsage`, which pushes metered
usage events to Polar (`POST /v1/events/ingest`) — see
[Background jobs](/docs/background-jobs) for the flush schedule.

## plans.go is the single source of truth

`internal/billing/plans.go` defines every plan exactly once — the pricing
page, the upgrade CTAs, the usage meter, the limit checks, and MRR all read
from it:

| Key | Price | Max projects | Max members | Features |
|---|---|---|---|---|
| `free` | $0 | 3 | 1 | 3 projects · 1 team member · Community support |
| `pro` | $20/mo | unlimited (−1) | 10 | Unlimited projects · 10 team members · Priority support |
| `team` | $50/mo | unlimited (−1) | unlimited (−1) | Unlimited everything · Unlimited members · SSO via Clerk |

`defaultPlans` is an **ordered slice** — a Go map would shuffle the pricing
cards per run. `PlanCatalog.ByKey` falls back to `free` for unknown keys, so a
stale `product_key` in the database can never widen access. `MaxMembers` is
informational only (invitations are hosted by the identity provider — see
[Organizations](/docs/organizations)). Provider product ids are bound by the
selected **adapter**, not by the seam: `internal/billing/polar/module.go`
copies `POLAR_PRODUCT_PRO` / `POLAR_PRODUCT_TEAM` onto the `pro` and `team`
plans and builds its catalog with `billing.NewPlanCatalog`, while
`billinglocal.LocalPlanCatalog` uses each plan's own key. The free plan has no
product id in either and can never be checked out.

## Polar sandbox setup

1. Create a Polar account and select the **sandbox** environment at
   [sandbox.polar.sh](https://sandbox.polar.sh). Sandbox and production data,
   access tokens, and webhook secrets are separate.
2. Create two monthly recurring products: Pro at $20 and Team at $50. Copy
   their IDs into `POLAR_PRODUCT_PRO` / `POLAR_PRODUCT_TEAM`.
3. In Organization Settings → Developers, create an Organization Access Token
   with `checkouts:write`, `customer_sessions:write`, and
   `subscriptions:write`; set it as `POLAR_ACCESS_TOKEN`.
   `POLAR_SERVER` defaults to `sandbox`.
4. For local webhook delivery, run `polar login`, then:

   ```sh
   polar listen http://localhost:8080/webhooks/polar
   ```

   Select the sandbox organization and copy the listener's generated
   **Secret** into `POLAR_WEBHOOK_SECRET`. A Dashboard webhook endpoint uses a
   different secret and is not needed while the local listener is running.
5. For a deployed app, create a Polar Dashboard webhook endpoint at
   `https://your-domain.com/webhooks/polar`, subscribe to all
   `subscription.*` events, and use that endpoint's secret instead.

With no `POLAR_ACCESS_TOKEN`, billing routes render a 503 "not configured"
fragment and everything else keeps working.

## Checkout, portal, and the success race

`POST /app/billing/checkout {plan}` creates a Polar checkout with
`SuccessURL={APP_URL}/app/settings/billing?success=1`,
`CustomerExternalID=<clerk_org_id>`, and metadata `clerk_org_id` — both are
how the webhook later resolves the org. An unknown or product-less plan gets
a 422 fragment. `POST /app/billing/portal` creates a customer-portal session
the same way. Both redirect out to another origin, so both use the hard form
(`303`, or `HX-Redirect` for HTMX) rather than a soft in-app navigation.

**The webhook races the redirect.** The customer returns to
`/app/settings/billing?success=1` possibly *before* Polar's webhook lands, so
the page must never assert on the immediate state. When `success=1` and the
subscription row is missing or still `incomplete`, the plan card renders in a
"Processing your subscription…" state with:

```html
hx-get="/app/settings/billing/fragment" hx-trigger="every 2s" hx-swap="outerMorph"
```

The fragment endpoint re-renders the card every 2 seconds; once the webhook
has written a real status the card comes back **without** the `hx-trigger`,
and polling stops on its own.

## Webhook events → actions

`POST /webhooks/polar` verifies the `webhook-*` headers (Standard Webhooks —
note Clerk uses `svix-*`, a different family; see
[Security](/docs/security)), records the `webhook-id` in `webhook_events` for
replay idempotency, then runs the state machine in
`internal/billing/webhook.go`. Common mechanics: the org resolves from
checkout metadata `clerk_org_id` first, then the Polar customer's
`external_id`; the **previous status is read before the upsert** because
transitions drive emails; the upsert conflicts on `clerk_org_id` (one
subscription per org — a resubscribe arrives with a *new* Polar subscription
ID and must overwrite); unknown product IDs are ACKed with a warning so a
stale product never wedges the endpoint; signature failures get 400; DB
errors get 500 so Polar retries.

| Event | Row | Side effects |
|---|---|---|
| `subscription.created` | Upsert, payload status **verbatim** | If `trialing` → enqueue the trial-ending email for trial_end − 3 days |
| `subscription.updated` | Upsert | Transition **into** `past_due` → `payment_failed` email; audit `subscription.updated` only when the status actually changed |
| `subscription.active` | Upsert, `cancel_at_period_end=false` | Audit `subscription.activated`; capture analytics **here** (`created` may still be `incomplete`) |
| `subscription.canceled` | Upsert, `cancel_at_period_end=true` | Cancellation email only if previous status ≠ `canceled` (one-shot across replays); audit `subscription.canceled`; capture |
| `subscription.uncanceled` | Upsert, `cancel_at_period_end=false` | Audit `subscription.reactivated` — without this branch a customer who withdraws cancellation loses access at period end while paying |
| `subscription.revoked` | Upsert, payload status verbatim (`canceled`/`unpaid`) | `revoked` is an **event, never a stored status**; same one-shot email guard; audit `subscription.revoked`; capture |
| anything else | — | 200 + log (forward-compatible) |

Emails go to the first `org:admin` member, else the first member. Delivery
details per email kind are in [Email](/docs/email).

## Entitlements

`Entitled(sub, now)` in `internal/billing/entitlements.go` is THE gate:

```go
switch sub.Status {
case "active", "trialing", "past_due":
	return true // past_due = grace + banner
case "canceled":
	return sub.CurrentPeriodEnd.Valid && sub.CurrentPeriodEnd.Time.After(now)
default:
	return false // unpaid, incomplete, incomplete_expired
}
```

`CurrentPlan` wraps it: no subscription row → `free`; **database error →
`free` (a hiccup must never widen entitlements)**; entitled → the row's
`product_key` plan; otherwise `free`. The `Entitled` check *inside*
`CurrentPlan` is the fix for the canonical billing bug: an expired or revoked
subscription silently keeping its paid limits. A canceled sub confers its
plan until `current_period_end`, then reverts to free automatically.

## Enforcement points

- **Project create (HTML)** — `CountProjectsByOrg >= plan.MaxProjects` (when
  not −1) → **422** form fragment with the limit and an upgrade CTA.
- **Project create (API)** — same check → **402** with code `plan_limit` (see
  [API](/docs/api)).
- **Banners** — `AppLayout` shows "payment failing — update card" while
  `past_due`, and "plan ends {date}" while canceled-but-entitled.
- **Billing page** — current-plan card with status badge and period end,
  usage meter (`3 / 3` on free), upgrade buttons for higher tiers, and the
  portal button.

## Dunning and trials

`past_due` keeps full access (grace) while the banner and one
`payment_failed` email push the customer to update their card in the Polar
portal; `subscription.active` on recovery clears the state. Trials get one
`trial_ending` email scheduled at trial_end − 3 days; a run-time guard skips
the send if the subscription is no longer `trialing` by then — see
[Background jobs](/docs/background-jobs).

## Usage-based billing

Metered usage rides the same seam: `usage.Record` writes local
`usage_events` rows (fire-and-forget), and the seeded `usage-flush` schedule
(every 300s) drives the `usage.flush` job, which claims batches, groups by
org, and calls `billing.Client.IngestUsage` → Polar's `POST
/v1/events/ingest`. The flush is **idempotent**: Polar dedups on
`external_id`, and every event is sent as `ue-<usage_events.id>`. A failed
ingest returns the batch to the pool; at-least-once retries are safe. No
`POLAR_ACCESS_TOKEN` → the job no-ops and events stay local (they flush when
Polar is configured later).

**Polar dashboard setup** (sandbox or production):

1. Meters → create meter `ai_tokens` with aggregation **sum of metadata
   `_llm.total_tokens`** (or **count of events** for per-call billing).
2. Edit the Pro/Team products → add a **metered price** on that meter.
3. Nothing in Go: `IngestUsage` carries the customer via
   `external_customer_id = clerk_org_id`, and the `_llm` metadata key is
   Polar's structured usage convention (`_llm.model`,
   `_llm.prompt_tokens`, `_llm.completion_tokens`, …).

In-app caps are plan truth (`Plan.Meters`, monthly) and render as meters on
the billing page; the AI endpoint enforces them with 402 `plan_limit` — see
[AI](/docs/ai).

## Testing without Polar

`billing.MockClient` implements the same `billing.Client` interface
(`CreateCheckout`, `CreatePortalSession`, `RevokeSubscription`,
`IngestUsage`) with canned URLs and recorded calls — no HTTP mocking of the
provider. Webhook tests use
signed fixtures via `signStandard`, which emits the real `webhook-*` header
family: replay the same `webhook-id` → 200 with no duplicate row and no
second email; transition coverage includes resubscribe-after-cancel,
`uncanceled`, and `revoked` payload-status mapping. See
[Testing](/docs/testing).

## Dunning

A failed charge starts a sequence, not a single email. Polar retries the card
on its own schedule; this is the human half — the messages that get someone to
replace an expired card before the subscription dies.

| When | What |
|---|---|
| Failure (transition into `past_due`) | In-app notification to the org, email to the owner, and a persistent banner in the app shell while the status lasts |
| +3 days (`billing.DunningReminderAfter`) | Reminder email: still retrying, here is the link |
| +7 days (`billing.DunningFinalAfter`) | Final notice email **plus** a fresh in-app notification — the day-0 one is a week stale by then |

Both follow-ups are enqueued **at the moment of failure** with a future
`run_at`, and both re-read the subscription before sending
(`Worker.paymentRecovered`). A customer who fixes their card on day 4 never
receives the day-7 warning: an apology-generating email that arrives after the
problem is solved is worse than no email at all. A subscription that has since
vanished skips too, rather than failing the job.

Only the **transition into** `past_due` schedules a sequence, so repeated
`subscription.updated` deliveries with the same status cannot stack five sets
of warnings. A genuine second failure after a recovery does earn a fresh
sequence, because that is a new transition.

Change the cadence by changing the two constants in `internal/billing` —
nothing else knows the numbers. Two follow-ups is the default because a third
rarely converts and starts to read as harassment.

The in-app notification is deliberately **outside** the email gate: it needs
no address, so an organization whose owner-email lookup fails still learns its
card is failing instead of sliding silently toward cancellation.
