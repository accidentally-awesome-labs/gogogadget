---
title: Observability
description: Structured logs, request ids, Sentry, PostHog behind a consent gate, and pprof.
section: Guides
weight: 24
---

Everything is **env-gated**: with no keys set you get structured logs and
nothing else — no crashes, no half-wired telemetry in the hot path.

## Logs: slog, JSON in production

`cmd/server/main.go` builds one `slog` logger: a **JSON handler in
production**, human-readable text in development. `LOG_LEVEL` overrides the
default (`debug` in development, `info` otherwise). Boot logs say exactly
what is wired:

```text
{"level":"INFO","msg":"starting","env":"production","version":"v1.4.0","port":8080}
{"level":"INFO","msg":"billing: polar","server":"production"}
{"level":"WARN","msg":"polar not configured — billing routes will 503"}
```

Unconfigured services log a warning and degrade; they never panic.

## Request ids and the access log

Every request gets a **16-byte hex id** (the middleware runs before logging),
returned to the client as `X-Request-Id` and attached to that request's log
lines. One line per request:

```text
{"level":"INFO","msg":"request","method":"POST","path":"/app/projects","status":422,"bytes":2410,"duration_ms":3,"request_id":"9f1c…"}
```

5xx logs at ERROR, everything else at INFO. Panics log the error, path, and
request id before the 500 page renders — a user reporting one header value
gives you their exact failure.

## Sentry (env-gated)

Set `SENTRY_DSN` and `sentry.Init` runs with `Environment = APP_ENV`. All
reporting goes through the `observability.Reporter` seam
(`internal/observability` — the only internal package importing sentry-go;
`NoopReporter` when unconfigured). Two capture points:

- **Panics.** The `recover` middleware captures the exception with the
  request path as a tag and the request id as context, then renders the 500
  page. Outside production the page itself carries the panic and stack.
- **Job dead-letters.** When a background job exhausts its attempts
  (`last_error='exhausted'`), the worker's `OnDeadLetter` hook reports it —
  silent email loss becomes a Sentry issue instead. See
  [Background jobs](/docs/background-jobs).

Queued events flush for 2s on shutdown. Without the DSN both paths no-op.

## PostHog (env-gated)

Set `POSTHOG_API_KEY` (and optionally `POSTHOG_HOST`, default
`https://us.i.posthog.com`) to enable product analytics.

### Server-side events

Four events fire from the backend through the `analytics.Capturer` seam:

| Event | Fired when |
|---|---|
| `user_signed_up` | The Clerk `user.created` webhook syncs the mirror row |
| `project_created` | A project is created (HTML form or API) |
| `subscription_activated` | The Polar `subscription.active` event lands |
| `subscription_canceled` | `subscription.canceled` / `subscription.revoked` lands |

The interface is one method: `Capture(userID, event string, props
map[string]any)`. Without a key, `NoopCapturer` discards events, so **call
sites are unconditional** — never wrap captures in `if enabled`. Queueing is
fire-and-forget: a PostHog outage cannot fail a user request.

### The `/ingest` reverse proxy

Browser-side PostHog loads through the app, not a third-party origin:
`/ingest/*` reverse-proxies to `POSTHOG_HOST` (prefix stripped, `Host`
rewritten). That keeps CSP at `script-src 'self'` (see
[Security](/docs/security)) and makes ad-blockers irrelevant. The route is
registered only when the key is set, and it is exempt from CSRF and the rate
limiter.

### The consent gate

`static/analytics.js` calls `posthog.init` **only after** the user accepts:
localStorage `ph_consent=yes`. A dismissible banner — rendered only when the
key exists — writes `yes` (and initializes immediately) or `no`. No consent,
no analytics: GDPR-sane by default, and the choice persists across visits.

## Adding a capture event

1. Pick a `snake_case` name and the distinct id (user or org id).
2. Call the capturer at the trigger site. Handlers already hold it:
   `s.analytics.Capture(user.ClerkUserID, "thing_happened",
   map[string]any{"org_id": org.ClerkOrgID})`. In the billing processor, use
   the injected `p.Capture` func.
3. Assert it with a fake capturer — `internal/web/observability_test.go`
   shows the pattern.

No new config, no new dependency, no-op when unconfigured.

## pprof (non-production)

Outside production the stdlib profiler is mounted at `/debug/pprof/`:

```sh
go tool pprof http://localhost:8080/debug/pprof/heap
go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30
```

CPU, heap, and goroutine profiles with zero config. The routes are not
registered when `APP_ENV=production`.

## Health endpoints

`/healthz` (liveness, includes the build version) and `/readyz` (readiness,
pings the DB) double as the cheapest uptime monitor. Their exact semantics
are in [Deployment](/docs/deployment).
