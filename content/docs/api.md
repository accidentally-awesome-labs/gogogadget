---
title: Public API
description: Org-scoped Bearer tokens and the versioned JSON API at /api/v1.
section: Features
weight: 12
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
