package modkit

import (
	"context"
	"io/fs"
)

// Snapshot is an immutable registry tree identified by its source commit. When
// the publisher supplies a signed snapshot, SnapshotSHA256 is the digest of
// registry.snapshot.json and Registry contains the verified root metadata.
type Snapshot struct {
	Commit         string
	SnapshotSHA256 string
	CacheKey       string
	Registry       RegistryRoot
	FS             fs.FS
}

// SourceResolver resolves one project registry to an immutable snapshot.
type SourceResolver interface {
	Resolve(context.Context, ProjectRegistry) (Snapshot, error)
}

// Source is the source resolver used by the planning engine.
type Source = SourceResolver
