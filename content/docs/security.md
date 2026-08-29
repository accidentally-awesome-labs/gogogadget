---
title: Security
description: CSP, CSRF, rate limiting, webhook verification, token hashing, and the XSS rules.
section: Guides
weight: 25
---

The security posture is a small set of **enforced defaults**, not a checklist.
Most of it lives in one middleware chain (`internal/web/middleware.go`), so
auditing one file audits the request path.

## Content-Security-Policy

Every response carries this policy, assembled from config in `secureHeaders`:

| Directive | Value | Why |
|---|---|---|
| `default-src` | `'self'` | Nothing loads cross-origin unless explicitly allowed |
| `script-src` | `'self'` | No third-party JavaScript, ever |
| `style-src` | `'self' 'unsafe-inline'` | Tailwind is a built file; inline *styles* (not scripts) are allowed |
| `img-src` | `'self' data: https://img.clerk.com` | Clerk avatars |
| `font-src` | `'self'` | Vendored Inter |
| `connect-src` | `'self' <CLERK_FRONTEND_API_URL>` | clerk-js keeps the ~60s `__session` JWT fresh against the Frontend API |
| `frame-ancestors` | `'none'` | Clickjacking |
| `base-uri` | `'self'` | Base-tag injection |
| `form-action` | `'self'` | Forms can only post back here |

### Why `script-src 'self'` is possible

- **Zero inline JavaScript.** Dark-mode init, Alpine components, the toast
  listener — all live in `static/app.js`. Not one inline `<script>` body
  exists in a template.
- **Alpine runs the CSP build** (`@alpinejs/csp`) with every component
  registered as `Alpine.data` in `app.js` — no inline `x-data` expressions
  for the browser to evaluate.
- **htmx and clerk-js are vendored** into `static/vendor/`, sha256-pinned by
  `scripts/vendor-frontend.sh`.
- **PostHog loads through the same-origin `/ingest` proxy**, so its SDK is a
  self-hosted script as far as the browser knows. See
  [Observability](/docs/observability).

### Extending `connect-src`

When clerk-js (or anything else) reports a blocked source in the browser
console, first check `CLERK_FRONTEND_API_URL` — most "blocked Clerk source"
reports are a misconfigured origin, not a missing directive. If the source is
legitimate, add **exactly that reported origin** to the matching directive in
`secureHeaders` (`connect-src` for fetches, `frame-src` for frames) with an
inline note saying why. Never widen `script-src`.

## The rest of the header set

`X-Content-Type-Options: nosniff`, `Referrer-Policy:
strict-origin-when-cross-origin`, `X-Frame-Options: DENY`, and
`Permissions-Policy: camera=(), microphone=(), geolocation=(),
interest-cohort=()` on every response. HSTS (`max-age=63072000;
includeSubDomains`) is sent **in production only** — over dev HTTP it would
poison `localhost` for every other app you run.

## CSRF

nosurf guards every unsafe method. The token reaches htmx via
`hx-headers:inherited` on `<body>`; failures render 403 (with the exact
`nosurf.Reason` shown outside production).

The `:inherited` suffix is load-bearing. Attribute inheritance is **explicit** in
htmx 4: a plain `hx-headers` on `<body>` applies to `<body>` alone, so every
nested form would post without the token and 403. This is the only inherited
attribute in the codebase — there is no `implicitInheritance` compatibility
shim — and `TestProjectCreateSearchArchiveDelete` posts a real form through the
CSRF middleware to keep it that way.

- **Cookie names differ by environment on purpose.** Production:
  `__Host-csrf` (Secure, HttpOnly, SameSite=Lax, Path=/). Development:
  `csrf_token`, non-Secure. A `__Host-`-prefixed cookie without `Secure` is
  **rejected by Safari**, which would silently break non-localhost dev — that
  is why the dev name exists.
- **nosurf v1.2 enforces same-origin** (CVE-2025-46721) and assumes HTTPS by
  default. Over plaintext dev HTTP every POST would 403, so `SetIsTLSFunc`
  decides per request: direct TLS, or `X-Forwarded-Proto: https` from the
  fly.io edge.
- **Exempt paths:** `/webhooks/*` (signature-verified), `/api/*` (cookieless
  Bearer — no cookie, nothing to forge), `/ingest/*`, `/static/*`,
  `/healthz`, `/readyz`.

## Staff roles

Platform access is two-tier (`users.admin_role`): `support` reads `/admin`,
`admin` also changes it. The separation exists because the alternative is
handing a support hire impersonation, account disable, feature-flag mutation,
schedule run-now, and dead-letter requeue in order to let them read a
dashboard.

Enforcement is a single method-based guard (`requireAdminWrite`) rather than a
per-route annotation, so routes added later inherit the boundary instead of
silently missing it. Role grants are audited (`admin.role_changed`, with the
old and new value), and the last full admin cannot be demoted — see
[Admin](/docs/admin).

## Exports never carry secrets

Both self-serve exports map database rows to explicit DTOs. That is a security
control, not a style: `api_tokens.token_hash`, `webhook_endpoints.secret` and
`secret_previous` have json tags, so marshaling sqlc rows straight into an
export would hand a downloadable file every credential the org holds. The
org export is tested against exactly that regression — the assertion fails if
a secret field is ever added back to the payload.

## Rate limiting

The budget is `RATE_LIMIT_RPM` (default 100/min per IP, burst 2×) — test
harnesses that drive a single IP raise it; production keeps the default.

Per-IP token bucket (`x/time/rate`): **100 req/min, burst 200**, on everything
except `/static/*`, `/healthz`, `/ingest/*`. Over-limit requests get 429 with
`Retry-After: 1`. The client IP is `Fly-Client-IP` when present (set by the
fly edge, not spoofable by clients there) and `RemoteAddr` otherwise — behind
any other proxy, the header story is yours to fix. Entries idle longer than
10 minutes are swept so the map cannot grow forever.

### Per-token budgets on the API

Authenticated API traffic gets a **second, independent** budget keyed on the
API token: `API_RATE_LIMIT_RPM` (default 60/min, burst 2×), enforced in
`RequireAPIToken` after scope resolution. A token is a better identity than an
address — it survives NAT and roaming, and it is the thing a customer can
rotate. Over-budget requests get a JSON `429 rate_limited` with `Retry-After`,
never the HTML error page.

The bucket is keyed on the token **row id**, not the plaintext, so the
limiter's map never holds a live credential. The check runs *after*
authentication, so a 429 always means "over budget" and never doubles as an
auth failure; unauthenticated hammering is shed by the per-IP shield instead.

Both layers apply. Behind a shared egress IP the per-IP shield is the binding
constraint, so an installation serving API clients from one NAT should raise
`RATE_LIMIT_RPM` and let `API_RATE_LIMIT_RPM` do the per-customer fairness.

This limiter is **single-node by design**. `fly scale count > 1` is the
documented trigger to swap it for a shared store (e.g. Upstash); the swap
point is the `rateLimit` middleware and nothing else.

## Webhook verification: two header families

Clerk and Polar both sign deliveries, but with **different header families**,
so one verification library cannot cover both:

| Provider | Headers | Library | Endpoint |
|---|---|---|---|
| Clerk (via Svix) | `svix-id`, `svix-timestamp`, `svix-signature` | `github.com/svix/svix-webhooks/go` | `POST /webhooks/clerk` |
| Polar | `webhook-id`, `webhook-timestamp`, `webhook-signature` | `github.com/standard-webhooks/standard-webhooks/libraries/go` | `POST /webhooks/polar` |

Reach for one library for both and you reject 100% of the other provider's
real traffic. Test fixtures (`signSvix`, `signStandard`) mirror the real
header names exactly.

Beyond signatures, the `webhook_events` table deduplicates by message id
(`INSERT … ON CONFLICT DO NOTHING RETURNING id` — `pgx.ErrNoRows` means a
replay: stop, return 200). Signature failure → 400; a DB error → 500 so the
provider retries.

## API tokens

Tokens are `ggg_` plus 32 random bytes (base64url). The database stores only
the **SHA-256 hex** — a leaked dump yields nothing usable, and the plaintext
is shown exactly once at creation. Verification hashes the presented token
and looks it up; `revoked_at` and `expires_at` are enforced on every request;
scope `write` satisfies `read`. The API is cookieless Bearer auth, which is
why it is CSRF-exempt. See [API](/docs/api).

## XSS rules

- **templ auto-escapes everything.** User-controlled content never goes
  through `templ.Raw` — no exceptions.
- **goldmark renders markdown with default options** (GFM on): raw HTML in
  content stays escaped. Never enable `html.WithUnsafe`.
- Error pages include panic/validation detail only outside production; in
  production they render generic copy and report to Sentry.

## Request bodies

Every route is capped by `http.MaxBytesReader` at **10 MB** — a memory-
exhaustion vector closed by default, not per-handler.

A route may declare a tighter cap through `RoutePolicy.MaxBodyBytes`, which
`routeBodyLimit` applies outside CSRF (parsing a form reads the body, so a cap
applied after that has already been bypassed). It only ever narrows: a declared
value at or above the global cap changes nothing. The webhook receivers are why
it exists — they `io.ReadAll` the body before verifying a signature, so
`/webhooks/clerk` and `/webhooks/polar` declare **1 MiB** and an oversized
delivery is refused before any HMAC work happens.

## Dependency policy

Every dependency is load-bearing and listed in the manifest
(see [Architecture](/docs/architecture)); a new dependency needs a
justification, not just an `import`. Codegen tools are pinned as `go tool`
directives in `go.mod`; the Tailwind binary and all vendored frontend assets
are sha256-pinned in their fetch scripts. CI runs `go tool govulncheck ./...`
— known-vulnerable dependencies do not merge.

## Impersonation is reason-gated

Admins cannot "view as" a user in one click: the interstitial requires a
stated reason (10–280 characters), which lands on the `impersonation_sessions`
row and in both the `impersonation.start` and `impersonation.stop` audit
entries. Audit rows have no foreign keys, so the trail outlives the session
and the accounts involved.

## The dev backdoor cannot ship

`DEV_AUTH_BYPASS=true` enables synthetic `e2e:` session tokens for local and
e2e runs. Combined with `APP_ENV=production` it is a **hard boot error** —
the escape hatch physically cannot reach production. See
[Authentication](/docs/authentication).

## Data export and account deletion (GDPR self-serve)

`/app/settings/account` exposes both rights directly to the user:

- **Export** (`GET /app/settings/account/export`) downloads everything the
  platform holds about the account as one JSON document: profile, memberships
  (org, role, since), the last 10,000 notification rows, and the user's audit
  trail. The export itself writes an `account.exported` audit row.
- **Deletion** (`POST /app/settings/account/delete`) requires typing the
  account email (case-insensitive; `users.email` is CITEXT). Guards, in
  order: never while an impersonation session is active (403); never when the
  user is the sole admin of a multi-member org (422 naming the org — transfer
  admin first). Deletion order: Clerk first via the `identity.Deleter` seam
  (a failed upstream delete aborts with nothing local removed), then
  impersonation session rows (they carry NO cascade FK), then every org where
  the user is the only member (cascading subscriptions, projects, files rows,
  API tokens, and flag overrides), then the user row (cascading memberships,
  notifications, and preferences).

Two deliberate survivors: **audit rows are retained** (`audit_log` has no
foreign keys by design — the audit trail outlives the entities it records;
retention is configurable via `AUDIT_RETENTION_DAYS`, 0 = forever by
default), and **R2 objects** remain
in the bucket (the DB rows pointing at them are gone; a lifecycle rule on the
bucket is the platform owner's call).

Dev-bypass caveat: under `DEV_AUTH_BYPASS` the `users` row is a local mirror
that `sessionLoad` lazily re-upserts on the next authenticated request — a
dev "deleted" user can reappear by simply requesting again with the same
synthetic cookie. Production deletes the Clerk user, which kills the session
JWT upstream.
