# Task 5 implementation report

## Final state

Task 5 provider-neutral identity and billing cutover is complete at implementation commit `df24259`.

- Identity/billing constructor-free seams select identity-dev/identity-clerk and billing-local/billing-polar adapters explicitly. Hosted user, organization, and webhook checks are adapter-owned and fail closed.
- Generated identity-session is required by web wiring. Subjects map transactionally to opaque IDs, mutable email/slug collisions return `identity.ErrLinkRequired`, conflict losers are removed/reread, provider provenance drives subject-correct deletion and dev org switching, and first-sight `ADMIN_EMAIL` grants are tested. Dev/e2e seed fragments include all demo subject mappings.
- `ggg identity link` validates environment/adapter/target and audited subject-aware user/org links. Hosted organization links use the organization API, not the user API.
- Billing plan catalogs are injected and immutable with deep-copy accessors. Local billing has adapter-owned stable product IDs and unique subscription identities, durable current-product cancellation, subscription/period-scoped cancellation idempotency, confirm→cancel→reconfirm→cancel route coverage, authenticated `/app` GET screens plus CSRF-protected POST actions, neutral ledger events, and a terminating registered portal destination. Local/dev webhook payloads are rejected by the hosted webhook handler.
- Migration 0020 retains explicit named legacy FK/index/customer guards and neutral ledger providers; migration 0021 removes the Polar provider default. Existing fixture inserts declare provider explicitly. Identity/billing mapping tables, queries, lifecycle metadata, seeds, manifests, and generated outputs are owned.

## Exact verification

```text
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 148e0087ef8627ec663de2e752f5a4aa8acdcea127e1d997820fc61d7d680cbe
  update    lock       gogogadget.lock.json

$ go run ./cmd/seed -reset -registry e2e
reset database gogogadget
seeded 6 fragment(s)
imported 3 posts, 8 releases

$ go tool sqlc generate
# passed
$ go tool templ generate
(✓) Complete [ updates=0 duration=155.141209ms ]

$ go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web
# passed (unfiltered)

$ make check
# passed: registry drift, templ/sqlc generation, gofmt, sync check, go vet ./..., go test ./...
```

Implementation commit: `df24259`. The report is finalized in a subsequent report-only commit; the worktree is clean after commit.
