---
title: Security
description: The middleware chain, CSP/CSRF/SSRF rules, provider-neutral identity, credential discipline, signed registries, and the refusals that fail closed.
section: Guides
weight: 25
---

The security posture is a small set of **enforced defaults** plus a set of
**refusals**. Most of the request-path half lives in one file
(`internal/web/middleware.go`), so auditing it audits the request path; the
supply-chain half lives in the registry engine, which refuses before it
writes.

## Middleware order is load-bearing

Assembled in `Server.Handler()`, outermost first:

```
MaxBytesReader (10 MB) → provider environment → telemetry.HTTP → routeBodyLimit
→ requestID → accessLog → i18n.Detect → maintenanceMode → rateLimit
→ secureHeaders → sessionLoad → csrf → route groups
```

Route groups add their own chains: `/app/*` is `requireAuth` →
`requireNotDisabled` → `requireOrg` → `loadPlan`; `/admin/*` is that chain
plus `requireStaff` → `requireAdminWrite`; `/api/v1/*` is
`RequireAPIToken(scope)`.

`routeBodyLimit` sits **outside** csrf on purpose: parsing a form reads the
body, so a cap applied after csrf has already been bypassed. The policy
matcher is fed the same `enabledRoutes` slice the mux is built from, so a
route whose `Enabled` or `ProviderActive` gate refused registration resolves
to no policy at all rather than to a stale one.

## Content-Security-Policy

Every response carries this policy, assembled from config in `secureHeaders`:

| Directive | Value | Why |
|---|---|---|
| `default-src` | `'self'` | Nothing loads cross-origin unless explicitly allowed |
| `script-src` | `'self'` | No third-party JavaScript, ever |
| `worker-src` | `'self' blob:` | clerk-js runs its session handshake in a blob: Web Worker |
| `style-src` | `'self' 'unsafe-inline'` | Tailwind is a built file; inline *styles* (not scripts) are allowed |
| `img-src` | `'self' data: https://img.clerk.com` | Clerk avatars |
| `font-src` | `'self'` | Vendored Inter |
| `connect-src` | `'self' <CLERK_FRONTEND_API_URL>` | clerk-js keeps the short-lived session JWT fresh |
| `frame-ancestors` | `'none'` | Clickjacking |
| `base-uri` | `'self'` | Base-tag injection |
| `form-action` | `'self'` | Forms can only post back here |

`script-src 'self'` is possible because there is **zero inline JavaScript**:
Alpine runs the CSP build with every component registered as `Alpine.data`,
htmx and clerk-js are vendored into `static/vendor/` and sha256-pinned, and
PostHog loads through the same-origin `/ingest` proxy. When something reports
a blocked source, add exactly that origin to the matching directive with an
inline note. Never widen `script-src`.

The rest of the header set on every response:
`X-Content-Type-Options: nosniff`, `Referrer-Policy:
strict-origin-when-cross-origin`, `X-Frame-Options: DENY`, and
`Permissions-Policy: camera=(), microphone=(), geolocation=()`. HSTS
(`max-age=63072000; includeSubDomains`) is sent **in production only** — over
dev HTTP it would poison `localhost` for every other app you run.

## CSRF

nosurf guards every unsafe method. The token reaches htmx via
`hx-headers:inherited` on `<body>`; failures render 403 (with the exact
`nosurf.Reason` shown outside production).

The `:inherited` suffix is load-bearing. Attribute inheritance is **explicit**
in htmx 4: a plain `hx-headers` on `<body>` would apply to `<body>` alone and
every nested form would post without the token.

- **Cookie names differ by environment on purpose.** Production:
  `__Host-csrf` (Secure, HttpOnly, SameSite=Lax, Path=/). Development:
  `csrf_token`, non-Secure. A `__Host-`-prefixed cookie without `Secure` is
  rejected by Safari, which would silently break non-localhost dev.
- **nosurf v1.2 enforces same-origin** (CVE-2025-46721) and assumes HTTPS by
  default, so `SetIsTLSFunc` decides per request: direct TLS, or
  `X-Forwarded-Proto: https` from the edge.
- **Exempt paths:** `/webhooks/*` (signature-verified), `/api/*` (cookieless
  Bearer — no cookie, nothing to forge), `/ingest/*`, `/static/*`,
  `/healthz`, `/readyz`.

## Identity ids are provider-neutral

No provider's identifier is ever a domain key. Internal ids are opaque
(`usr_<32 hex>`, `org_<32 hex>`), and three mapping tables hold the join:

| Table | Primary key | Also unique |
|---|---|---|
| `identity_subjects` | `(provider, subject)` | `(provider, user_id)` |
| `identity_organizations` | `(provider, subject)` | `(provider, org_id)` |
| `billing_accounts` | `(provider, provider_customer_id)` | `(provider, org_id)` |

One subject per provider per domain row, and one domain row may carry
different providers. Claims in `context.Context` carry `UserID`/`OrgID` only;
`UserSubject`/`OrgSubject` exist solely at the adapter boundary. Swapping an
identity or billing provider is an adapter change plus a mapping row, not a
rewrite of every tenant-owned table.

**Mutable identity data never auto-links.** A verified subject whose email or
org slug already exists locally under a different subject returns
`identity.ErrLinkRequired` rather than merging accounts on a matching email —
that would be an account-takeover primitive. Linking is a deliberate, audited
operator action:

```sh
ggg identity link --environment production --provider clerk \
  --subject user_2abc… --user usr_0f3c…
```

It resolves the adapter, target and secrets only from the named environment,
verifies the subject through that adapter, and requires exactly one
destination (`--user` or `--org`, never both).

## Webhook verification

Two header families, one shared idempotency ledger:

| Provider | Headers | Library | Endpoint |
|---|---|---|---|
| Clerk (via Svix) | `svix-id`, `svix-timestamp`, `svix-signature` | `github.com/svix/svix-webhooks/go` | `POST /webhooks/clerk` |
| Polar | `webhook-id`, `webhook-timestamp`, `webhook-signature` | `github.com/standard-webhooks/standard-webhooks/libraries/go` | `POST /webhooks/polar` |

Reach for one library for both and you reject 100% of the other provider's
real traffic. Test fixtures (`signSvix`, `signStandard`) mirror the real
header names exactly.

The HTTP handlers do not parse signatures themselves: they call
`identity.Webhook.Verify` / `billing.BillingWebhook.Verify` and receive a
provider-neutral event, so `internal/web` imports no provider SDK. The
`webhook_events` table deduplicates by message id (`INSERT … ON CONFLICT DO
NOTHING RETURNING id` — `pgx.ErrNoRows` means a replay: stop, return 200).
Signature failure → 400; a database error → 500 so the provider retries.

## Outbound webhooks and SSRF

Customer endpoints are signed with the standard-webhooks library
(`webhook-id`/`webhook-timestamp`/`webhook-signature`), and the destination
guard is double-checked: the URL is validated, then the resolved IP is checked
again at dial time. `isPublicIP` is an **allow-list** — global unicast, then
explicit non-routable blocks including RFC 6598 `100.64.0.0/10` — so an
unenumerated range fails closed. Delivery is https-only in every environment
and refuses redirects outright (`CheckRedirect`): a 302 would hand a signed
payload to a host the guard never classified. Secret rotation keeps the
previous secret verifying for a 24-hour dual-sign grace window.

## Rate limiting

Rate limiting is a provider slot (`ggg/rate-limit`), so the budget is enforced
by the selected adapter — memory in development and test, Redis (Valkey or
Upstash) where more than one process serves traffic. The limiter **fails
closed**: an adapter error, or a missing limiter capability, is a 503 with the
diagnostic `rate_limit_unavailable` (JSON under `/api/`), never an open door.
Over-limit requests get 429 with `Retry-After: 1`. Routes may declare
`RateExempt` in their policy, and `/ingest/*` is exempt by name.

Authenticated API traffic gets a **second, independent** budget keyed on the
API token row id (`API_RATE_LIMIT_RPM`, default 60/min, burst 2×), enforced in
`RequireAPIToken` after scope resolution. Keying on the row id rather than the
plaintext means the limiter's map never holds a live credential; running the
check after authentication means a 429 always says "over budget" and never
doubles as an auth failure.

## API tokens

Tokens are `ggg_` plus 32 random bytes (base64url). The database stores only
the **SHA-256 hex** — a leaked dump yields nothing usable, and the plaintext
is shown exactly once at creation. Verification hashes the presented token and
looks it up; `revoked_at` and `expires_at` are enforced on every request;
scope `write` satisfies `read`. The API is cookieless Bearer auth, which is
why it is CSRF-exempt. See [API](/docs/api).

## Credential discipline

- **`gogogadget.json` cannot express a secret.** It holds registries, module
  selections, provider choices and the deployment module — nothing else. Nor
  can the `--answers` file `ggg new` accepts.
- **Development and test values the CLI manages** live in
  `.ggg/env/<environment>.env`, created at mode `0600` inside gitignored
  `.ggg/`. Lookup order is process environment → that file → the legacy `.env`
  (development only). **No file is read when the environment is production.**
- **Production values are never written to disk by the tooling.**
  `ggg provider configure --environment production --set …` is refused, and
  the Docker deploy target refuses production secrets outright.
- **Nothing secret enters a plan, an argv, an envelope or `.ggg/state.json`.**
  Plans carry key *names*; `ggg deploy secrets` streams values to the platform
  over stdin. Every declared secret passes through the redactor before any
  output — human, JSON, diagnostics, or console — leaves the process.
- **`.env` is never read or written by the tooling.** `.env.example` is
  generated from manifest `environment` declarations; copying a line into your
  own `.env` is a human act.

## Supply chain

- **Remote registries must be signed.** `registry.snapshot.json` lists the
  registry root, indexes, profiles, manifests and every payload path with its
  SHA-256; `registry.snapshot.sig` is a detached Ed25519 signature over those
  exact bytes. A project pins the base64 public key, and the signature plus
  every listed digest is verified **before** the catalog is parsed. A file
  present under a payload root but absent from the snapshot is refused. An
  unsigned tree is consumable only as an explicitly configured, project-relative
  `directory` source.
- **Key rotation needs both signatures and a clock.**
  `registry-key-rotation.json` declares `{namespace, old_fingerprint,
  new_public_key, not_before}`; the new key is honoured only once the old and
  new detached signatures both verify and the wall clock reaches `not_before`.
- **Snapshots are pinned in the lock.** Each installed module records its
  registry namespace, source commit and snapshot digest, and `--offline`
  re-reads only cached bytes and re-checks all of it. A tampered archive, a
  bad signature, a namespace that does not match what was pinned, a colliding
  canonical Go module prefix, and a dependency outside its declared contract
  range each refuse before the first byte is written.
- **Installation executes nothing.** Manifests are data: no hook, no
  postinstall, no command array, no shell fragment. Tool artifacts are
  digest-verified before extraction, which rejects absolute paths, `..`,
  symlinks and any undeclared executable bit.
- **Contributed CLI commands are inert until invoked.** A `runtime.cli`
  contribution must claim its name, cannot shadow a built-in, and its handler
  reaches the project only through the controller — never a provisioner, a
  deploy client, or `SecretValues`.
- **Vendored browser assets are self-hosted and checksummed**, recorded in
  their manifests with source URL, version, byte count, SHA-256 and licence;
  `ggg registry build` re-verifies them and rejects `eval(`, `new Function(`,
  string-argument `setTimeout`/`setInterval`, and undeclared origins.
- **Dependencies are declared, not discovered.** Every Go dependency is an
  exact `{module, version}` in a manifest, and before the lock or `go.mod` is
  touched the planner scans the authored and generated imports and refuses an
  undeclared direct dependency. CI runs `go tool govulncheck ./...`.

## Staff roles and impersonation

Platform access is two-tier (`users.admin_role`): `support` reads `/admin`,
`admin` also changes it. Enforcement is a single method-based guard
(`requireAdminWrite`) rather than a per-route annotation, so routes added
later inherit the boundary. Role grants are audited
(`admin.role_changed`, old and new value) and the last full admin cannot be
demoted.

Impersonation is reason-gated: the interstitial requires a 10–280 character
reason, stored on the `impersonation_sessions` row and in both the
`impersonation.start` and `impersonation.stop` audit entries. Audit rows have
no foreign keys, so the trail outlives the session and the accounts involved.

## The fire-and-forget quartet

`audit.Log`, `notify.Send`, `webhooks.Emit` and `usage.Record` log their
errors and never return them: a notification, webhook or meter hiccup must
never fail the request that triggered it. That is a deliberate availability
trade, and it is why the audit **ledger** write is not one of them — the
durable Postgres audit row is written transactionally, and
`ggg/audit-export` (OTLP or noop) is a separate, additive slot that can never
replace or bypass it.

## Exports never carry secrets

Both self-serve exports map database rows to explicit DTOs. That is a security
control, not a style: `api_tokens.token_hash`, `webhook_endpoints.secret` and
`secret_previous` have json tags, so marshaling sqlc rows straight into an
export would hand a downloadable file every credential the org holds. The org
export is tested against exactly that regression.

## XSS and request bodies

- **templ auto-escapes everything.** User-controlled content never goes
  through `templ.Raw` — no exceptions.
- **goldmark renders markdown with default options** (GFM on): raw HTML in
  content stays escaped. Never enable `html.WithUnsafe`.
- Error pages include panic and validation detail only outside production.
- Every route is capped by `http.MaxBytesReader` at **10 MB**. A route may
  declare a tighter `RoutePolicy.MaxBodyBytes`, which only ever narrows — the
  webhook receivers declare **1 MiB**, because they `io.ReadAll` the body
  before verifying a signature and an oversized delivery must be refused
  before any HMAC work happens.

## The dev backdoor cannot ship

`DEV_AUTH_BYPASS=true` enables synthetic `e2e:` session tokens for local and
e2e runs. Combined with `APP_ENV=production` it is a **hard boot error** — the
escape hatch physically cannot reach production. The dev-only routes
(`/dev/login`, `/dev/gallery`, `/dev/scenarios`) are registered only when the
bypass is on. See [Authentication](/docs/authentication).

## Data export and account deletion (GDPR self-serve)

`/app/settings/account` exposes both rights directly to the user:

- **Export** (`GET /app/settings/account/export`) downloads profile,
  memberships, the last 10,000 notification rows, and the user's audit trail
  as one JSON document. The export itself writes an `account.exported` audit
  row.
- **Deletion** (`POST /app/settings/account/delete`) requires typing the
  account email (case-insensitive; `users.email` is CITEXT). Guards, in
  order: never while an impersonation session is active (403); never when the
  user is the sole admin of a multi-member org (422 naming the org). Deletion
  order: the identity provider first through the `identity.Deleter`
  capability, resolved by subject (a failed upstream delete aborts with
  nothing local removed), then impersonation session rows (they carry no
  cascade FK), then every org where the user is the only member, then the user
  row.

Two deliberate survivors: **audit rows are retained** (`audit_log` has no
foreign keys by design; retention is `AUDIT_RETENTION_DAYS`, 0 = forever), and
**stored objects** remain in the bucket — the rows pointing at them are gone,
and a lifecycle rule is the platform owner's call.

Dev-bypass caveat: under `DEV_AUTH_BYPASS` the `users` row is a local mirror
that `sessionLoad` lazily re-upserts on the next authenticated request, so a
dev "deleted" user reappears on the next request with the same synthetic
cookie. Production deletes the upstream user, which kills the session
upstream.
