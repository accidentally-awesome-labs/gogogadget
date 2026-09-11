---
title: Email
description: The Sender seam, templ email pairs, and rendered-at-enqueue payloads.
section: Features
weight: 10
---

Transactional email rides the [background job queue](/docs/background-jobs):
nothing sends synchronously inside a request. Three design decisions define
the system — a one-method `Sender` seam, templ HTML+text pairs, and bodies
rendered **at enqueue time**.

## The Sender seam

`internal/mail/mail.go` defines the seam — the only file a provider author
needs to read:

```go
type Message struct {
	To, Subject, HTML, Text string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}
```

No implementation is hand-wired anywhere. Three adapters ship, each a module
that provides the `mail.sender` capability on the `ggg/mail` provider slot,
and the generated per-environment boot (`bootstrap_registry_gen.go`)
constructs exactly the one your project selected for that `APP_ENV`:

| Adapter | Targets | Selected for |
|---|---|---|
| `ggg/system/mail-dev` | `filesystem` | development, test — logs the recipient and subject **and writes the rendered HTML to `tmp/emails/<timestamp>-<to>.html`**, so you can open the exact email in a browser. The welcome email from a fresh clone lands there — no account required. |
| `ggg/system/mail-smtp` | `mailpit`, `smtp` | self-hosted production — Mailpit appears in the dev Compose file only when selected |
| `ggg/system/mail-resend` | `resend` | managed production, sending from `EMAIL_FROM` via the Resend API |

Swapping providers is a selection, not a source edit:

```sh
ggg provider set --provider ggg/mail:production=ggg/system/mail-resend@resend
```

Handlers and the job worker hold the seam's capability and never see an
implementation. **Writing a new adapter** (your own mail provider, or a
third-party one) means authoring a module that satisfies `Sender` in its own
package — never editing `cmd/server` or any generated file; see
[extending → Swap a provider](/docs/extending#swap-a-provider) for the full
authoring path, and [the module reference](/docs/module-reference) for the
slot's capability and contract numbers.

## templ HTML + text pairs

Every email is **two templ components** — an HTML body and a plain-text body
— living in `internal/web/templates/emails.templ` on the shared inline-styled
`EmailLayout`. The seven shipped kinds:

| Kind | Subject | Trigger |
|---|---|---|
| `email.welcome` | "Welcome to GoGoGadget" | Clerk `user.created` webhook |
| `email.payment_failed` | "Your payment failed" | Transition **into** `past_due` |
| `email.subscription_canceled` | "Your subscription is canceled" | First `subscription.canceled`/`revoked` (one-shot guard) |
| `email.trial_ending` | "Your trial ends soon" | Scheduled at trial_end − 3 days |
| `email.digest` | "Your GoGoGadget digest" | Hourly schedule; sends to users whose cadence is due |
| `email.dunning_reminder` | "Your payment is still failing" | 3 days after entering `past_due`, if still failing |
| `email.dunning_final` | "Final notice: your plan is about to be canceled" | 7 days after entering `past_due`, if still failing |

Builder functions in `mail.go` (`WelcomeMessage`, `PaymentFailedMessage`,
`SubscriptionCanceledMessage`, `TrialEndingMessage`, `DigestMessage`,
`DunningReminderMessage`, `DunningFinalMessage`) render
both components to strings and return a `Message`.

## The digest

Everything else is transactional — one event, one email, rendered when the
job is enqueued. The digest is the exception on both counts, and the
differences are the design:

- **Cadence is per user, not per schedule.** `users.digest_frequency` is
  `off` / `daily` / `weekly` (default `weekly`, changeable at
  **Settings → Notifications**). The `email.digest` schedule only decides how
  often the worker *looks*; the handler decides who is actually due. Run it
  hourly and daily users get theirs on time.
- **Rendered by the worker, not at enqueue time.** Its content is a query
  result that only exists at send time; rendering early would store a stale
  email in a job payload. `Worker.AppURL` exists for exactly this reason —
  the worker needs the base URL that enqueue-time builders normally supply.
- **Window = `users.last_digest_at`.** The stamp is both "when we last sent"
  and "where the next window starts", so nothing is repeated and nothing is
  skipped.
- **Send, then stamp.** Stamping first would drop a period's content on a
  delivery failure. The trade is that a crash between the two can repeat one
  digest — a duplicate summary being the smaller harm than a lost one. A
  failed send returns an error, so the job retries with backoff and the
  unstamped users are still due.
- **Quiet accounts are stamped anyway.** No notifications in the window means
  no email (an empty digest is spam) but the stamp still advances, so a dormant
  user is not rescanned on every pass.
- **In-app mutes apply first.** The digest reads the `notifications` table, so
  a kind the user muted never got a row and therefore never reaches the email.
- **Batched at 200 users, 25 items each** — a pass that would email the whole
  user table in one job is a job that times out and retries from zero.

Locale is per user: `users.locale` when they picked a language, otherwise the
deployment default (`Worker.DigestLocale`, English). An email has no
`Accept-Language` header to fall back on, which is exactly why that preference
is stored server-side — see [i18n](/docs/i18n).

## Rendered at enqueue time

The job payload contract (`jobs.EmailPayload`) carries finished bodies:

```json
{"to": "...", "subject": "...", "html": "...", "text": "...", "org_id": "..."}
```

`org_id` is optional and exists for run-time guards. Because rendering
happens when the job is **enqueued** — in the web layer, where templ is
already imported — the worker never touches templates, and a template change
never affects mail that is already queued. `jobs.EnqueueEmail` takes a built
`mail.Message` and stores it verbatim.

## The trial-ending run-time guard

`email.trial_ending` is enqueued when a `trialing` subscription is created,
with `run_at = trial_end − 3 days`. Days later, when the job is finally
claimed, the world may have changed: the customer converted, canceled, or the
row is gone. Sending "your trial ends soon" to a paying customer would be
wrong, so before sending, the worker **re-reads the subscription and skips
the send unless the status is still `trialing`**. A missing row also skips.
The skip is logged, not retried. The guard needs `org_id` in the payload —
that is why it exists.

## Adding an email kind

1. **Template pair** — add `<Name>EmailHTML` and `<Name>EmailText` to
   `internal/web/templates/emails.templ`.
2. **Builder** — add a `<Name>Message(...)` function in
   `internal/mail/mail.go` that renders both and returns `mail.Message`.
3. **Job kind** — add a `Kind<Name> = "email.<name>"` constant in
   `internal/jobs/jobs.go`.
4. **Dispatch** — add the constant to the email case list in
   `Worker.dispatch` (all email kinds share the single `sender.Send` path; if
   the kind needs a run-time guard like trial-ending, add it there).
5. **Enqueue** — call `jobs.EnqueueEmail` from the event source (a webhook
   processor or handler), passing any scheduling `runAt`.

`make generate` after editing the templ file, then exercise it with the
development selection (`ggg/system/mail-dev@filesystem`) and open the file
under `tmp/emails/`.
