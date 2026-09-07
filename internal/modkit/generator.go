package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// deletes the registry-owned outputs the selected graph no longer renders AND
// can prove it wrote. The delete is the half that was missing: nine emitters
// return no file at all once their input set empties, so removing a module
// used to leave an aggregate on disk that still compiled into the build and
// still referenced renderers the removal had deleted.
//
// An unrendered output with no provenance marker is authored bytes at a
// registry-owned name, and this stage refuses it. Engine.Plan already refuses
// before anything is written, so reaching here means the tree changed under a
// confirmed plan; refusing rolls the transaction back rather than deleting
// somebody's file inside it.
func (g RegistryGenerator) Generate(ctx context.Context, plan Plan) error {
	root := plan.Root
	files, err := g.render(ctx, plan)
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
		if err := atomicWrite(target, []byte(file.Content), false); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
	}

	unrendered, err := UnrenderedRegistryOutputs(root, rendered)
	if err != nil {
		return err
	}
	if len(unrendered.Unowned) != 0 {
		return UnownedGeneratedOutputError{Paths: unrendered.Paths(unrendered.Unowned)}
	}
	for _, output := range unrendered.Stale {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(output.Path))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete stale %s: %w", output.Path, err)
		}
	}
	return nil
}

// render returns the plan's aggregates, reusing the render Engine.Plan already
// performed when the plan carries it. Planning renders to classify unrendered
// outputs and to scan generated imports, so recomputing here would be the
// third identical render of one apply.
func (g RegistryGenerator) render(ctx context.Context, plan Plan) ([]GeneratedFile, error) {
	if len(plan.rendered) != 0 {
		return plan.rendered, nil
	}
	return g.Render(ctx, plan)
}

// GeneratedPaths reports every path this pipeline owns, so the transaction
// journal can snapshot them before generation and restore them on failure. It
// includes the stale outputs Generate is about to delete: a delete the journal
// did not snapshot would survive a rollback as a missing file.
func (g RegistryGenerator) GeneratedPaths(plan Plan) []string {
	files, err := g.render(context.Background(), plan)
	if err != nil {
		return nil
	}
	rendered := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		rendered[file.Path] = struct{}{}
		paths = append(paths, file.Path)
	}
	unrendered, err := UnrenderedRegistryOutputs(plan.Root, rendered)
	if err != nil {
		return nil
	}
	paths = append(paths, unrendered.Paths(unrendered.Stale)...)
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

// isNestedCheckout reports whether dir is the root of another checkout, which
// this project must never prune. Name matching is not enough: `.git` is a
// directory in a clone but a *file* in a linked worktree or a submodule, so a
// `git worktree add .worktrees/feature` inside the project root was walked as
// project tree and every one of its aggregates reported `generated_stale` —
// `sync --check` refused with 23 findings that named another checkout's files
// and could not be cleared by the `run ggg sync` the message prescribed.
func isNestedCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// UnrenderedOutput is one registry-owned output present in the tree that the
// supplied render does not produce.
type UnrenderedOutput struct {
	Path string
	// SHA256 digests the bytes on disk, so a planned delete reports what it
	// is about to remove.
	SHA256 string
}

// UnrenderedOutputs partitions the unrendered registry-owned outputs by
// provenance, which is the whole question: a path this pipeline owns is not
// the same as a file this pipeline wrote.
type UnrenderedOutputs struct {
	// Stale carries the provenance marker. This tool wrote it, nothing
	// renders it now, and `ggg sync` deletes it — as a named plan change, so
	// no byte leaves the tree unannounced.
	Stale []UnrenderedOutput
	// Unowned does not carry the marker. The bytes are somebody's authored
	// work sitting at a registry-owned name, so every command refuses and
	// names them instead of deleting them. A refusal costs one command; the
	// deletion cost work that may exist nowhere else.
	Unowned []UnrenderedOutput
}

// Paths projects one partition onto its sorted paths.
func (UnrenderedOutputs) Paths(outputs []UnrenderedOutput) []string {
	paths := make([]string, 0, len(outputs))
	for _, output := range outputs {
		paths = append(paths, output.Path)
	}
	sort.Strings(paths)
	return paths
}

// UnrenderedRegistryOutputs lists the registry-owned generated files present
// in the tree that the supplied render does not produce, split by whether the
// bytes claim this tool's authorship. `sync --check`, the planner and Generate
// all call it, so the file the gate reports is exactly the file the mutation
// deletes — or exactly the file every one of them refuses.
func UnrenderedRegistryOutputs(root string, rendered map[string]struct{}) (UnrenderedOutputs, error) {
	outputs := UnrenderedOutputs{Stale: make([]UnrenderedOutput, 0), Unowned: make([]UnrenderedOutput, 0)}
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
			if slashed == "." {
				return nil
			}
			if skippedSweepDirs[entry.Name()] || isNestedCheckout(full) {
				return fs.SkipDir
			}
			return nil
		}
		if !IsRegistryOwnedOutputPath(slashed) {
			return nil
		}
		if _, ok := rendered[slashed]; ok {
			return nil
		}
		content, readErr := os.ReadFile(full)
		if readErr != nil {
			return readErr
		}
		output := UnrenderedOutput{Path: slashed, SHA256: digestBytes(content)}
		if HasGeneratedOutputMarker(content) {
			outputs.Stale = append(outputs.Stale, output)
			return nil
		}
		outputs.Unowned = append(outputs.Unowned, output)
		return nil
	})
	if err != nil {
		return UnrenderedOutputs{}, fmt.Errorf("scan unrendered generated outputs: %w", err)
	}
	sort.Slice(outputs.Stale, func(i, j int) bool { return outputs.Stale[i].Path < outputs.Stale[j].Path })
	sort.Slice(outputs.Unowned, func(i, j int) bool { return outputs.Unowned[i].Path < outputs.Unowned[j].Path })
	return outputs, nil
}

// unrenderedOutputChanges turns the unrendered registry-owned outputs into the
// plan changes that announce their deletion, and refuses the ones this tool
// cannot prove it wrote.
//
// Every plan producer calls it, because every one of them reaches the same
// generation stage. A removal that classified only inside Apply reported exit
// 5 over a rolled-back tree where the other verbs reported exit 3 over an
// untouched one — the same question answered two ways, which is the disease
// this whole area keeps catching.
//
// The deletion is a plan Change and not a diagnostic because a change is
// strictly more: it is counted by the drift count, printed by the human
// renderer, carried in `changes[]`, folded into the run id so a changed
// removal set cannot reuse one, journalled and rolled back by Apply, and
// reported in Result.Deleted.
func unrenderedOutputChanges(root string, rendered []GeneratedFile, existing []Change) ([]Change, error) {
	paths := make(map[string]struct{}, len(rendered))
	for _, file := range rendered {
		paths[file.Path] = struct{}{}
	}
	unrendered, err := UnrenderedRegistryOutputs(root, paths)
	if err != nil {
		return nil, err
	}
	if len(unrendered.Unowned) != 0 {
		return nil, UnownedGeneratedOutputError{Paths: unrendered.Paths(unrendered.Unowned)}
	}
	declared := make(map[string]struct{}, len(existing))
	for _, change := range existing {
		declared[change.Path] = struct{}{}
	}
	changes := make([]Change, 0, len(unrendered.Stale))
	for _, output := range unrendered.Stale {
		// A plan never deletes the same path twice: a module's own removal
		// may already have named it.
		if _, ok := declared[output.Path]; ok {
			continue
		}
		changes = append(changes, Change{
			Path: output.Path, Kind: ChangeDelete, Class: DestinationGenerated, SHA256: output.SHA256,
		})
	}
	return changes, nil
}

// UnownedGeneratedOutputError refuses a tree holding authored bytes at a
// registry-owned name. It carries the declared refusal code because nothing
// was planned and nothing written, and it names only remedies that work:
// delete the file, move it to a name outside IsRegistryOwnedOutputPath, or —
// if ggg really did write it and the header was lost — restore the marker,
// which puts the file back in the swept class.
//
// Declaring the file in a module's `files` is NOT one of them, and the message
// says so, because the guidance that said it was survived a review. It cannot
// work for ANY path this error can print: the Unowned set is filtered by
// IsRegistryOwnedOutputPath, IsGeneratedOutputPath is that predicate ORed with
// the external-tool outputs, and reconcilePlannedState refuses every payload
// whose target satisfies it — "generated outputs are tool-owned and cannot be
// authored" — before any lock-state branching. So declaring trades this
// refusal for that one, at the same exit 3, with the file still on disk.
// `--claim` does not reach it either: a claim adopts a divergent file against
// a DECLARED target, and declaring is what is refused.
//
// snapshot_ownership.go had already written this down for the registry tree.
// It was re-derived wrongly anyway, which is why it now lives in the sentence
// the operator actually reads rather than only in a comment.
//
// The refusal itself is the same verdict ValidateRegistryTreeOwnership reaches
// one directory over for the same question about the registry tree. Two
// answers to one question is how a project ends up deleting work.
type UnownedGeneratedOutputError struct {
	Paths []string
}

func (e UnownedGeneratedOutputError) Error() string {
	return fmt.Sprintf(
		"%s sits at a name this pipeline generates but carries no %q marker, so ggg cannot prove it wrote it. "+
			"Delete it, rename it to a name this pipeline does not generate, or restore the marker in its header "+
			"if ggg wrote it, then re-run. Declaring it in a module's `files` does NOT clear this: a manifest "+
			"target at a generated path is refused as tool-owned",
		strings.Join(e.Paths, ", "), GeneratedOutputMarker)
}

// ExitCode reports the refusal code: nothing was planned and nothing written.
func (e UnownedGeneratedOutputError) ExitCode() int { return ExitRefusal }

// Diagnostics reports the coded, per-path findings a machine consumer reads
// off `diagnostics[]`, so the removal set is data rather than prose.
func (e UnownedGeneratedOutputError) Diagnostics() []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(e.Paths))
	for _, path := range e.Paths {
		diagnostics = append(diagnostics, Diagnostic{
			Code: "generated_unowned", Severity: "error", Path: path,
			Message: "a file at this generated name carries no ggg provenance marker; delete it, rename it off the generated name, or restore the marker if ggg wrote it. Declaring it in a module's files does NOT clear this: generated outputs are tool-owned and cannot be authored",
		})
	}
	return diagnostics
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
