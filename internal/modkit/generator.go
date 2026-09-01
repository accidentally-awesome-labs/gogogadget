package modkit

import (
	"context"
	"fmt"
	"io/fs"
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

// Generate renders every registry aggregate the plan implies, writes it, and
// deletes any registry-owned output the selected graph no longer renders. The
// delete is the half that was missing: nine emitters return no file at all once
// their input set empties, so removing a module used to leave an aggregate on
// disk that still compiled into the build and still referenced renderers the
// removal had deleted. `sync --check` reported that as `generated_stale` with
// the instruction "run ggg sync", which could not clear it.
func (g RegistryGenerator) Generate(ctx context.Context, plan Plan) error {
	root := plan.Root
	files, err := g.Render(ctx, plan)
	if err != nil {
		return err
	}
	rendered := make(map[string]struct{}, len(files))
	for _, file := range files {
		rendered[file.Path] = struct{}{}
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(file.Path), err)
		}
		if err := atomicWrite(target, []byte(file.Content)); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}

	stale, err := StaleRegistryOutputs(root, rendered)
	if err != nil {
		return err
	}
	for _, path := range stale {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete stale %s: %w", path, err)
		}
	}
	return nil
}

// GeneratedPaths reports every path this pipeline owns, so the transaction
// journal can snapshot them before generation and restore them on failure. It
// includes the stale outputs Generate is about to delete: a delete the journal
// did not snapshot would survive a rollback as a missing file.
func (g RegistryGenerator) GeneratedPaths(plan Plan) []string {
	files, err := g.Render(context.Background(), plan)
	if err != nil {
		return nil
	}
	rendered := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		rendered[file.Path] = struct{}{}
		paths = append(paths, file.Path)
	}
	stale, err := StaleRegistryOutputs(plan.Root, rendered)
	if err != nil {
		return nil
	}
	paths = append(paths, stale...)
	sort.Strings(paths)
	return paths
}

// skippedSweepDirs are directory names the stale sweep never descends into.
// The sweep deletes, so it stays inside the project's own source: `tmp/` holds
// staged conflict candidates that are legitimate copies of generated files, and
// a vendored or nested checkout is not this project's tree to prune.
var skippedSweepDirs = map[string]bool{
	".git": true, "node_modules": true, "tmp": true, "bin": true, "test-results": true,
}

// StaleRegistryOutputs lists the registry-owned generated files present in the
// tree that the supplied render does not produce. Both `sync --check` and
// Generate call this, so the file the gate reports is exactly the file the
// mutation deletes.
func StaleRegistryOutputs(root string, rendered map[string]struct{}) ([]string, error) {
	stale := make([]string, 0)
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return relErr
		}
		slashed := filepath.ToSlash(rel)
		if entry.IsDir() {
			if slashed != "." && skippedSweepDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !IsRegistryOwnedOutputPath(slashed) {
			return nil
		}
		if _, ok := rendered[slashed]; !ok {
			stale = append(stale, slashed)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan stale generated outputs: %w", err)
	}
	sort.Strings(stale)
	return stale, nil
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
		if module.Reason == TombstoneReason {
			continue
		}
		graph = append(graph, module.Manifest)
	}
	return lock, graph, modulePath
}
