---
title: Public API
description: Org-scoped Bearer tokens and the versioned JSON API at /api/v1.
section: Features
weight: 13
---

The public API is a second transport over the same rules as the HTML app —
same sqlc queries, same validation, same plan limits. Never parallel logic.

## Tokens

- Format: `ggg_` + 32 random bytes (base64url), from `api.GenerateToken()`.
- Storage: only the **SHA-256 hex** of the token is stored
  (`api_tokens.token_hash`). The plaintext is shown **once** in the reveal
  card at creation and cannot be recovered later.
- Ownership: tokens belong to an **organization**, not a user.
- Management: **Settings → API** (`/app/settings/api`) — create (name +
  scope), list (last used, expiry), revoke. Any org member may create and
  revoke org tokens (boilerplate default; tightening to `org:admin` is one
  middleware line).

Use a token as a Bearer credential:

```sh
curl -H "Authorization: Bearer ggg_…" http://localhost:8080/api/v1/projects
```

`RequireAPIToken(scope, next)` verifies the hash, rejects revoked and expired
tokens, sets the org on the request context, and touches `last_used_at`
asynchronously — never on the request path. Because API requests are
cookieless Bearer auth, `/api/*` is CSRF-exempt in the middleware chain (see
[Security](/docs/security)).

## Scopes

`read` and `write`. **Write satisfies read**: a write-scoped token can call
every endpoint. A read-scoped token calling a write endpoint gets `403`.

## Error shape

Every error is the same JSON object, with the HTTP status to match:

```json
{"error": {"code": "plan_limit", "message": "The Free plan allows 3 projects. Upgrade to create more."}}
```

| Status | Code             | When                                                            |
|--------|------------------|-----------------------------------------------------------------|
| 400    | `invalid_json`   | Request body is not valid JSON                                  |
| 401    | `unauthorized`   | Missing/malformed header, unknown or revoked token, expired     |
| 402    | `plan_limit`     | The org is at its plan's project limit                          |
| 403    | `forbidden`      | The token's scope cannot perform the operation                  |
| 404    | `not_found`      | Unknown `/api/` route (JSON, never the HTML 404)                |
| 422    | `validation_error` | Input failed validation                                       |
| 500    | `internal_error` | Server-side failure                                             |

## Endpoints

### `GET /api/v1/projects` (scope: read)

Lists the token org's active projects, newest first.

| Param    | Default | Notes                    |
|----------|---------|--------------------------|
| `limit`  | 50      | over 100 falls back to 50 |
| `offset` | 0       | negative falls back to 0 |

```json
{"projects": [ … ], "limit": 50, "offset": 0}
```

### `POST /api/v1/projects` (scope: write)

Body: `{"name": "Launch checklist"}`. The name rule is shared with the HTML
form (`api.ValidateProjectName`): required, ≤ 80 characters after trimming.
On success: `201` plus the project JSON, and a `project.created` audit event
with `{"via": "api"}`. At the plan limit: `402` with code `plan_limit` —
limits come from the same plan truth as the app (see [Billing](/docs/billing)).

## Versioning

`/api/v1` is **additive-only**: new fields and new endpoints are fine;
renames, removals, and type changes are not. A breaking change ships as
`/api/v2` — v1 keeps working.

## Adding an endpoint

1. Write the handler in `internal/api/`, reusing the sqlc queries and the
   app's validation — do not fork rules.
2. Register it in `internal/web/routes.go` wrapped in
   `apiMW.RequireAPIToken("read" /* or "write" */, …)`.
3. Emit errors with `api.WriteError(w, status, code, message)`.

The full recipe is in [Extending](/docs/extending); handler behavior gets
integration tests — see [Testing](/docs/testing).

## Idempotency

A POST that times out leaves the client unable to tell whether the work
happened: retry and risk a duplicate, or don't and risk losing the write.
Send an `Idempotency-Key` and the retry is safe.

```sh
curl -X POST "$API/api/v1/projects" \
  -H "Authorization: Bearer ggg_…" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"name":"Only once"}'
```

| Situation | Result |
|---|---|
| No `Idempotency-Key` | Nothing changes — the feature is opt-in, two POSTs are two creates |
| New key | Executes, stores the outcome, returns it |
| Same key, same body | Replays the stored status and body, with `Idempotency-Replayed: true` |
| Same key, **different** body or endpoint | `409 idempotency_conflict` — the operation does **not** run |
| Same key, first request still running | `409 idempotency_in_progress` — retry shortly |
| Key longer than 255 chars | `400 invalid_idempotency_key` |

Details that matter in production:

- **The claim is the lock.** The key row is inserted *before* the handler
  runs, and the primary key on `(clerk_org_id, key)` is what makes two
  concurrent retries resolve to exactly one execution — no advisory locks, no
  application-level mutex that dies with the process.
- **Keys are org-scoped, not token-scoped.** A client that rotates
  credentials mid-retry still deduplicates.
- **5xx releases the claim.** A server error says nothing about whether the
  work should happen, so the key is freed rather than pinned to a failure —
  otherwise a transient blip would poison that key for its whole lifetime,
  which is the exact situation the client is retrying to escape. 4xx *is* a
  real outcome and is stored, so a retry replays the same validation error.
- **Retention is 24 hours**, swept by the worker's janitor pass
  (`jobs.IdempotencyRetention`). Long enough for any sane retry schedule,
  short enough that `idempotency_keys` stays a cache rather than an archive.

## Rate limits

Each token carries its own budget — `API_RATE_LIMIT_RPM`, default **60
req/min, burst 120** — spent across every `/api/v1` endpoint, so a client
cannot multiply its allowance by spreading calls over routes. Exceeding it
returns `429` with code `rate_limited` and a `Retry-After` header:

```json
{"error":{"code":"rate_limited","message":"This API token is over its request budget. …"}}
```

Budgets are per **token**, not per IP: one customer's traffic cannot spend
another's allowance, and issuing a second token gives a second budget. The
per-IP shield described in [Security](/docs/security) still applies on top.

## Pagination

`GET /api/v1/projects` supports two modes; both return the same envelope.

**Cursor (preferred).** Follow `next_cursor` until it is `null`:

```sh
curl -H "Authorization: Bearer ggg_…" "$API/api/v1/projects?limit=50"
# {"projects":[…],"limit":50,"offset":0,"next_cursor":"MTc3…MC40Mg"}
curl -H "Authorization: Bearer ggg_…" "$API/api/v1/projects?limit=50&cursor=MTc3…MC40Mg"
```

Keyset paging over `(created_at DESC, id DESC)` — the `id` tiebreak makes the
order **total**, which offset paging quietly lacked when two projects shared a
timestamp. A cursor names a row rather than a position, so a project created
while a client walks pages cannot shift rows across a page boundary, and deep
pages cost the same as the first (`projects_org_idx` serves the row-value
comparison; no rows are skipped server-side).

Cursors are **opaque** — base64url today, subject to change. Echo them back
verbatim; parsing or constructing one is unsupported, and a malformed cursor
is a `400 invalid_cursor` rather than a silent restart at page one.

**Offset (legacy).** `?limit=&offset=` still works and is still tested, but it
repeats or drops rows when the set changes mid-walk. Every response — offset
included — carries `next_cursor`, so a client can switch to cursors mid-stream
without a flag day.

## OpenAPI

The contract is published as OpenAPI 3.1 at **`GET /api/v1/openapi.yaml`** —
unauthenticated, so tooling can read it before a token exists:

```sh
curl -s http://localhost:8080/api/v1/openapi.yaml | head
npx @redocly/cli preview-docs http://localhost:8080/api/v1/openapi.yaml
```

The spec is hand-written (`internal/api/openapi.yaml`, `go:embed`-ed into the
binary, so a deployed build always serves the contract it shipped with) and
kept honest by tests rather than discipline:

- `TestOpenAPISpecMatchesRegisteredRoutes` scans the `/api/v1` patterns in
  `routes.go` and compares the set against the spec's paths — adding a route
  without documenting it (or documenting a path that isn't routed) fails CI.
- `TestOpenAPIProjectShapeMatchesHandler` drives the real endpoint and
  compares the payload keys against the documented `Project` schema, so the
  spec cannot drift from what the handler actually emits.

Responses are typed DTOs, not raw sqlc rows: `projectResponse` pins the
public fields, which is why the internal `search_tsv` FTS column never
appears in a payload and why adding a database column can't silently change
the API.
