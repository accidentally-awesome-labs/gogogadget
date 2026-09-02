package modkit

import (
	"context"
	"fmt"
)

// ProjectSource resolves every registry kind a project may declare. Directory
// registries are always rooted at the project and therefore cannot escape it;
// GitHub registries retain the signed archive/cache behavior of GitHubSource.
type ProjectSource struct {
	Root   string
	GitHub GitHubSource
}

func (s ProjectSource) Resolve(ctx context.Context, registry ProjectRegistry) (Snapshot, error) {
	switch registry.Source {
	case "directory":
		return (DirectorySource{Root: s.Root}).Resolve(ctx, registry)
	case "github":
		return s.GitHub.Resolve(ctx, registry)
	default:
		return Snapshot{}, fmt.Errorf("unsupported registry source %q", registry.Source)
	}
}
