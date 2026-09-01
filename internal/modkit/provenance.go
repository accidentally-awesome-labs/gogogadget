package modkit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

func findLockedModule(modules []LockedModule, id string) (LockedModule, bool) {
	for _, module := range modules {
		if module.ID == id {
			return module, true
		}
	}
	return LockedModule{}, false
}

func registryProvenance(sources []resolvedRegistry, catalog Catalog, modules []Manifest) (string, []LockedRegistry, []LockedSnapshot) {
	registries := make([]LockedRegistry, 0, len(sources))
	snapshots := make([]LockedSnapshot, 0, len(sources))
	byNamespace := map[string]Snapshot{}
	for _, source := range sources {
		fingerprint := ""
		if source.config.PublicKey != "" {
			fingerprint, _ = RegistryKeyFingerprint(source.config.PublicKey)
		}
		canonical := source.snapshot.Registry.CanonicalModule
		if canonical == "" {
			canonical = catalog.CanonicalModule
		}
		registries = append(registries, LockedRegistry{Namespace: source.config.Namespace, Source: source.config.Source, RequestedRef: source.config.Ref, CanonicalModule: canonical, KeyFingerprint: fingerprint})
		digest := source.snapshot.SnapshotSHA256
		if digest == "" {
			digest = source.snapshot.Commit
		}
		snapshots = append(snapshots, LockedSnapshot{Namespace: source.config.Namespace, Commit: source.snapshot.Commit, SnapshotSHA256: digest, CacheKey: digest})
		byNamespace[source.config.Namespace] = source.snapshot
	}
	sort.Slice(registries, func(i, j int) bool { return registries[i].Namespace < registries[j].Namespace })
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Namespace < snapshots[j].Namespace })
	allLegacy := true
	for _, source := range sources {
		if source.snapshot.SnapshotSHA256 != "" {
			allLegacy = false
			break
		}
	}
	if allLegacy && len(sources) == 1 {
		return sources[0].snapshot.Commit, registries, snapshots
	}
	pairs := make([]string, 0, len(modules))
	for _, module := range modules {
		namespace := catalog.ModuleRegistries[module.ID]
		digest := ""
		if snapshot, ok := byNamespace[namespace]; ok {
			digest = snapshot.SnapshotSHA256
			if digest == "" {
				digest = snapshot.Commit
			}
		}
		pairs = append(pairs, fmt.Sprintf("%s\x00%s", module.ID, digest))
	}
	sort.Strings(pairs)
	hash := sha256.New()
	for _, pair := range pairs {
		_, _ = hash.Write([]byte(pair))
		_, _ = hash.Write([]byte("\n"))
	}
	return hex.EncodeToString(hash.Sum(nil)), registries, snapshots
}
