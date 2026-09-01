package modkit

import (
	"context"
	"fmt"
)

// Catalog resolves one registry commit and loads its validated catalog. It is
// the read-only entry point behind `ggg catalog` and `ggg info`: no planning, no
// project state, and no writes to the target tree.
func (e *Engine) Catalog(ctx context.Context, repository, ref string) (Catalog, string, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, "", err
	}
	if e.source == nil {
		return Catalog{}, "", fmt.Errorf("catalog: engine has no registry source")
	}
	snapshot, err := resolveSnapshot(ctx, e.source, ProjectRegistry{Repository: repository, Ref: ref, Source: "github"}, repository, ref)
	if err != nil {
		return Catalog{}, "", fmt.Errorf("resolve registry: %w", err)
	}
	catalog, err := LoadCatalog(snapshot.FS)
	if err != nil {
		return Catalog{}, "", fmt.Errorf("load catalog at %s: %w", snapshot.Commit, err)
	}
	return catalog, snapshot.Commit, nil
}
