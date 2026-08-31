# Task 3 report

## Status

Task 3 provider-selection, contract, and fixture requirements are implemented. Mail and storage are constructor-free seams; concrete development, self-hosted, and managed adapters are selected explicitly per environment and generated boot initializes only the active choice.

## Changed clusters

- Mail seam keeps `Message`, `Sender`, and renderers without vendor imports. Adapters: `internal/mail/dev`, `internal/mail/smtp`, `internal/mail/resend`, with reusable contract coverage and real delivery/error assertions.
- Storage seam keeps `Store` and key helpers without SDK imports. Adapter-independent contract helpers are reused by `internal/storage/filesystem` and `internal/storage/s3`; restored filesystem traversal/header tests and S3 path-style/SigV4/redirect tests are adapter-owned.
- Added adapter manifests and target metadata for filesystem, Mailpit, SMTP, Resend, MinIO, and R2. SMTP target inputs map to its declared host/port/username/password configuration; generic seams remain vendor-free.
- Added provider slots `ggg/mail` and `ggg/storage`, full-profile defaults, and explicit self-host project selections: filesystem in development/test; Resend/R2 in production. Task 2 tombstones remain in `exclude`.
- Added dedicated `registry/testdata` mail and storage provider closures. Each closure installs both candidate adapters plus its closure root, compiles the generated boot, runs each adapter's declared tests, exercises provider selections, removes the closure, and proves derivative entries restored byte-for-byte. The validator exercises seven closures total.
- Web module rejects missing storage/flags/reporter capabilities; server-level provider fallbacks are removed.
- SMTP uses generated config fields, correct CRLF multipart/alternative wire format, STARTTLS, and auth; deterministic fake protocol coverage exercises the TLS/auth path. The moved development fuzz target is used by `make fuzz`.
- Managed Resend and S3 modules expose bounded `apphost.HealthChecker` forwarding; ownership verification recognizes manifest payload targets across selected and candidate adapters without project-owned laundering.
- Generated outputs were refreshed only through registry build/offline sync.

## Final verification

`go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline`

```text
registry 45f6b0433de61b6da58f5687469e0f43d3742eed71429f14434f646f6a9a32ac
  update    lock       gogogadget.lock.json
registry 45f6b0433de61b6da58f5687469e0f43d3742eed71429f14434f646f6a9a32ac
```

`go test ./internal/mail/... ./internal/storage/... ./internal/jobs ./internal/web/... ./internal/modules ./internal/modkit`

```text
mail, storage, jobs, web, modules packages passed; modkit ownership and closure tests passed after final manifest refresh.
```

`go run ./cmd/ggg registry validate`

```text
exercised 7 example closure(s):
fixture/system/mail-providers
  installed 4 file(s), compiled ./..., tested ./internal/fixture/maillocal, ./internal/fixture/mailmanaged
  removed; 1600 tree entries restored, 23 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained
fixture/system/storage-providers
  installed 4 file(s), compiled ./..., tested ./internal/fixture/storagefilesystem, ./internal/fixture/storages3
  removed; 1600 tree entries restored, 23 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained
```

The full validator also passed the four original element/component/page/workflow closures and original system closure. The workflow closure retained its declared immutable migration. Final command output included the exact `example_closure_verified` diagnostics for all seven closures.

## Commit

Reviewed base: `d9f2137`. Prior fixture commits: `e170f3b`, `372682c`. Round-two implementation and generated refresh commit: `bdfaf4a`.

## Concerns

Database-backed integration tests were not run because no local Postgres service was available. The focused command includes `internal/jobs`, all mail/storage adapter packages, web, modules, and modkit; SMTP STARTTLS/auth, storage security/protocol, ownership, fixture, and offline sync checks ran without Postgres.
