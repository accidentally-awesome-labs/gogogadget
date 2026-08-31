# Task 3 report

## Status

Task 3 provider-selection, contract, and fixture requirements are implemented. Mail and storage are constructor-free seams; concrete development, self-hosted, and managed adapters are selected explicitly per environment and generated boot initializes only the active choice.

## Changed clusters

- Mail seam keeps `Message`, `Sender`, and renderers without vendor imports. Adapters: `internal/mail/dev`, `internal/mail/smtp`, `internal/mail/resend`, with one reusable contract harness and real delivery/error assertions.
- Storage seam keeps `Store` and key helpers without SDK imports. Adapter-independent contract helpers are reused by `internal/storage/filesystem` and `internal/storage/s3`; adapter tests are owned by their adapter manifests.
- Added adapter manifests and target metadata for filesystem, Mailpit, SMTP, Resend, MinIO, and R2. Vendor dependencies and managed credentials are adapter-owned; generic endpoint is `STORAGE_S3_ENDPOINT`.
- Added provider slots `ggg/mail` and `ggg/storage`, full-profile defaults, and explicit self-host project selections: filesystem in development/test; Resend/R2 in production. Task 2 tombstones remain in `exclude`.
- Added dedicated `registry/testdata` mail and storage provider closures. Each closure installs both candidate adapters plus its closure root, compiles the generated boot, runs each adapter's declared tests, switches provider selections, removes the closure, and proves 1,597 derivative entries restored byte-for-byte. The validator now exercises seven closures total.
- Web module rejects missing storage/flags/reporter capabilities; server-level provider fallbacks are removed.
- Restored the jobs janitor test's live query handle and retained deterministic SMTP protocol coverage. SMTP uses generated config fields, correct CRLF multipart/alternative wire format, STARTTLS, and auth.
- Managed Resend and S3 modules expose bounded `apphost.HealthChecker` forwarding; ownership verification recognizes manifest payload targets across selected and candidate adapters without project-owned laundering.
- Generated outputs were refreshed only through registry build/offline sync.

## Verification

`go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline`

```text
registry 7c8a3f648abfb02569438ad96789cdc2fc4f546d7830008068fe3fce19c39767
  update    lock       gogogadget.lock.json
```

`go test ./internal/mail/... ./internal/storage/... ./internal/jobs ./internal/web/... ./internal/modules ./internal/modkit`

```text
go test: 13 packages ok, 2 no tests
```

`go run ./cmd/ggg registry validate`

```text
exercised 7 example closure(s)
fixture/system/mail-providers: installed 4 file(s), compiled ./..., tested ./internal/fixture/maillocal and ./internal/fixture/mailmanaged, restored 1597 tree entries byte for byte
fixture/system/storage-providers: installed 4 file(s), compiled ./..., tested ./internal/fixture/storagefilesystem and ./internal/fixture/storages3, restored 1597 tree entries byte for byte
```

The full validator output also reports the four original element/component/page/workflow closures and the original system closure, each compiled and restored successfully; the workflow closure retained its declared immutable migration.

## Commit

Implementation changes are based on reviewed commit `d9f2137`; the final implementation commit is recorded immediately after this report is committed.

## Concerns

Database-backed integration tests were not required for this slice and were not run because no local Postgres service was available. The focused package command above includes `internal/jobs`, all mail/storage adapter packages, web, modules, and modkit.
