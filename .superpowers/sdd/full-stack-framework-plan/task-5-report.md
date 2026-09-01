# Task 5 implementation report

## Final state

Task 5 provider-neutral identity and billing cutover is complete at commit `608bd51`.

- `ggg/system/identity` and `ggg/system/billing` are constructor-free seams with complete typed capabilities. Development/test and production choices select identity-dev/identity-clerk and billing-local/billing-polar adapters through provider selections. Hosted credential and signature checks are adapter-owned; hosted Clerk verification fails closed and subject linking verifies users and organizations through their respective APIs.
- `identity-session` is a generated runtime capability consumed by web middleware. Provider subjects are mapped transactionally to opaque `usr_<32 hex>`/`org_<32 hex>` IDs, with conflict rereads, `ErrLinkRequired` for mutable email/slug collisions, provider provenance in request context, and ADMIN_EMAIL first-sight role grants. Dev/e2e seed fixtures include provider mappings, and dev login/org switching preserve provider subjects.
- Account deletion resolves the active provider subject from session provenance or identity mappings before invoking the adapter deleter. Identity webhooks resolve/insert mapping rows for users, organizations, and memberships; deletion resolves mapped domain IDs.
- `ggg identity link --environment ENV --provider PROVIDER --subject SUBJECT (--user USER_ID|--org ORG_ID)` validates the selected adapter/target, uses subject-aware user/org verification, and writes an audited mapping transaction.
- Billing uses injected immutable `PlanCatalog` values with deep-copy accessors; no mutable global Plans or SetProviderProductIDs remain. Local billing has stable local product IDs, authenticated `/app/billing/confirm` and `/app/billing/cancel` screen/action routes with nosurf tokens, current-product cancellation, non-colliding checkout event IDs, neutral idempotency, reactivation coverage, and a terminating registered portal destination. Local/dev webhook events cannot mutate state through the hosted webhook endpoint.
- Migration `0020_provider_neutral_ids.sql` retains named legacy schema/customer guards and allows neutral local/dev ledger providers. Forward migration `0021_provider_explicit.sql` removes the legacy Polar provider default; all authored fixtures explicitly provide provider values. Identity/billing mapping tables, generated queries, lifecycle metadata, and manifests are owned and regenerated.

## Exact final verification

```text
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 222720c49a83c4f2a97be4941b40ab0c475d32a55058dec73ec3b7c8ebbfc223
  update    lock       gogogadget.lock.json

$ go tool sqlc generate
# passed
$ go tool templ generate
(✓) Complete [ updates=0 ... ]

$ go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web
# passed (unfiltered)

$ make check
# passed: registry drift, templ/sqlc generation, gofmt, sync check, go vet ./..., go test ./...
```

The final worktree is clean. The final registry digest is `222720c49a83c4f2a97be4941b40ab0c475d32a55058dec73ec3b7c8ebbfc223`.
