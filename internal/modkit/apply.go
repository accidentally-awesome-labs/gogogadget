package modkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Generator runs the fixed post-mutation generation pipeline (templ, sqlc,
// Tailwind, and registry-owned outputs) once per apply. The engine treats any
// error as a transaction failure and restores pre-run bytes, including any
// generator-owned paths the pipeline dirtied before failing.
type Generator interface {
	// Render returns every generated output the plan implies without writing
	// anything, so `sync --check` can compare bytes against the tree.
	Render(ctx context.Context, plan Plan) ([]GeneratedFile, error)
	// Generate renders every generated output the plan implies and writes it. It
	// takes the plan, not the root, because the first sync has no lock on disk
	// yet: Apply writes the lock last, so a generator that read the tree would
	// emit nothing exactly when a fresh install needs the aggregates.
	Generate(ctx context.Context, plan Plan) error
	// GeneratedPaths returns every generated path the pipeline owns for this
	// plan, relative to the plan root, so the journal can snapshot them before
	// generation.
	GeneratedPaths(plan Plan) []string
}

// Result is the outcome of applying one plan.
type Result struct {
	Exit       int      `json:"exit"`
	RolledBack bool     `json:"rolled_back"`
	Written    []string `json:"written"`
	Deleted    []string `json:"deleted"`
}

// journalEntry records one pre-run filesystem state so a failed apply can be
// restored exactly.
type journalEntry struct {
	path    string
	existed bool
	content []byte
}

// Apply executes a plan through a transaction journal: snapshot every path
// the plan can touch plus every generator-owned path, stage and atomically
// write authored/generated outputs, run the generator pipeline once, and write
// the lock last. Any failure restores exact pre-run bytes and reports exit 5.
func (e *Engine) Apply(ctx context.Context, plan Plan) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if e == nil {
		return Result{}, fmt.Errorf("apply requires an engine")
	}
	if plan.Operation.DryRun {
		return Result{Exit: 0}, nil
	}
	if e.generator == nil {
		return Result{}, fmt.Errorf("apply requires a generator pipeline")
	}

	journal := make(map[string]*journalEntry)
	order := make([]string, 0, len(plan.Changes)+1)
	snapshot := func(path string) (*journalEntry, error) {
		if entry, ok := journal[path]; ok {
			return entry, nil
		}
		full := filepath.Join(plan.Root, filepath.FromSlash(path))
		data, err := os.ReadFile(full)
		if err == nil {
			entry := &journalEntry{path: path, existed: true, content: data}
			journal[path] = entry
			order = append(order, path)
			return entry, nil
		}
		if os.IsNotExist(err) {
			entry := &journalEntry{path: path, existed: false}
			journal[path] = entry
			order = append(order, path)
			return entry, nil
		}
		return nil, fmt.Errorf("snapshot %s: %w", path, err)
	}
	rollback := func() {
		// Restore newest-first so created paths are removed before updated
		// paths regain their exact prior bytes.
		for i := len(order) - 1; i >= 0; i-- {
			entry := journal[order[i]]
			full := filepath.Join(plan.Root, filepath.FromSlash(entry.path))
			if entry.existed {
				_ = os.WriteFile(full, entry.content, 0o644)
			} else {
				_ = os.Remove(full)
			}
		}
	}

	// 1. Snapshot every touched path plus every generator-owned path before
	//    any write.
	for _, change := range plan.Changes {
		if _, err := snapshot(change.Path); err != nil {
			return Result{}, err
		}
	}
	if _, err := snapshot("gogogadget.lock.json"); err != nil {
		return Result{}, err
	}
	for _, path := range e.generator.GeneratedPaths(plan) {
		if _, err := snapshot(path); err != nil {
			return Result{}, err
		}
	}

	// 2. Stage and atomically apply authored/generated changes (lock excluded;
	//    it is written last after generation succeeds).
	written := make([]string, 0, len(plan.Changes))
	deleted := make([]string, 0)
	apply := func() error {
		for _, change := range plan.Changes {
			if change.Kind == ChangeUnchanged || change.Class == DestinationLock {
				continue
			}
			full := filepath.Join(plan.Root, filepath.FromSlash(change.Path))
			switch change.Kind {
			case ChangeCreate, ChangeUpdate:
				if err := atomicWrite(full, change.Content); err != nil {
					return fmt.Errorf("write %s: %w", change.Path, err)
				}
				written = append(written, change.Path)
			case ChangeDelete:
				if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("delete %s: %w", change.Path, err)
				}
				deleted = append(deleted, change.Path)
			}
		}
		return nil
	}
	if err := apply(); err != nil {
		rollback()
		return Result{Exit: 5, RolledBack: true}, err
	}

	// 3. Run the fixed generator pipeline once. Any failure restores the tree.
	if err := e.generator.Generate(ctx, plan); err != nil {
		rollback()
		return Result{Exit: 5, RolledBack: true}, fmt.Errorf("generation failed, restored pre-run bytes: %w", err)
	}

	// 4. Write the lock last, only after generation succeeds.
	lockContent, err := MarshalLock(plan.Lock)
	if err != nil {
		rollback()
		return Result{Exit: 5, RolledBack: true}, fmt.Errorf("marshal lock: %w", err)
	}
	if err := atomicWrite(filepath.Join(plan.Root, "gogogadget.lock.json"), lockContent); err != nil {
		rollback()
		return Result{Exit: 5, RolledBack: true}, fmt.Errorf("write lock: %w", err)
	}

	sort.Strings(written)
	sort.Strings(deleted)
	return Result{Exit: 0, Written: written, Deleted: deleted}, nil
}

// atomicWrite writes content to a sibling temp file and renames it into place
// so a crash cannot leave a half-written target.
func atomicWrite(full string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".ggg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return err
	}
	tmpName = ""
	return nil
}
