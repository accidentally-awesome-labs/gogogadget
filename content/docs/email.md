---
title: Email
description: The Sender seam, templ email pairs, and rendered-at-enqueue payloads.
section: Features
weight: 9
---

Transactional email rides the [background job queue](/docs/background-jobs):
nothing sends synchronously inside a request. Three design decisions define
the system — a one-method `Sender` seam, templ HTML+text pairs, and bodies
rendered **at enqueue time**.

## The Sender seam

`internal/mail/mail.go` is the only file that imports an email SDK:

```go
type Message struct {
	To, Subject, HTML, Text string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}
```

Two implementations ship:

- **`ResendSender`** — production path via the Resend API, wired when
  `RESEND_API_KEY` is set, sending from `EMAIL_FROM`. (resend-go has no
  context support, so `ctx` is accepted for the interface and not forwarded.)
- **`DevSender`** — the zero-infra default when no key is set. It logs the
  recipient and subject **and writes the rendered HTML to
  `tmp/emails/<timestamp>-<to>.html`**, so you can open the exact email in a
  browser during development. The welcome email from a fresh clone lands
  there — no account required.

Swapping providers means adding one file that satisfies `Sender` and wiring
it in `cmd/server/main.go`. Handlers and the job worker never see either
implementation.

## templ HTML + text pairs

Every email is **two templ components** — an HTML body and a plain-text body
— living in `internal/web/templates/emails.templ` on the shared inline-styled
`EmailLayout`. The four shipped kinds:

| Kind | Subject | Trigger |
|---|---|---|
| `email.welcome` | "Welcome to GoGoGadget" | Clerk `user.created` webhook |
| `email.payment_failed` | "Your payment failed" | Transition **into** `past_due` |
| `email.subscription_canceled` | "Your subscription is canceled" | First `subscription.canceled`/`revoked` (one-shot guard) |
| `email.trial_ending` | "Your trial ends soon" | Scheduled at trial_end − 3 days |

Builder functions in `mail.go` (`WelcomeMessage`, `PaymentFailedMessage`,
`SubscriptionCanceledMessage`, `TrialEndingMessage`) render both components
to strings and return a `Message`.

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
DevSender and open the file under `tmp/emails/`.
