package modkit

import (
	"context"
	"io/fs"
)

// Snapshot is an immutable registry tree identified by its source commit.
type Snapshot struct {
	Commit string
	FS     fs.FS
}

// Source resolves a repository reference to a registry snapshot.
type Source interface {
	Resolve(context.Context, string, string) (Snapshot, error)
}
