package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

type resolvedRegistry struct {
	config   ProjectRegistry
	snapshot Snapshot
}

func mergeResolvedCatalogs(ctx context.Context, sources []resolvedRegistry) (Catalog, error) {
	merged := Catalog{ModuleSources: map[string]fs.FS{}, ModuleRegistries: map[string]string{}, ModuleCanonical: map[string]string{}}
	moduleSeen := map[string]string{}
	profileSeen := map[string]string{}
	canonicalSeen := map[string]string{}
	for _, resolved := range sources {
		if err := ctx.Err(); err != nil {
			return Catalog{}, err
		}
		catalog, err := LoadCatalog(resolved.snapshot.FS)
		if err != nil {
			return Catalog{}, fmt.Errorf("load registry %s: %w", resolved.config.Namespace, err)
		}
		if resolved.config.Namespace != "" && catalog.Namespace != resolved.config.Namespace {
			return Catalog{}, fmt.Errorf("registry namespace %q does not match requested namespace %q", catalog.Namespace, resolved.config.Namespace)
		}
		if previous := canonicalSeen[catalog.CanonicalModule]; previous != "" && previous != catalog.Namespace {
			return Catalog{}, fmt.Errorf("canonical module %q is claimed by registries %q and %q", catalog.CanonicalModule, previous, catalog.Namespace)
		}
		canonicalSeen[catalog.CanonicalModule] = catalog.Namespace
		for previous, previousNamespace := range canonicalSeen {
			if previousNamespace != catalog.Namespace && (strings.HasPrefix(catalog.CanonicalModule, previous+"/") || strings.HasPrefix(previous, catalog.CanonicalModule+"/")) {
				return Catalog{}, fmt.Errorf("canonical module prefix %q collides with %q", catalog.CanonicalModule, previous)
			}
		}
		merged.CanonicalModules = append(merged.CanonicalModules, catalog.CanonicalModule)
		for _, module := range catalog.Modules {
			if previous := moduleSeen[module.ID]; previous != "" {
				return Catalog{}, fmt.Errorf("duplicate scoped module id %q in registries %q and %q", module.ID, previous, catalog.Namespace)
			}
			moduleSeen[module.ID] = catalog.Namespace
			merged.Modules = append(merged.Modules, module)
			merged.ModuleRegistries[module.ID] = catalog.ModuleRegistries[module.ID]
			merged.ModuleSources[module.ID] = catalog.ModuleSources[module.ID]
			merged.ModuleCanonical[module.ID] = catalog.ModuleCanonical[module.ID]
		}
		for _, profile := range catalog.Profiles {
			if previous := profileSeen[profile.ID]; previous != "" {
				return Catalog{}, fmt.Errorf("duplicate scoped profile id %q in registries %q and %q", profile.ID, previous, catalog.Namespace)
			}
			profileSeen[profile.ID] = catalog.Namespace
			merged.Profiles = append(merged.Profiles, profile)
		}
	}
	sort.Strings(merged.CanonicalModules)
	sort.Slice(merged.Modules, func(i, j int) bool { return merged.Modules[i].ID < merged.Modules[j].ID })
	sort.Slice(merged.Profiles, func(i, j int) bool { return merged.Profiles[i].ID < merged.Profiles[j].ID })
	return merged, nil
}

func canonicalPrefixes(catalog Catalog) []string {
	prefixes := append([]string(nil), catalog.CanonicalModules...)
	if len(prefixes) == 0 && catalog.CanonicalModule != "" {
		prefixes = []string{catalog.CanonicalModule}
	}
	sort.Strings(prefixes)
	return prefixes
}
