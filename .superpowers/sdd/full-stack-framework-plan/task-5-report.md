# Task 5 implementation report

## Final state

Task 5 provider-neutral identity and billing cutover is complete at implementation commit `ff224cc`.

- Identity and billing are constructor-free provider seams selected by explicit development/test/production adapter choices. Hosted credential, user-subject, organization-subject, and webhook verification stay adapter-owned and fail closed.
- Generated `identity-session` is required by web wiring. Provider subjects map transactionally to opaque `usr_<32 hex>`/`org_<32 hex>` IDs, with email/slug collision `ErrLinkRequired`, conflict rereads, provider provenance, ADMIN_EMAIL first-sight grants, and dev/e2e identity seed mappings. Account deletion and dev org switching resolve provider subjects from mappings.
- `ggg identity link --environment ENV --provider PROVIDER --subject SUBJECT (--user USER_ID|--org ORG_ID)` validates selected adapters and writes audited mappings. Clerk user/org linking uses separate subject-aware APIs.
- Billing uses injected immutable catalogs with deep-copy accessors. Local billing assigns stable unique subscription IDs, preserves current product IDs during cancellation (including after process restart via the subscription row), scopes cancel idempotency to subscription/period, and supports confirm→cancel→reconfirm→cancel. Confirm/cancel screens are authenticated `/app` GET/POST routes with nosurf tokens; the registered portal GET destination terminates at settings. Local/dev webhook payloads cannot mutate state through the hosted webhook endpoint.
- Forward migrations retain 0001–0020 and add 0021 to remove the Polar provider default. 0020 has explicit named legacy FK/index/customer guards and accepts neutral ledger providers. New tables, queries, lifecycle metadata, seed fixtures, manifests, and generated outputs are owned and regenerated.

## Exact verification

```text
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 953f352878374f0c84f3f7137bf74d122c4465021a1013cb27439e36fd327767
  update    lock       gogogadget.lock.json

$ go run ./cmd/seed -reset -registry e2e
reset database gogogadget
seeded 6 fragment(s)
imported 3 posts, 8 releases

$ go tool sqlc generate
# passed
$ go tool templ generate
(✓) Complete [ updates=0 duration=152.363458ms ]

$ go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web
# passed (unfiltered)

$ go test ./internal/billinglocal ./internal/web -run 'TestLocalBillingSubscriptionIDsAreUniqueAcrossOrganizations|TestLocalBillingConfirmCancelReactivates' -count=1
# passed

$ make check
# passed: registry drift, templ/sqlc generation, gofmt, sync check, go vet ./..., go test ./...
```

Final implementation commit: `ff224cc`. The worktree is clean after commit.
