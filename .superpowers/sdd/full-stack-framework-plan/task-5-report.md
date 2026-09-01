# Task 5 implementation report

## Final state

Task 5 provider-neutral identity and billing cutover is complete at implementation commit `3612a6d` (report finalized in a subsequent documentation commit).

- Identity and billing are constructor-free provider seams. Explicit provider choices select development/test identity-dev and billing-local adapters or production identity-clerk and billing-polar adapters. Hosted credential and signature verification is adapter-owned and fails closed.
- `identity-session` is generated and required by web wiring. It transactionally maps provider subjects to opaque `usr_<32 hex>`/`org_<32 hex>` IDs, rereads conflicts, returns `identity.ErrLinkRequired` for mutable email/slug collisions, carries provider provenance, and grants configured `ADMIN_EMAIL` on first sight. Dev/e2e seed fixtures include identity subject and organization mappings; `/dev/login` and org switching use provider subjects.
- Account deletion resolves the active provider subject before adapter deletion. Identity webhooks resolve or create user/org mappings, resolve mapped membership IDs, and delete mapped domain rows. Hosted user and organization link verification use separate Clerk APIs.
- `ggg identity link --environment ENV --provider PROVIDER --subject SUBJECT (--user USER_ID|--org ORG_ID)` validates project selection and adapter target, verifies the subject-aware port, and writes an audited mapping transaction.
- Billing uses injected immutable plan catalogs with deep-copy accessors. Local billing assigns stable local product IDs, preserves product state across cancellation, authenticates `/app/billing/confirm` and `/app/billing/cancel` forms with nosurf tokens and POST actions, uses checkout-scoped idempotency for reactivation, rejects local/dev hosted-webhook mutation, and terminates portal navigation at the registered settings destination.
- Forward migrations preserve 0001–0020 and add `0021_provider_explicit.sql` to remove the Polar provider default. The 0020 guard uses explicit named legacy FK/index/customer invariants and accepts neutral local/dev webhook providers. All mapping tables, queries, data lifecycle declarations, manifests, generated outputs, and seed files are registry-owned.

## Exact verification

```text
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 613c3fe8a7ddbebc92c32f9c64d4dabd976401a766553103bd966687859fc8dd
  update    lock       gogogadget.lock.json

$ go tool sqlc generate
# passed
$ go tool templ generate
(✓) Complete [ updates=0 ... ]

$ go run ./cmd/seed -reset -registry e2e
reset database gogogadget
seeded 6 fragment(s)
imported 3 posts, 8 releases

$ go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web
# passed (unfiltered)

$ make check
# passed: registry drift, templ/sqlc generation, gofmt, sync check, go vet ./..., go test ./...
```

Final implementation commit: `3612a6d`. Final report commit is recorded by git after this content is written. The worktree is clean after commit.
