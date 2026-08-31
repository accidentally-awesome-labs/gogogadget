# Task 3 report

## Status

Implemented the mail and storage provider-selection slice. The project now has constructor-free mail/storage seams, independently selectable adapter modules, explicit environment choices, and generated boot wiring that chooses exactly one adapter per slot for each APP_ENV.

## Changed clusters

- Mail seam: `internal/mail/mail.go` retains `Message`, `Sender`, and all renderers without vendor SDK imports. Removed seam constructor and credential selector. Moved filesystem delivery to `internal/mail/dev`, Resend delivery to `internal/mail/resend`, and added self-hosted SMTP delivery to `internal/mail/smtp`.
- Storage seam: `internal/storage/storage.go` retains `Store` and key helpers without SDK imports. Moved filesystem implementation to `internal/storage/filesystem` and S3/R2 implementation to `internal/storage/s3`; removed credential-based seam constructor.
- Adapter manifests: added `ggg/system/mail-dev`, `ggg/system/mail-smtp`, `ggg/system/mail-resend`, `ggg/system/storage-filesystem`, and `ggg/system/storage-s3`. Each declares its own package claim, lifecycle constructor, capability/type, provider slot, targets, dependencies, and adapter-owned environment keys. Mailpit and MinIO local service metadata is target-owned.
- Seam manifests: `ggg/system/mail` and `ggg/system/storage` now declare provider slots and only seam files. Vendor dependencies/environment declarations moved to adapter manifests. Generic S3 endpoint is `STORAGE_S3_ENDPOINT`/`StorageS3Endpoint`.
- Project/profile: `gogogadget.json` and `registry/profiles/full.json` explicitly select filesystem mail/storage for development and test, Resend/R2 for production while preserving Task 2 lock tombstones.
- Web wiring: `web.NewModule` rejects missing storage, flags, and observability capabilities; server-level storage/flags/reporter fallbacks were removed. Existing direct tests now supply explicit filesystem/flag/reporter capabilities.
- Tests/callsites: migrated jobs/web/module type assertions and constructors to adapter packages. Added dev/Resend/SMTP adapter contract tests and retained seam renderer/storage behavior tests.
- Generator: removed duplicate target-required errors from generated config production-key checks; active target requirements now report once through parsing while production-only declarations retain their production check.

## Commands and exact results

1. Baseline focused test:

`go test ./internal/mail/... ./internal/storage/... ./internal/web/...`

Result: passed before Task 3 edits.

2. Required generation after manifest edits:

`go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline`

Final result:

```text
registry 219fe3652ed5385e15601132278846dfb12f66a6ae54c6780694f39946348980
  update    lock       gogogadget.lock.json
```

3. Focused adapter/web/generated verification:

`go test ./internal/mail/... ./internal/storage/... ./internal/web/... ./internal/modules ./internal/modkit`

Result: all listed packages passed. Mail, storage, generated boot, and modkit checks passed; the web integration package completed in approximately 52 seconds without local Postgres.

4. Ownership-specific verification:

`git add -A && go test ./internal/modkit -run TestEveryTrackedSourceFileHasAnOwner`

Result: passed.

## Commits

- Task 3 implementation commit: `ed302ef` (`feat: split mail and storage adapters`).

## Concerns

- SMTP construction reads SMTP host/port/user/password from process environment because the current root project does not install the SMTP candidate adapter and therefore its fields are intentionally absent from the root generated Config. A derivative selecting `ggg/system/mail-smtp` receives those fields from generated config declarations; the adapter's runtime path remains isolated and does not affect the selected root graph.
- The focused web suite uses its existing integration helper and takes approximately 50 seconds when no local Postgres is available; it still passed.
- Generated outputs were only changed through `registry build` and offline `sync`; bootstrap/config outputs were not hand-edited.
