---
title: Notifications
description: In-app notifications — notify.Send, the unread badge, and the SSE stream.
section: Features
weight: 16
---

In-app notifications are per-user rows in the `notifications` table, a bell +
unread badge in the app sidebar, and a Server-Sent Events stream that pushes
count changes. No external service — the whole feature is Postgres + one
Go endpoint.

## Sending

`internal/notify` is the fire-and-forget seam (`audit.Log` style — errors are
logged, never returned):

```go
notify.Send(ctx, q, orgID, userID, kind, title, body, url)  // one user
notify.SendOrg(ctx, q, orgID, kind, title, body, url)       // every member
```

Rows are **per-user only**; `SendOrg` fans out at send time. `kind` is free
text (`welcome`, `payment_failed`, `export.ready`, …); `url` renders as an
Open link when non-empty. Titles/bodies are stored verbatim — server-side
strings stay English this round (the i18n boundary covers templates, not
stored rows).

Wired producers today: `organizationMembership.created` sends the welcome
notification; a `subscription.updated` transition into `past_due` notifies
every org member (payment_failed, linking `/app/settings/billing`); the CSV
export job notifies `export.ready` (see [File storage](/docs/storage)).

## The badge and the stream

The sidebar renders a self-swapping span that loads its count from
`GET /app/notifications/badge` and re-fetches on `sse:notifications` events:

```html
<div sse-connect="/app/notifications/stream">
  <a href="/app/notifications" ...>
    <span id="notif-badge" hx-get="/app/notifications/badge"
          hx-trigger="load, sse:notifications" hx-swap="outerHTML"></span>
  </a>
</div>
```

The `sse-connect` div lives **in the shell** deliberately: boosted navigation
swaps only `#content`, so the EventSource survives page changes. The SSE
endpoint (`GET /app/notifications/stream`):

- disables the server's 30s WriteTimeout per-response via
  `http.NewResponseController(w).SetWriteDeadline(time.Time{})` — the
  middleware's `statusWriter` gained `Unwrap()` so the controller reaches the
  real writer,
- polls the unread count every **5s** and emits
  `event: notifications / data: {"unread":N}` **only on change**,
- sends a `: ping` comment every **15s** (keeps proxies from idling the
  stream), with `X-Accel-Buffering: no` for nginx-style buffers,
- exits on `r.Context().Done()`.

The documented upgrade path — not built — is Postgres LISTEN/NOTIFY in place
of the 5s poll. If the vendored htmx-ext-sse ever misbehaves against a future
htmx version, drop `sse-connect` and give the badge
`hx-trigger="load, every 30s"`; the stream endpoint stays for builders.

## Pages and marks

`GET /app/notifications` paginates (unread rows bolded);
`POST /app/notifications/{id}/read` and `POST /app/notifications/read-all`
are **user-scoped** — user A cannot mark B's rows, ever. A 90-day janitor
query (`DeleteOldReadNotifications`) is available for the worker's janitor
pass.

## Preferences

`GET/POST /app/settings/notifications` lets each user mute individual kinds.
The catalog is `notify.Kinds` — `welcome`, `payment_failed`, `export.ready`,
`webhook.failed` — every kind the product emits. Absent preference rows mean
**default-on**; an explicit `in_app = false` row mutes that one kind for that
one user (a `notification_preferences` row, per-user per-kind). `SendOrg`
fan-out honors per-member preferences, so a broadcast reaches everyone except
the people who muted it. A preference-lookup hiccup logs and sends — the
failure mode is an extra notification, never a silently dropped one.

## Digest emails

Preferences on `/app/settings/notifications` govern the **in-app** channel;
the same page carries the **email digest** cadence
(`users.digest_frequency`: `off` / `daily` / `weekly`, default `weekly`).

The two compose in one direction on purpose: the digest is built from the
`notifications` table, so a kind muted in-app never produced a row and can
never appear in an email. Muting a kind mutes it everywhere; the cadence only
controls how often the surviving rows are mailed. See
[Email → The digest](/docs/email).
