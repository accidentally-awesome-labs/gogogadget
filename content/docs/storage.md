---
title: File storage
description: The storage.Store seam — Cloudflare R2 or the zero-account DevStore, quotas, and the upload pattern.
section: Features
weight: 19
---

File uploads live behind one interface, `storage.Store` — the same seam shape
as `mail.Sender`. Handlers never import an S3 SDK; the two implementations
are `storage.R2Store` (Cloudflare R2, S3 API) and `storage.DevStore`
(local disk). Unconfigured → DevStore, so a fresh clone uploads and downloads
real files with zero accounts.

## The seam

```go
type Store interface {
    Put(ctx context.Context, key, contentType string, r io.Reader) (sizeBytes int64, err error)
    Serve(ctx context.Context, w http.ResponseWriter, key, filename, contentType string) error
    Delete(ctx context.Context, key string) error
}
```

- `Put` writes the object and returns its size (recorded in the `files` row).
- `Serve` delivers it: **R2 answers 303 to a presigned GET (15 min)** so the
  bytes stream from the provider, never through the app; DevStore streams
  from disk. Both always send `Content-Disposition: attachment` — user
  uploads are untrusted bytes and are never rendered inline (the global
  `X-Content-Type-Options: nosniff` backs this up).
- `Delete` removes the object; row deletion is best-effort followed by
  object deletion (an orphaned object is cheap; a 500 after the row is gone
  is worse).

Storage keys are `orgs/{clerk_org_id}/{32 hex random}{.ext}` — unguessable,
collision-free, and org-prefixed so per-org bucket lifecycle is trivial.
`storage.NewKey` builds them; the extension is the original filename's
trailing `[a-zA-Z0-9]{1,8}` when present.

## Configuration

```sh
STORAGE_R2_ACCOUNT_ID=        # Cloudflare account ID
STORAGE_R2_ACCESS_KEY_ID=     # R2 API token (Object Read & Write)
STORAGE_R2_SECRET_ACCESS_KEY=
STORAGE_R2_BUCKET=            # bucket name (create it in the R2 dashboard)
STORAGE_R2_ENDPOINT=          # optional: point at AWS S3 / MinIO instead
```

All four non-endpoint values set → R2. Any missing → DevStore
(`tmp/uploads/`, git-ignored). `cmd/server` logs which store is live at
boot. For S3/MinIO compat, set `STORAGE_R2_ENDPOINT` (e.g.
`http://localhost:9000`) and put the bucket name in `STORAGE_R2_BUCKET`.

## Quotas and the files table

`files` rows are org-scoped (`clerk_org_id`, cascade delete) with
`uploader_user_id`, `filename`, `content_type`, `size_bytes`, and the unique
`storage_key`. Plan truth carries `MaxStorageMB`: free 50, pro 5000 (5 GB),
team −1 (unlimited). The upload handler enforces
`SumBytesByOrg + incoming size > cap → 422` with the same upgrade-CTA
fragment the project limit uses. The global 10 MB request cap doubles as the
per-file limit (documented, deliberate).

The billing settings page shows a storage meter next to the projects meter —
both read the same plan truth.

## Add an upload to any resource

The `files` handler set is the canonical pattern:

1. Accept the field via `r.FormFile("name")` after
   `ParseMultipartForm(10 << 20)`.
2. Check the org's quota with `SumBytesByOrg` BEFORE writing anything.
3. `storage.NewKey(orgID, header.Filename)` → `store.Put` → `InsertFile`.
   On an insert failure, delete the object — never orphan one.
4. Download route = `GetFileByID` (org-scoped → cross-org ids 404) +
   `store.Serve`.
5. Row delete = `DeleteFile` + best-effort `store.Delete`, 200 empty body
   for the htmx row swap.

See [Extending GoGoGadget](/docs/extending) for the recipe index, and
[Background jobs](/docs/background-jobs) for the CSV export job, which
writes through the same seam.
