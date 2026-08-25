---
title: Outbound webhooks
description: Customer-facing webhooks — endpoints, standard-webhooks signatures, retries, replay.
section: Features
weight: 20
---

Your customers can receive GoGoGadget events at their own HTTPS endpoints.
Everything rides the existing Postgres job engine — retries, backoff, and
dead-letters are the same machinery inbound emails use — and signatures follow
the [Standard Webhooks](https://www.standardwebhooks.com) format, verified
with the same library that checks inbound Polar deliveries.

## Endpoints and events

Manage endpoints at **Settings → Webhooks** (`/app/settings/webhooks`):
create with an `https://` URL, an optional description, and zero or more
event-type checkboxes (none selected = **all events**). Each endpoint gets a
`whsec_…` signing secret — **shown exactly once** at creation (clipboard
button included); store it in your customer's secret manager.

Event catalog (`webhooks.EventTypes` — extend it to add events):

| Event | Payload `data` |
|---|---|
| `project.created` | `id`, `name`, `status`, `org_id` |
| `project.updated` | `id`, `name`, `status`, `org_id` |
| `project.archived` | `id`, `name`, `status`, `org_id` |
| `project.deleted` | `id`, `name`, `status`, `org_id` |

Emitting your own event is one line, fire-and-forget (the emit sites for
projects in `internal/web/workflow_projects.go` are the canonical example):

```go
webhooks.Emit(ctx, q, orgID, "project.created", map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "org_id": orgID})
```

The envelope is `{"type", "occurred_at" (RFC3339 UTC), "data"}` — marshaled
once, so every endpoint receives byte-identical JSON.

## Verifying signatures (customer side)

Deliveries carry the standard-webhooks header family — the same one Polar
uses to call *us*:

```
webhook-id: msg_…
webhook-timestamp: 1755370000
webhook-signature: v1,base64…
```

Go verification with the library:

```go
wh, _ := standardwebhooks.NewWebhookRaw([]byte(secret)) // your whsec_…
err := wh.Verify(payload, r.Header) // r.Header is http.Header — canonical keys
```

## Delivery semantics

- Each emit creates one `webhook_deliveries` row per matching active
  endpoint plus one `webhook.deliver` job.
- **2xx → success** (status + `delivered_at` recorded).
- Anything else (or network error) → the job's **2^n-minute backoff** applies
  (2, 4, 8, …); every attempt updates `attempts`, `last_response_status`,
  `last_error`.
- Exhausted (8 attempts) → `dead` + the endpoint's creator gets an in-app
  **webhook.failed** notification.
- **Replay** from the settings page resets a delivery to `pending` with zero
  attempts and re-enqueues it.
- Disabling an endpoint stops future deliveries (pending ones die on claim).

## SSRF policy

Outbound POSTs are a server-side request forgery primitive without a guard,
so delivery enforces it in two places:

1. **Before the request**: `https://` scheme only, the host must resolve, and
   every resolved address must be public — loopback, RFC-1918, link-local
   (cloud metadata!), unspecified, and multicast are rejected.
2. **At the dial**: the transport re-resolves and dials only approved IPs, so
   a DNS rebind between the guard and the dial cannot smuggle a private
   address through.

Local testing against `localhost`/LAN receivers is therefore impossible by
design — use a tunnel (ngrok, cloudflared) or run the test suite's
guard-relaxed worker.

## Rotating a signing secret

Endpoints show **Rotate secret** in the settings table. Rotation mints a new
secret, shows it exactly once (like creation), and keeps the previous one
verifying for a 24-hour grace window (`jobs.WebhookRotationGrace`).

During the window every delivery carries **both** signatures in the
`webhook-signature` header — the standard-webhooks format is a
space-delimited list, so a receiver holding either the old or the new secret
validates:

```
webhook-signature: v1,<sig-with-new-secret> v1,<sig-with-old-secret>
```

That is what makes rotation safe to do in production: deploy the new secret
to your receiver at any point inside the window, with no dropped deliveries
on either side of the switch. When the window closes, deliveries sign with
the new secret only, and the worker's janitor clears the stored previous
secret (`ClearExpiredPreviousSecrets`). Rotation writes a
`webhook_endpoint.secret_rotated` audit row.

Leaked secret? Rotate, update the receiver, and — if you cannot wait out the
window — rotate a second time: the first rotation's secret becomes the
previous one, and the leaked secret stops signing immediately.
