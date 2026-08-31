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
registry d35603f7647e0c597eb452aa6fd50492e8adb22d6bd537479cd6a0b325ba1a40
  update    lock       gogogadget.lock.json
registry d35603f7647e0c597eb452aa6fd50492e8adb22d6bd537479cd6a0b325ba1a40
```

`go test ./internal/mail/... ./internal/storage/...`

```text
ok  	github.com/gogogadget/gogogadget/internal/mail	(cached)
?   	github.com/gogogadget/gogogadget/internal/mail/contract	[no test files]
ok  	github.com/gogogadget/gogogadget/internal/mail/dev	(cached)
ok  	github.com/gogogadget/gogogadget/internal/mail/resend	(cached)
ok  	github.com/gogogadget/gogogadget/internal/mail/smtp	(cached)
ok  	github.com/gogogadget/gogogadget/internal/storage	(cached)
?   	github.com/gogogadget/gogogadget/internal/storage/contract	[no test files]
ok  	github.com/gogogadget/gogogadget/internal/storage/filesystem	(cached)
ok  	github.com/gogogadget/gogogadget/internal/storage/s3	0.195s
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

Reviewed base: `d9f2137`. Prior fixture commits: `e170f3b`, `372682c`. Round-two implementation: `bdfaf4a`; shared-contract/config follow-up: `cdf8226`.

## Concerns

Database-backed integration tests were not run because no local Postgres service was available. The final focused command above covers the changed mail/storage adapter packages; prior final verification covered jobs, web, modules, and modkit before this contract-only follow-up.
