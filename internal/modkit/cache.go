package modkit

import (
	"fmt"
	"os"
	"path/filepath"
)

// PruneRegistryCache removes only cache entries not referenced by lock
// snapshots. It is explicit by design; normal resolution never deletes a
// lock-referenced or otherwise cached snapshot.
func PruneRegistryCache(cacheDir string, referenced []string) (int, error) {
	if cacheDir == "" {
		return 0, fmt.Errorf("registry cache directory is required")
	}
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	keep := map[string]struct{}{}
	for _, ref := range referenced {
		if ref != "" {
			keep[filepath.Base(ref)] = struct{}{}
		}
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) == 0 {
			continue
		}
		if _, ok := keep[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(cacheDir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
