# Task 3 report

## Status

Mail and storage provider-selection slice implemented. Mail/storage are constructor-free seams; concrete development, self-hosted, and managed adapters are selected explicitly per environment and generated boot initializes only the active choice.

## Changed clusters

- Mail seam keeps `Message`, `Sender`, and renderers without vendor imports. Adapters: `internal/mail/dev`, `internal/mail/smtp`, `internal/mail/resend`.
- Storage seam keeps `Store` and key helpers without SDK imports. Adapters: `internal/storage/filesystem`, `internal/storage/s3`.
- Added adapter manifests and target metadata for filesystem, Mailpit, SMTP, Resend, MinIO, and R2. Vendor dependencies and managed credentials are adapter-owned; generic endpoint is `STORAGE_S3_ENDPOINT`.
- Added provider slots `ggg/mail` and `ggg/storage`, full-profile defaults, and explicit self-host project selections: filesystem in development/test; Resend/R2 in production. Task 2 tombstones remain in `exclude`.
- Web module rejects missing storage/flags/reporter capabilities; server-level provider fallbacks are removed.
- Migrated jobs/web/module tests to adapter packages, restored jobs janitor test's live query handle, removed tautological DevStore fallback assertion, and added deterministic SMTP protocol coverage.
- SMTP now uses generated `Config` fields, emits proper CRLF multipart/alternative text+HTML, supports STARTTLS when advertised, and authenticates after TLS.
- Managed Resend and S3 modules expose bounded `apphost.HealthChecker` forwarding; ownership verification recognizes manifest payload targets across selected and candidate adapters without project-owned laundering.
- Generated outputs were refreshed only through registry build/offline sync.

## Verification

`go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline`

```text
registry b31c1095ca1ce7c26957ccae08c76c4738bbbfd658b466e77f0331f167549424
  update    lock       gogogadget.lock.json
```

`go test ./internal/mail/... ./internal/storage/... ./internal/jobs ./internal/web/... ./internal/modules ./internal/modkit`

Result: all packages passed, including the SMTP fake protocol test, jobs tests, generated boot tests, and modkit tests. Web integration package completed in approximately 54 seconds without local Postgres.

## Commit

Final review-fix commit is recorded by the parent after this report is staged.

## Concerns

- Registry testdata still uses the existing example-closure harness; dedicated mail/storage fixture closure expansion remains a follow-up if not supplied by the parent integration commit.
- Storage seam contract tests are external-package tests and exercise both adapters; an install-only seam derivative should omit those adapter-construction test references in a future fixture split.
