package modkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// RegistryGenerator is the production generator pipeline. It renders the
// deterministic `*_registry_gen.*` aggregates from the installed lock and its
// resolved manifest graph. External generators (templ, sqlc, Tailwind) run after
// this from the Makefile, so this stage owns only registry-derived output and
// stays hermetic — it reads the project's own lock and nothing else.
type RegistryGenerator struct {
	// ModulePath is the target Go import prefix. Empty means read it from go.mod.
	ModulePath string
}

// Generate renders every registry aggregate for the project at root.
func (g RegistryGenerator) Generate(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, graph, modulePath, err := g.inputs(root)
	if err != nil {
		return err
	}
	if lock == nil {
		// No lock yet: nothing is installed, so there is nothing to render. This
		// is the first-sync case, where Apply writes the lock after generation.
		return nil
	}
	files, err := GenerateAll(ctx, modulePath, *lock, graph)
	if err != nil {
		return fmt.Errorf("render registry aggregates: %w", err)
	}
	for _, file := range files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(file.Path), err)
		}
		if err := atomicWrite(target, []byte(file.Content)); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	return nil
}

// GeneratedPaths reports every path this pipeline owns, so the transaction
// journal can snapshot them before generation and restore them on failure.
func (g RegistryGenerator) GeneratedPaths(root string) []string {
	lock, graph, modulePath, err := g.inputs(root)
	if err != nil || lock == nil {
		return nil
	}
	files, err := GenerateAll(context.Background(), modulePath, *lock, graph)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)
	return paths
}

// inputs reads the lock and reconstructs the manifest graph it embeds. The lock
// carries a full manifest snapshot per module, so generation never needs the
// registry source and works offline.
func (g RegistryGenerator) inputs(root string) (*Lock, []Manifest, string, error) {
	data, err := os.ReadFile(filepath.Join(root, LockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, "", nil
	}
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", LockFileName, err)
	}
	lock, err := ParseLock(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse %s: %w", LockFileName, err)
	}

	modulePath := g.ModulePath
	if modulePath == "" {
		goMod, readErr := os.ReadFile(filepath.Join(root, "go.mod"))
		if readErr != nil {
			return nil, nil, "", fmt.Errorf("read go.mod: %w", readErr)
		}
		modulePath, err = parseModulePath(goMod)
		if err != nil {
			return nil, nil, "", fmt.Errorf("parse go.mod: %w", err)
		}
	}

	graph := make([]Manifest, 0, len(lock.Modules))
	for _, module := range lock.Modules {
		if module.Reason == removalTombstoneReason {
			continue
		}
		graph = append(graph, module.Manifest)
	}
	return &lock, graph, modulePath, nil
}
