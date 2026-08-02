---
title: Authentication
description: Clerk-hosted auth, the 60-second session JWT, and the Verifier seam.
section: Features
weight: 6
---

GoGoGadget does not implement authentication. **Clerk's hosted Account Portal
owns every credential surface**: sign-in, sign-up, OAuth providers, passwords,
email verification, magic links, and 2FA. There is no password column in the
database, no reset flow, and no session table — the app verifies a JWT and
mirrors profile data locally. This page covers the session lifecycle, the
`Verifier` seam, the guard chain, and the dev bypass.

## The hosted-portal model

Three routes in the app redirect to Clerk-hosted pages, with `redirect_url`
pointing back:

| App route | Portal destination |
|---|---|
| `GET /login` | `{CLERK_PORTAL_URL}/sign-in?redirect_url={APP_URL}/app` |
| `GET /signup` | `{CLERK_PORTAL_URL}/sign-up?redirect_url={APP_URL}/app` |
| `GET /logout` | `{CLERK_PORTAL_URL}/sign-out?redirect_url={APP_URL}/` |

Profile management, password changes, 2FA enrollment, and org management live
at `{CLERK_PORTAL_URL}/user-profile` and `/organization-profile` — the
settings pages link out to them. Enabling Google OAuth or TOTP 2FA is Clerk
dashboard configuration, not code.

## The `__session` lifecycle — and why clerk-js is load-bearing

Clerk's `__session` cookie is a **JWT that expires in ~60 seconds**. The
server never refreshes it. The vendored `static/vendor/clerk.browser.js`,
initialized in `static/app.js`, keeps it fresh:

1. The layout emits `<meta name="clerk-publishable-key" content="…">` when
   `CLERK_PUBLISHABLE_KEY` is set.
2. On `DOMContentLoaded`, `static/app.js` reads the meta tag, calls
   `window.Clerk.load()`, and clerk-js begins refreshing the JWT against the
   Clerk Frontend API in the background (that origin is in the CSP
   `connect-src` — see [Security](/docs/security)).
3. clerk-js also mounts the prebuilt `UserButton` at `#user-button` and the
   `OrganizationSwitcher` at `#org-switcher`.

**Removing clerk-js means auth expires about a minute after login.** It is
vendored — not loaded from a CDN — so `script-src 'self'` stays intact. Test
and e2e environments leave `CLERK_PUBLISHABLE_KEY` empty; the bootstrap finds
no meta tag and skips clerk-js entirely, because the bypass verifier owns auth
there.

Server-side, `ClerkVerifier` verifies the JWT against Clerk's JWKS with a
10-second leeway. An invalid or expired token is treated as *unauthenticated*
— the request continues without identity and `RequireAuth` redirects.

## The Verifier seam

`internal/identity/verifier.go` is the only file that imports the Clerk SDK:

```go
type Verifier interface {
	Verify(ctx context.Context, token string) (*Claims, error)
}

type Claims struct {
	UserID, OrgID, OrgRole, OrgSlug string
}
```

`ClerkVerifier` implements it against JWKS; `FakeVerifier` implements it for
tests. Handlers and middleware only ever see `Claims` — swapping auth
providers means replacing one file.

## sessionLoad: cookie to context

The `sessionLoad` middleware runs on every request:

1. Extract the `__session` cookie. Absent → continue unauthenticated.
2. `Verify` → `Claims`. Failure → continue unauthenticated.
3. Look up the local `users` mirror row by `claims.UserID`. On a miss, fetch
   the profile via the `UserFetcher` seam (Clerk users API in production) and
   **lazy-upsert** the row — so a user whose `user.created` webhook hasn't
   landed yet still gets in. A fetch failure renders **503 "Identity sync in
   progress"**; a refresh retries.
4. On first sight of an email matching `ADMIN_EMAIL`, grant `is_admin` (the
   `user.created` webhook applies the same grant).
5. Set `ctxUser` + `ctxClaims`; when the claims carry an active `org_id`,
   load the org mirror row into `ctxOrg`. See
   [Architecture](/docs/architecture) for the context-key inventory.

## Guards

The `/app` chain is `RequireAuth → RequireNotDisabled → RequireOrg →
LoadPlan`, and `/admin` adds `RequireAdmin`. The order is load-bearing.

- **RequireAuth** — anonymous → `303 /login`. For HTMX requests: `401` +
  `HX-Redirect: /login` (a fragment must never render a redirect target).
- **RequireNotDisabled** — a user with `disabled_at` set gets the Disabled
  page with **403** (see [Admin](/docs/admin)).
- **RequireOrg** — no active org in claims → query mirror memberships: one or
  more → render the SelectOrg page; zero → redirect to
  `{CLERK_PORTAL_URL}/create-organization?redirect_url={APP_URL}/app`. Claims
  naming an org the mirror hasn't synced yet → 503 "Organization sync in
  progress" (the webhook is in flight). Details in
  [Organizations](/docs/organizations).
- **LoadPlan** — resolves the org's subscription into `ctxSub` + `ctxPlan`
  via [billing entitlements](/docs/billing).
- **RequireAdmin** — `!user.IsAdmin` → 403.

When neither Clerk nor the bypass is configured, `/app` routes render a 503
"not configured" fragment instead of crashing.

## Dev bypass and e2e tokens

`DEV_AUTH_BYPASS=true` wires `FakeVerifier` in place of `ClerkVerifier`. The
config loader **hard-errors at boot if this is combined with
`APP_ENV=production`**. Synthetic tokens have the exact shape:

```
__session=e2e:<userID>:<orgID>:<role>
```

An empty `orgID` means "no active org" — which is how tests exercise the
SelectOrg and create-organization branches. Every guard and middleware still
executes; only the token check is synthetic. `DevUserFetcher` synthesizes
profiles (`<userID>@gogogadget.dev`) so the lazy upsert works with no Clerk
account. `GET /dev/login` sets the demo cookie
(`e2e:user_demo:org_demo:org:admin`) and lands in `/app`. See
[Getting started](/docs/getting-started) for the zero-account walkthrough and
[Testing](/docs/testing) for the Playwright harness.

## Mirror sync

Profile changes after sign-in arrive as webhooks: `POST /webhooks/clerk`
verifies the `svix-id` / `svix-timestamp` / `svix-signature` headers (Clerk
delivers via Svix) and upserts the local mirror — covered in
[Organizations](/docs/organizations). The mirror is what lets org-scoped
queries stay local SQL instead of API calls.
