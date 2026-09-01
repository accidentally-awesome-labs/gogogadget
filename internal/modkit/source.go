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

// SourceResolver is the external-source contract. Implementations must not
// persist credentials in ProjectRegistry or Snapshot metadata.
type SourceResolver interface {
	Resolve(context.Context, ProjectRegistry) (Snapshot, error)
}

// Source is retained as an intentionally loose compatibility boundary for
// test and embedding implementations written before schema-v2. New resolvers
// should implement SourceResolver; resolveSnapshot accepts both signatures.
type Source interface{}

func resolveSnapshot(ctx context.Context, source any, registry ProjectRegistry, legacyRepository, legacyRef string) (Snapshot, error) {
	if resolver, ok := source.(interface {
		Resolve(context.Context, ...any) (Snapshot, error)
	}); ok {
		return resolver.Resolve(ctx, registry)
	}
	if resolver, ok := source.(interface {
		Resolve(context.Context, ProjectRegistry) (Snapshot, error)
	}); ok {
		return resolver.Resolve(ctx, registry)
	}
	if resolver, ok := source.(interface {
		Resolve(context.Context, string, string) (Snapshot, error)
	}); ok {
		return resolver.Resolve(ctx, legacyRepository, legacyRef)
	}
	return Snapshot{}, &sourceResolutionError{message: "registry source does not implement Resolve(context.Context, ProjectRegistry)"}
}

type sourceResolutionError struct{ message string }

func (e *sourceResolutionError) Error() string { return e.message }
