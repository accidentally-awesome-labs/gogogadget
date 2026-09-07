---
title: Authentication
description: Clerk-hosted auth, the 60-second session JWT, and the Verifier seam.
section: Features
weight: 7
---

GoGoGadget does not implement authentication. **Clerk's hosted Account Portal
owns every credential surface**: sign-in, sign-up, OAuth providers, passwords,
email verification, magic links, and 2FA. There is no password column in the
database, no reset flow, and no session table — the app verifies a JWT and
mirrors profile data locally. This page covers the session lifecycle, the
`Verifier` seam, the guard chain, and the dev bypass.

## The hosted-portal model

GoGoGadget sends sign-in and sign-up through a public callback page so
clerk-js can establish the local session before a protected route runs.

Every destination below is asked for through `identity.Navigator`, so the
adapter selected for the environment owns the path, the name of the return
parameter, and the escaping. The neutral web package passes a destination and
a return target, never a URL fragment. With `identity-clerk` selected:

| App route | Navigator destination | Clerk Account Portal URL |
|---|---|---|
| `GET /login` | `LoginURL` | `{CLERK_PORTAL_URL}/sign-in?redirect_url={APP_URL}/?after-auth=1` |
| `GET /signup` | `SignupURL` | `{CLERK_PORTAL_URL}/sign-up?redirect_url={APP_URL}/?after-auth=1` |
| `GET /logout` | `LogoutURL` | `{CLERK_PORTAL_URL}/sign-out?redirect_url={APP_URL}/` |
| `/app/settings/account` link | `AccountURL` | `{CLERK_PORTAL_URL}/user?redirect_url={APP_URL}/app/settings/account` |
| `/app/settings/org` link | `OrganizationURL` | `{CLERK_PORTAL_URL}/organization?redirect_url={APP_URL}/app/settings/org` |
| zero-organization guard | `CreateOrganizationURL` | `{CLERK_PORTAL_URL}/create-organization?redirect_url={APP_URL}/app` |

`GET /logout` expires the `__session` cookie before handing off, for every
adapter. That cookie is the one this application reads on each request, so
leaving it in place while redirecting to a hosted sign-out kept the visitor
signed in locally until clerk-js happened to clear it.

A destination the selected adapter does not publish is a refusal
(`identity.ErrNoDestination`), never an empty URL. The settings pages render
their explanatory copy with no link; the guards render a named 503 that says
which provider and which destination. `identity-dev` publishes only
`LoginURL`/`SignupURL` (its own `/dev/login`) and `LogoutURL` (the home page):
a synthetic profile has nothing to manage, and this application serves no
org-creation route in any environment.

The callback renders the public page, lets clerk-js finish its development
handshake, then replaces the location with `/app`. Redirecting straight to
`/app` creates a loop when Clerk still needs a rendered page to establish the
local `__session` cookie.

Clerk invitation emails do not necessarily carry a per-invitation
`redirect_url`. In **Clerk Dashboard → Account Portal → Redirects**, set both
fallback sign-in and sign-up URLs to `{APP_URL}/?after-auth=1`. Without these
fallbacks, an invitee can finish joining successfully but land on Clerk's
`/default-redirect` page.

Enabling Google OAuth or TOTP 2FA is Clerk dashboard configuration, not code.

## The `__session` lifecycle — and why clerk-js is load-bearing

Clerk's `__session` cookie is a **JWT that expires in ~60 seconds**. The
server never refreshes it. The vendored `static/vendor/clerk.browser.js`,
initialized in `static/app.js`, keeps it fresh:

1. `ggg/system/identity-clerk` contributes a `head` shell slot that emits
   `<meta name="clerk-publishable-key" content="…">` and the deferred
   `clerk.browser.js` tag when `CLERK_PUBLISHABLE_KEY` is set. The shell owns
   no part of this: a slot renderer receives its own module's declared
   non-secret configuration, so the loader lives with the adapter that also
   installs the script, and it renders only in the environments whose
   `ggg/identity` selection is this adapter.
2. On `DOMContentLoaded`, `static/app.js` reads the meta tag, calls
   `window.Clerk.load()` once, and clerk-js begins refreshing the JWT against
   the Clerk Frontend API in the background (that origin is in the CSP
   `connect-src` — see [Security](/docs/security)).
3. The bootstrap mounts the prebuilt `UserButton` at `#user-button` and the
   `OrganizationSwitcher` at `#org-switcher` — **once per page load**. Both
   elements are contributed by this adapter too, through the `user-button` and
   `org-switcher` shell slots, including their `clerk-*-slot` classes and
   placeholder attributes. Deselect the adapter and the shell renders its own
   empty containers instead, carrying no provider classes at all.
4. **App navigation swaps only `#content`.** clerk-js mounts React components
   into those two roots *and* renders their dropdown menus as **portals
   appended directly to `<body>`** — siblings of the mount roots, not children.
   That makes the shell untouchable: no per-element attribute can protect a
   sibling, so any swap or morph of `<body>` deletes the portals and the
   dropdowns go dead (`hx-preserve` is worse still — it *stashes* the element
   and restores it, which detaches clerk's listeners; and `hx-morph-skip` only
   covers the root, not the portal). Alpine bindings in the shell break the
   same way. So app nav links use `hx-boost="true" hx-target="#content"
   hx-select="#content" hx-swap="outerHTML transition:true show:top"`: `<body>`'s other children — the
   mount roots, the portals, the shell's Alpine components — are never in the
   swap at all. Clerk mounts once per full page load and stays mounted: no
   remount, no flash, dropdowns keep working. The trade-off is that the
   persistent sidebar's server-rendered `aria-current` would go stale, so
   `static/app.js` re-syncs it from `data-nav-match` after each swap. htmx also
   owns in-page interactions (pagination, search, form validation) inside
   `#content`.
5. Server-rendered `:empty` fallbacks keep the controls' shape and identity
   visible during the unavoidable first Clerk paint after a page load.
6. A failed `Clerk.load()` is logged, released, and retried once (self-driven,
   so it cannot race the rejection) instead of caching a rejected promise.

**Removing clerk-js means auth expires about a minute after login.** It is
vendored — not loaded from a CDN — so `script-src 'self'` stays intact. Test
and e2e environments leave `CLERK_PUBLISHABLE_KEY` empty; the bootstrap finds
no meta tag and skips clerk-js entirely, because the bypass verifier owns auth
there.

Server-side, the Clerk adapter's `Verifier` verifies the JWT against Clerk's
JWKS with a 10-second leeway. An invalid or expired token is treated as
*unauthenticated* — the request continues without identity and `RequireAuth`
redirects.

## The Verifier seam

`internal/identity` imports no provider SDK at all. It holds the ports and the
neutral types; `internal/identity/clerk` is the only package in the tree that
imports the Clerk SDK, and `internal/identity/devadapter` the only one that
mints the synthetic `e2e:` tokens:

```go
type Verifier interface {
	Verify(ctx context.Context, token string) (*ProviderClaims, error)
}

// ProviderClaims is the provider-facing identity an adapter returns.
type ProviderClaims struct {
	Provider, UserSubject, OrgSubject, OrgRole, OrgSlug string
}

// Claims is the internal identity. IDs are opaque domain identifiers, never
// provider subjects.
type Claims struct {
	UserID, OrgID, OrgRole, OrgSlug string
}
```

`clerk.Verifier` implements the port against JWKS; `identitydev.Verifier`
implements it for the zero-account path. `internal/identity/session` maps a
verified `ProviderClaims` onto internal `Claims`, so handlers and middleware
only ever see `Claims` — swapping auth providers means replacing one adapter
package, and a project that never selects Clerk never compiles its SDK.
Both adapters are held to one table, `internal/identity/contract`.

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
  more → render the SelectOrg page; zero → redirect to the selected adapter's
  `CreateOrganizationURL` (Clerk:
  `{CLERK_PORTAL_URL}/create-organization?redirect_url={APP_URL}/app`). An
  adapter with no such page — `identity-dev` — makes this a named 503 rather
  than a redirect, because a relative `/create-organization` is a route this
  application does not serve. Claims naming an org the mirror hasn't synced
  yet → 503 "Organization sync in progress" (the webhook is in flight).
  Details in [Organizations](/docs/organizations).
- **LoadPlan** — resolves the org's subscription into `ctxSub` + `ctxPlan`
  via [billing entitlements](/docs/billing).
- **RequireAdmin** — `!user.IsAdmin` → 403.

When neither Clerk nor the bypass is configured, `/app` routes render a 503
"not configured" fragment instead of crashing.

## Dev bypass and e2e tokens

Selecting `ggg/system/identity-dev` (with `DEV_AUTH_BYPASS=true`) wires
`identitydev.Verifier` in place of the Clerk adapter's. The generated config
loader **hard-errors at boot if this is combined with `APP_ENV=production`** —
`identity-dev`'s manifest declares `refused_in_production` on the key, so the
refusal is emitted from that declaration and disappears with the adapter rather
than outliving it in the config seam. Synthetic tokens have the exact shape:

```
__session=e2e:<userID>:<orgID>:<role>
```

An empty `orgID` means "no active org" — which is how tests exercise the
SelectOrg and create-organization branches. Every guard and middleware still
executes; only the token check is synthetic. `identitydev.UserFetcher` synthesizes
profiles (`<userID>@gogogadget.dev`) so the lazy upsert works with no Clerk
account. `GET /dev/login` sets the demo cookie
(`e2e:user_demo:org_demo:org:admin`) and lands in `/app`; `/login` and
`/signup` go there too, because that is what this adapter's `LoginURL` and
`SignupURL` answer — the login handler reads no bypass key and names no dev
route of its own.

That shape is written in exactly one place: `identitydev.Verifier.MintSession`,
the `identity.SyntheticSessionMinter` port. Nothing outside the adapter spells
it. `ggg/workflow/dev-session` mints through the port and refuses loudly when
the selected adapter offers no minter — a named 503 rather than a cookie the
verifier rejects — and `GET /dev/session?user=&org=&role=` exposes the same
mint to the e2e harness, so no TypeScript has to restate the grammar either.
The route's guard, and why it is narrower than the client-side cookie it
replaced, is in [Security](/docs/security#the-dev-backdoor-cannot-ship).

See [Getting started](/docs/getting-started) for the zero-account walkthrough
and [Testing](/docs/testing) for the Playwright harness.

## Mirror sync

Profile changes after sign-in arrive as webhooks: `POST /webhooks/clerk`
verifies the `svix-id` / `svix-timestamp` / `svix-signature` headers (Clerk
delivers via Svix) and upserts the local mirror — covered in
[Organizations](/docs/organizations). The mirror is what lets org-scoped
queries stay local SQL instead of API calls.
