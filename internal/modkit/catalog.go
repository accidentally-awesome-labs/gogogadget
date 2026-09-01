package modkit

import (
	"context"
	"fmt"
)

// Catalog resolves every configured registry through the typed source resolver
// and merges their globally namespaced catalogs without precedence.
func (e *Engine) Catalog(ctx context.Context, registries []ProjectRegistry) (Catalog, string, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, "", err
	}
	if e.source == nil {
		return Catalog{}, "", fmt.Errorf("catalog: engine has no registry source")
	}
	if len(registries) == 0 {
		return Catalog{}, "", fmt.Errorf("catalog: no registries configured")
	}
	resolved := make([]resolvedRegistry, 0, len(registries))
	for _, registry := range registries {
		snapshot, err := e.source.Resolve(ctx, registry)
		if err != nil {
			return Catalog{}, "", fmt.Errorf("resolve registry %s: %w", registry.Namespace, err)
		}
		if snapshot.FS == nil || snapshot.Commit == "" {
			return Catalog{}, "", fmt.Errorf("resolved registry %s is incomplete", registry.Namespace)
		}
		resolved = append(resolved, resolvedRegistry{config: registry, snapshot: snapshot})
	}
	catalog, err := mergeResolvedCatalogs(ctx, resolved)
	if err != nil {
		return Catalog{}, "", err
	}
	commit, _, _ := registryProvenance(resolved, catalog, catalog.Modules)
	return catalog, commit, nil
}
