# Task 5 implementation report

## Status

Implemented and committed the provider-neutral identity/billing slice in commit `b1cbf7d` (`feat: decouple identity and billing provider state`). The worktree was clean after commit.

## Exact verification commands and results

- `go tool sqlc generate` — passed after generating against a temporary final-schema representation of the forward migration; generated `internal/db/sqlc` was produced by sqlc and not hand-edited.
- `go tool templ generate` — passed (`Complete [updates=4]`).
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry digest `b3a29ba8f489f22558c286847f3d2506e5c7583c98610a77a3e17ebce6f741e3`.
- `go test ./internal/identity ./internal/identitysession ./internal/identity/session ./internal/billing ./internal/billinglocal ./internal/db ./internal/web -run 'TestFakeVerifier|TestMigrateUpDown|TestRoundtripEveryTable|TestPolarWebhookReplay|TestNewModule' -count=1` — passed.
- `go test ./internal/api ./internal/audit ./internal/billing ./internal/billinglocal ./internal/db ./internal/flags ./internal/identity/... ./internal/jobs ./internal/notify ./internal/schedules ./internal/usage ./internal/webhooks ./internal/web -run '^$'` — all affected backend packages compiled (12 packages passed; 2 had no tests).
- `go test ./internal/db -run 'TestMigrateUpDown|TestRoundtripEveryTable' -count=1` — passed, including the forward 0020 migration and generic round-trip queries.

## Changed areas

- Added transactional `internal/db/migrations/0020_provider_neutral_ids.sql` with pre-DDL schema/index/FK/customer invariants, generic domain key renames, provider subscription columns, identity mapping tables, billing account backfill, and reversible test-harness down path.
- Updated authored SQL queries to generic `user_id`/`org_id`/`provider_*`; added identity and billing-account mapping queries; regenerated sqlc output.
- Migrated backend callers, tests, seed data, OpenAPI metadata, manifests, lock, and generated registry outputs away from provider-named domain identifiers.
- Added identity `ProviderClaims`, `Navigator`, `Webhook`, neutral event types, adapter-owned Clerk webhook verification, and transactional `internal/identity/session` loader/linker services with opaque `usr_`/`org_` IDs and `ErrLinkRequired` ambiguity handling.
- Added billing `PlanCatalog` immutable API, neutral `SubscriptionEvent`/`BillingWebhook`, account mapping checks, and Polar adapter verification; billing processing writes provider-neutral subscription/customer fields.
- Added deterministic `internal/billinglocal` adapter with in-app checkout/portal URLs and neutral confirmation/cancellation events.
- Changed generic web webhook handling to dispatch through provider adapters rather than importing signature libraries directly.

## Concerns / follow-up

- The repository's existing integration tests still encode pre-Task-5 assumptions in several full web flows (for example, direct seeded provider-shaped identities and legacy local billing expectations); focused migration, identity, billing, and web tests selected above pass. The full project suite was intentionally not run per assignment constraints.
- The existing generated registry model remains broad and registry regeneration touched many dependent generated files because generic identifier names affect manifests, lock digests, OpenAPI, and generated metadata.
- Provider-specific Clerk/Polar implementation code remains in its existing adapter-oriented packages for compatibility; generic callers now consume neutral contracts and events.

## Review follow-up

Review findings C2/C3/C4/C6/I2/I3 were addressed after the initial report: the migration excludes the empty legacy customer sentinel, session loading returns lookup errors and cleans loser rows on mapping races, web wiring can use the transactional session loader, identity/billing modules expose non-nil webhook/navigation capabilities, and catalog results deep-copy nested slices.

Follow-up commands:

- `gofmt -w internal/identity/session/*.go internal/identity/*.go internal/billing/*.go internal/billinglocal/*.go internal/web/auth.go internal/web/server.go` — passed.
- `go test ./internal/identity ./internal/identity/session ./internal/billing ./internal/billinglocal ./internal/web -run '^$'` — passed.
- `go test ./internal/db -run 'TestMigrateUpDown|TestRoundtripEveryTable' -count=1` — passed.

Current implementation commit remains `b1cbf7d`; report/generated commits are `77c0bf4` and `b96107d`.

## Follow-up review work

Additional review fixes are committed in `f18dbea` and `e876877`: neutral webhook capabilities are injected into web, subject-aware linking is required, session mapping handles lookup/race errors, local billing now has a registry adapter manifest and contract tests, and immutable catalog deep-copy tests cover nested slices.

Latest verification:

- `gofmt -w internal/billing/catalog_test.go internal/billinglocal/local_test.go internal/identity/session/*.go internal/web/*.go` — passed.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed (registry `d3ba886a6f6ae01971577aaf7e4e2a982c049fabf1d172ad530cb46c402b7677`).
- `go test ./internal/billing ./internal/billinglocal ./internal/identity ./internal/identity/session ./internal/web ./internal/db -run 'TestLocalBillingContract|TestPlanCatalogDeepCopies|TestFakeVerifier|TestMigrateUpDown|TestRoundtripEveryTable' -count=1` — passed.

Remaining known gaps are the full constructor-free provider-slot manifest cutover, removal of legacy global plan compatibility API, a wired top-level `ggg identity link` command, and a dedicated pre-0020 fixture migration integration test.
