package modkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func RefreshManifestDigests(root string) ([]string, error) {
	refreshed := make([]string, 0)
	for _, include := range catalogIncludes {
		if include.kind == CatalogProfile {
			continue
		}
		dir := filepath.Join(root, "registry", "modules", string(include.kind))
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			rel := "registry/modules/" + string(include.kind) + "/" + entry.Name() + "/module.json"
			changed, err := refreshManifestDocument(root, rel)
			if err != nil {
				return nil, err
			}
			if changed {
				refreshed = append(refreshed, rel)
			}
		}
	}
	sort.Strings(refreshed)
	return refreshed, nil
}

// refreshManifestDocument rewrites one manifest's payload digests. It decodes
// into the typed model so a malformed manifest is rejected rather than silently
// rewritten, and it re-encodes with the same canonical shape the loader expects.
func refreshManifestDocument(root, rel string) (bool, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	original, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var document ModuleDocument
	if err := decodeStrict(original, &document); err != nil {
		return false, fmt.Errorf("%s: %w", rel, err)
	}

	changed := false
	for i, file := range document.Module.Files {
		// A generated payload's digest is read by nothing: readPlannedPayloads
		// returns early on FileClassGenerated, before the verification that
		// raises "payload ... sha256 mismatch", because the registry does not
		// distribute bytes the build produces. Recording one rewrote manifests
		// on every build for no consumer, and a stale value sitting there reads
		// as authoritative when nothing will ever check it. Cleared once, then
		// left alone.
		if file.Class == FileClassGenerated {
			if file.SHA256 != "" {
				document.Module.Files[i].SHA256 = ""
				changed = true
			}
			continue
		}
		digest, err := payloadDigest(root, file.Source)
		if err != nil {
			return false, fmt.Errorf("%s: %w", rel, err)
		}
		if digest != file.SHA256 {
			document.Module.Files[i].SHA256 = digest
			changed = true
		}
	}
	for i, migration := range document.Module.Migrations {
		digest, err := payloadDigest(root, migration.Source)
		if err != nil {
			return false, fmt.Errorf("%s: %w", rel, err)
		}
		if digest != migration.SHA256 {
			document.Module.Migrations[i].SHA256 = digest
			changed = true
		}
	}
	if !changed {
		return false, nil
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return false, err
	}
	if err := atomicWrite(full, append(encoded, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// payloadDigest hashes one manifest payload from the registry tree.
func payloadDigest(root, source string) (string, error) {
	if err := validateSafePath(source); err != nil {
		return "", fmt.Errorf("payload %q: %w", source, err)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
	if err != nil {
		return "", fmt.Errorf("read payload %s: %w", source, err)
	}
	return digestBytes(content), nil
}

// BuildRegistryIndexes rewrites each kind index from the documents actually
// present under registry/. It scans the tree rather than reading the indexes it
// writes: deriving the index from itself would make a newly authored module
// permanently invisible. Item order is sorted, so output is byte-stable.
func BuildRegistryIndexes(root string) (written []string, discovered []string, err error) {
	byKind := make(map[CatalogKind][]string)
	for _, include := range catalogIncludes {
		if include.kind == CatalogProfile {
			continue
		}
		dir := filepath.Join(root, "registry", "modules", string(include.kind))
		entries, readErr := os.ReadDir(dir)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", dir, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			item := "registry/modules/" + string(include.kind) + "/" + entry.Name() + "/module.json"
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(item))); statErr != nil {
				continue
			}
			byKind[include.kind] = append(byKind[include.kind], item)
			discovered = append(discovered, string(include.kind)+"/"+entry.Name())
		}
	}

	profileDir := filepath.Join(root, "registry", "profiles")
	profileEntries, readErr := os.ReadDir(profileDir)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("scan %s: %w", profileDir, readErr)
	}
	for _, entry := range profileEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		byKind[CatalogProfile] = append(byKind[CatalogProfile], "registry/profiles/"+entry.Name())
		discovered = append(discovered, "profile/"+strings.TrimSuffix(entry.Name(), ".json"))
	}

	for _, include := range catalogIncludes {
		items := byKind[include.kind]
		if items == nil {
			items = []string{}
		}
		sort.Strings(items)
		data, marshalErr := json.MarshalIndent(CatalogIndex{
			Schema: 2, Kind: include.kind, Items: items,
		}, "", "  ")
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		target := filepath.Join(root, filepath.FromSlash(include.path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
			return nil, nil, err
		}
		written = append(written, include.path)
	}
	sort.Strings(discovered)
	return written, discovered, nil
}
