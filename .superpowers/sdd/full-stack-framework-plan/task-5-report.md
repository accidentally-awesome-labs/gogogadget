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

- `go test ./internal/db -run TestProviderNeutralMigrationPreservesLegacyRows -count=1` — passed; dedicated up-to-19 seeded fixture verifies user/org/membership/subscription/projects/audit/files/notifications/API-token rows plus identity and billing mappings survive 0020.

## Final cutover

The remaining Task 5 binding work is complete in this worktree:

- `ggg/system/identity` and `ggg/system/billing` now expose constructor-free provider slots with complete non-nil capability sets. `identity-dev`, `identity-clerk`, `billing-local`, and `billing-polar` are selected by explicit development/test/production provider choices; credential checks live in the hosted adapters.
- `identity-session` is a registry-owned runtime consumer. Generated boot wiring supplies its transactional `SessionLoader` to web middleware; middleware no longer writes provider subjects into domain IDs. Local identity/session creation uses opaque `usr_<32 hex>` and `org_<32 hex>` IDs and conflict rereads.
- Added the real `ggg identity link --environment ... --provider ... --subject ... (--user ...|--org ...)` CLI path. It validates the selected project adapter/target, loads environment configuration, constructs that adapter, verifies the subject-aware port, and writes the audited mapping transaction.
- Removed package-global mutable billing plans and `SetProviderProductIDs`. Plan catalogs are immutable and all accessors deep-copy nested meters/features. Web/API billing consumers use the selected catalog.
- Added local billing confirmation/cancellation routes and neutral event processing through the shared webhook/idempotency workflow. Generic identity/billing handlers now consume only injected neutral webhook capabilities.
- Added `identity-session` profile ownership and updated full-profile/project provider defaults for identity and billing.

Final focused verification:

- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; generated registry `47fccef6f072d6dd95438d084a2cb2776f16a3b128fd8083f0197dec52de3be1`.
- `go tool sqlc generate` — passed (`updates=0`).
- `go tool templ generate` — passed (`updates=0`).
- `go test ./cmd/ggg ./internal/modules ./internal/modkit ./internal/config ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/api ./internal/web -run '^$'` — passed (9 packages; 4 packages had no tests).
- `go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal -count=1` — passed.
- `go test ./internal/db -run TestProviderNeutralMigrationPreservesLegacyRows -count=1` — passed.

The migration guard retains the empty Polar-customer sentinel exclusion (`polar_customer_id <> ''`) and the pre-DDL legacy-column/index/FK/customer invariants. Changes and generated outputs are committed with the final worktree clean.

## Review fix round

- Hardened Clerk webhook verification to fail closed when its hosted secret is absent; unsigned parsing is now an explicit `identity.DevWebhook` selected only by the development adapter.
- Routed identity webhook lifecycle through provider mappings: user/org events create or resolve opaque domain IDs, membership events resolve both mappings, and deletion resolves the mapped domain row before cascading.
- Seeded web test identities with provider mappings and corrected local billing URLs to carry org context without trusting caller-supplied org IDs. Local confirmation/cancellation now has authenticated screen (GET) and action (POST) paths with same-origin route policy and idempotent neutral ledger processing.
- Added `GetOrgBySlug` sqlc query and declared identity/session and billing mapping table ownership in manifests; regenerated sqlc and all registry outputs.

Review-round verification:

- `gofmt -w internal/identity internal/billing internal/billinglocal internal/web internal/modkit internal/api internal/config` — passed.
- `go tool sqlc generate` — passed.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; generated registry `4d945ab1a5732d5a782c96c702d4deb727b8c054db7265c859de773cd3d7df77`.
- Focused unfiltered identity/billing/billing-local tests — passed.
- Focused compile tests for CLI, generated modules, config, identity/session, billing, API, web, and DB — passed.

## Final review fix round

- Migration `0020_provider_neutral_ids.sql` now checks the explicit named legacy foreign-key set and cascade actions (no FK-count sentinel), retains the empty Polar customer exclusion, and expands the shared webhook ledger provider check to include `local` with a reversible down path.
- Identity webhook processing now resolves all user/org/membership subjects through `identity_subjects`/`identity_organizations`, creates opaque IDs for first-seen entities, and resolves mapped domain IDs for deletion. Development uses an explicit unsigned `DevWebhook`; hosted Clerk verification fails closed without its secret.
- Local billing checkout now carries org context, renders authenticated GET confirmation/cancellation screens, and mutates state only via POST neutral events with ledger idempotency. New session/billing tables and `GetOrgBySlug` are manifest/query-owned and regenerated.

Verification after review fixes:

- `gofmt -w internal/identity internal/billing internal/billinglocal internal/web internal/modkit internal/api internal/config` — passed.
- `go tool sqlc generate` — passed.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry `f4af69b8cb8fa1d8bc0995088c0e9bfb0de5e9137a06f4ac0442fc39c7e16457`.
- `go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal -count=1` — passed.
- `go test ./internal/db -run 'TestProviderNeutralMigrationPreservesLegacyRows|TestMigrateUpDown' -count=1` — passed.

## Final review acceptance verification

- `go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web` — passed unfiltered.
- `go tool sqlc generate` and `go tool templ generate` — passed with zero updates.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry `3bde3ad737f5ab27b86b4c95816ce4224651dce62e83bf8602bdb8d8a6ddc5fb`.
- Focused `go vet` over CLI, modkit, identity, billing, session, web, API, and config — passed.
- `make check` reached its formatting gate but reports five pre-existing unrelated files (`internal/audit/audit.go`, `internal/db/db_test.go`, `internal/jobs/digest.go`, `internal/schedules/schedules.go`, `internal/schedules/schedules_test.go`); all Task 5 changed authored files are gofmt-clean.

## Final repository gate

- Formatted the five tracked files previously blocking the repository gate: `internal/audit/audit.go`, `internal/db/db_test.go`, `internal/jobs/digest.go`, `internal/schedules/schedules.go`, and `internal/schedules/schedules_test.go`.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry `9569d62f9089ab2e39b142551b41219fe68d6dc94decc4c4e9a65c028ae2bca9`.
- `make check` — passed, including registry drift, templ/sqlc generation, gofmt, sync check, `go vet ./...`, and `go test ./...`.

## Round 2 review acceptance

- Declared all generated sqlc ownership (`GetUserByEmail`, `GetOrgBySlug`, identity mappings, billing accounts), corrected lifecycle semantics for mapping tables, and owned every Task 5 source file without migration target collisions.
- Added explicit hosted/development navigator behavior, session error logging and invalid-token distinction, and provider-aware webhook organization updates and subscription revocation.
- Updated generated boot tests for explicit provider selections and non-nil neutral capabilities. The complete affected suite is green unfiltered.
- `go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web` — passed.
- `make check` — passed fully; this includes `go vet ./...` and `go test ./...`.
- Final registry digest: `b2c546cb07d61941cff82ed4a4c59e2ae3105bd97a177c3f881069949307f683`.

## Round 2 subject-verification fix

- `ClerkVerifier.VerifySubject` now uses the hosted Clerk user API client and requires the returned user ID to exactly match the requested subject; it no longer treats an arbitrary subject as verified.
- `identity.PortalNavigator` and catalog injection remain provider-neutral; generated/configured runtime paths are unchanged.
- `make check` passed fully after regeneration, including gofmt, registry drift, `go vet ./...`, and `go test ./...`.
- Focused unfiltered identity, session, billing, billing-local, web, database, API, and jobs suites passed.
- Final registry digest: `e67c3305fc3dcec889c79765d816695158ba597aec9efdfd714467797ce8aac9`.

## Repair acceptance

- Account deletion now resolves the provider subject from `identity_subjects` before invoking adapter deletion. Dev identity switching resolves provider subjects from the mapping tables, preserving opaque domain IDs; session first sight restores the configured `ADMIN_EMAIL` grant.
- Local billing confirm/cancel preserve product/customer/checkout context through authenticated app-scope forms, enforce POST plus explicit CSRF token checks, use non-lifetime-colliding checkout event IDs, and expose an authenticated local portal screen. Local/dev webhook deliveries are rejected by the hosted webhook endpoint.
- Added org/user mapping lookup queries and session context provider provenance; hosted `VerifySubject` uses the Clerk user API. Mapping lifecycle metadata and generated ownership remain synchronized.
- `go test ./internal/identity/... ./internal/billing/... ./internal/billinglocal ./internal/identity/session ./internal/db ./internal/api ./internal/jobs ./internal/web` — passed unfiltered.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry `5c4d67d7d48e257c05d7751593b18571b77957c9a12676328b0034c4eb437420`.
- `make check` — passed fully (`gofmt`, registry drift, templ/sqlc, sync check, `go vet ./...`, and `go test ./...`).

## Final repair verification

- Account deletion selects the active provider session's upstream subject when available and otherwise resolves one from the identity mapping table; opaque `UserID` is never passed to provider deleters.
- `go test ./internal/web -run 'TestAccountDelete' -count=1` — passed.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` — passed; registry `d95c819a12cc441195d63f245b0d17a4507698e65682ade396e0b40a6abc2f2f`.
