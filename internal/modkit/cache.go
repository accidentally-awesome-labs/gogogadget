package modkit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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
		snapshotPath := filepath.Join(cacheDir, entry.Name(), "tree", RegistrySnapshotPath)
		if data, readErr := os.ReadFile(snapshotPath); readErr == nil {
			digest := sha256.Sum256(data)
			if _, ok := keep[hex.EncodeToString(digest[:])]; ok {
				continue
			}
		} else if !os.IsNotExist(readErr) && !errors.Is(readErr, fs.ErrNotExist) {
			return removed, readErr
		}
		if err := os.RemoveAll(filepath.Join(cacheDir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
