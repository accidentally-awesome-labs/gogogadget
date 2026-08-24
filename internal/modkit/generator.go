package modkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RegistryGenerator is the production generator pipeline. It renders the
// deterministic `*_registry_gen.*` aggregates from the installed lock and its
// resolved manifest graph. External generators (templ, sqlc, Tailwind) run after
// this from the Makefile, so this stage owns only registry-derived output and is
// a pure function of the plan — it never reads the target tree.
type RegistryGenerator struct {
	// ModulePath overrides the target Go import prefix. Empty uses the plan's,
	// which the planner resolved from the project's go.mod.
	ModulePath string
}

// Render returns every registry aggregate the plan implies, writing nothing.
func (g RegistryGenerator) Render(ctx context.Context, plan Plan) ([]GeneratedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock, graph, modulePath := g.inputs(plan)
	files, err := GenerateAll(ctx, modulePath, lock, graph)
	if err != nil {
		return nil, fmt.Errorf("render registry aggregates: %w", err)
	}
	return files, nil
}

// Generate renders every registry aggregate the plan implies and writes it.
func (g RegistryGenerator) Generate(ctx context.Context, plan Plan) error {
	root := plan.Root
	files, err := g.Render(ctx, plan)
	if err != nil {
		return err
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
func (g RegistryGenerator) GeneratedPaths(plan Plan) []string {
	files, err := g.Render(context.Background(), plan)
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

// inputs derives generation inputs from the plan. The plan carries the lock the
// transaction is about to write and the module path resolved from go.mod, so
// generation is a pure function of the plan and never reads the target tree.
func (g RegistryGenerator) inputs(plan Plan) (Lock, []Manifest, string) {
	modulePath := g.ModulePath
	if modulePath == "" {
		modulePath = plan.ModulePath
	}
	lock := plan.Lock

	graph := make([]Manifest, 0, len(lock.Modules))
	for _, module := range lock.Modules {
		if module.Reason == removalTombstoneReason {
			continue
		}
		graph = append(graph, module.Manifest)
	}
	return lock, graph, modulePath
}
