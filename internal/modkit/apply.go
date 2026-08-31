package modkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Generator runs the registry-owned generation stage once per apply: it renders
// the `*_registry_gen.*` aggregates and the other outputs
// IsRegistryOwnedOutputPath names, and deletes the ones the selected graph no
// longer owns. templ, sqlc, and Tailwind run afterwards, from the Makefile, and
// are outside this transaction. The engine treats any error here as a
// transaction failure and restores pre-run bytes and modes, including any
// generator-owned paths the stage dirtied before failing.
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
	// generation. It includes the stale outputs Generate will delete.
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
// restored exactly — bytes and mode. Mode matters because os.CreateTemp makes
// the staged file 0600: without capturing and restoring the original, a
// rolled-back file would come back owner-only.
type journalEntry struct {
	path    string
	existed bool
	content []byte
	mode    fs.FileMode
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
	// preexistingDir answers "did this directory exist before the run" for every
	// ancestor of every snapshotted path, so rollback can remove the directories
	// MkdirAll created without touching one the operator already had.
	preexistingDir := make(map[string]bool)
	noteDirs := func(path string) {
		for dir := slashParent(path); dir != ""; dir = slashParent(dir) {
			if _, seen := preexistingDir[dir]; seen {
				return
			}
			_, statErr := os.Stat(filepath.Join(plan.Root, filepath.FromSlash(dir)))
			preexistingDir[dir] = statErr == nil
		}
	}
	snapshot := func(path string) (*journalEntry, error) {
		if entry, ok := journal[path]; ok {
			return entry, nil
		}
		noteDirs(path)
		full := filepath.Join(plan.Root, filepath.FromSlash(path))
		data, err := os.ReadFile(full)
		if err == nil {
			mode := fs.FileMode(defaultFileMode)
			if info, statErr := os.Stat(full); statErr == nil {
				mode = info.Mode().Perm()
			}
			entry := &journalEntry{path: path, existed: true, content: data, mode: mode}
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
	// rollback restores every journalled path and reports what it could not
	// restore. Silently swallowing a restore failure is the worst outcome here:
	// disk-full is the likeliest cause of the generation failure that triggered
	// the rollback, it is just as likely to defeat the restore, and exit 5's one
	// job is to tell the operator whether the tree is trustworthy.
	rollback := func() error {
		failures := make([]string, 0)
		// Restore newest-first so created paths are removed before updated
		// paths regain their exact prior bytes.
		for i := len(order) - 1; i >= 0; i-- {
			entry := journal[order[i]]
			full := filepath.Join(plan.Root, filepath.FromSlash(entry.path))
			if entry.existed {
				if err := os.WriteFile(full, entry.content, entry.mode); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", entry.path, err))
					continue
				}
				// WriteFile does not chmod a file that already exists, and the
				// staged write replaced the inode, so set the mode explicitly.
				if err := os.Chmod(full, entry.mode); err != nil {
					failures = append(failures, fmt.Sprintf("%s: restore mode: %v", entry.path, err))
				}
				continue
			}
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("%s: %v", entry.path, err))
			}
		}

		// Deepest first, so a created nest empties from the leaf up.
		created := make([]string, 0, len(preexistingDir))
		for dir, existed := range preexistingDir {
			if !existed {
				created = append(created, dir)
			}
		}
		sort.Slice(created, func(i, j int) bool {
			if a, b := strings.Count(created[i], "/"), strings.Count(created[j], "/"); a != b {
				return a > b
			}
			return created[i] < created[j]
		})
		for _, dir := range created {
			err := os.Remove(filepath.Join(plan.Root, filepath.FromSlash(dir)))
			// A directory this run created that is not empty now holds something
			// this transaction does not own. Leaving it is correct.
			if err == nil || os.IsNotExist(err) || errors.Is(err, syscall.ENOTEMPTY) ||
				errors.Is(err, syscall.EEXIST) {
				continue
			}
			failures = append(failures, fmt.Sprintf("%s/: %v", dir, err))
		}

		if len(failures) == 0 {
			return nil
		}
		sort.Strings(failures)
		return fmt.Errorf("rollback incomplete, do not trust the tree; could not restore %d path(s): %s",
			len(failures), strings.Join(failures, "; "))
	}
	// failed reports the transaction outcome. RolledBack is true only when the
	// restore actually succeeded, so an operator reading `rolled_back` can act
	// on it; a partial restore names every path it could not put back.
	failed := func(cause error) (Result, error) {
		restoreErr := rollback()
		if restoreErr == nil {
			return Result{Exit: 5, RolledBack: true}, cause
		}
		return Result{Exit: 5, RolledBack: false}, fmt.Errorf("%w; %w", cause, restoreErr)
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
	if e.toolRunner != nil && (len(plan.Lock.Dependencies) > 0 || len(plan.previousDependencies) > 0) {
		if _, err := snapshot("go.mod"); err != nil {
			return Result{}, err
		}
		if _, err := snapshot("go.sum"); err != nil {
			return Result{}, err
		}
		if _, err := ReconcileManagedDependencies(ctx, plan.Root, plan.previousDependencies, plan.Lock.Dependencies, e.toolRunner); err != nil {
			return failed(fmt.Errorf("update dependencies: %w", err))
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
		return failed(err)
	}

	// 3. Run the fixed generator pipeline once. Any failure restores the tree.
	if err := e.generator.Generate(ctx, plan); err != nil {
		return failed(fmt.Errorf("generation failed, restored pre-run bytes: %w", err))
	}

	// 4. Write the lock last, only after generation succeeds.
	lockContent, err := MarshalLock(plan.Lock)
	if err != nil {
		return failed(fmt.Errorf("marshal lock: %w", err))
	}
	if err := atomicWrite(filepath.Join(plan.Root, "gogogadget.lock.json"), lockContent); err != nil {
		return failed(fmt.Errorf("write lock: %w", err))
	}

	sort.Strings(written)
	sort.Strings(deleted)
	return Result{Exit: 0, Written: written, Deleted: deleted}, nil
}

// defaultFileMode is the mode a newly created authored or generated file gets.
// It matches what a git checkout produces for a non-executable file, which is
// what makes an installed module readable to a group checkout and to a container
// that builds as one uid and runs as another.
const defaultFileMode fs.FileMode = 0o644

// slashParent returns the parent of a slash-separated relative path, or "" at
// the top. It is deliberately not filepath.Dir: journal keys are slash paths,
// and on Windows filepath.Dir would not split them.
func slashParent(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return ""
	}
	return path[:i]
}

// atomicWrite writes content to a sibling temp file and renames it into place
// so a crash cannot leave a half-written target. os.CreateTemp opens at 0600,
// so the mode is set explicitly before the rename: an update keeps the target's
// existing mode and a create gets defaultFileMode. Without this every file
// `ggg add` installs would be owner-only.
func atomicWrite(full string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	mode := defaultFileMode
	if info, err := os.Stat(full); err == nil {
		mode = info.Mode().Perm()
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
	if err := tmp.Chmod(mode); err != nil {
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
