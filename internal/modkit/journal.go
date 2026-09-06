package modkit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// journalEntry records one pre-run filesystem state so a failed transaction
// can be restored exactly — bytes and mode. Mode matters because
// os.CreateTemp makes the staged file 0600: without capturing and restoring
// the original, a rolled-back file would come back owner-only.
type journalEntry struct {
	path    string
	existed bool
	content []byte
	mode    fs.FileMode
}

// fileJournal is the one write-transaction primitive in this engine: snapshot
// every path an operation can touch before touching any of them, then restore
// exact pre-run bytes, modes and directory existence if the operation fails.
//
// Engine.Apply owns the project transaction. `ggg registry build` needs the
// same guarantee for a different reason and used to have none: it refreshed
// manifests, wrote six index files, and only then read registry.json — so a
// build that refused on a missing root had already mutated the tree it
// refused to build. Two journals would have been two sets of rollback bugs.
type fileJournal struct {
	root    string
	entries map[string]*journalEntry
	order   []string
	// preexistingDir answers "did this directory exist before the run" for
	// every ancestor of every snapshotted path, so rollback can remove the
	// directories MkdirAll created without touching one the operator already
	// had.
	preexistingDir map[string]bool
}

func newFileJournal(root string) *fileJournal {
	return &fileJournal{
		root:           root,
		entries:        map[string]*journalEntry{},
		preexistingDir: map[string]bool{},
	}
}

// noteDirs records whether each ancestor of path existed before the run.
func (j *fileJournal) noteDirs(path string) {
	for dir := slashParent(path); dir != ""; dir = slashParent(dir) {
		if _, seen := j.preexistingDir[dir]; seen {
			return
		}
		_, statErr := os.Stat(filepath.Join(j.root, filepath.FromSlash(dir)))
		j.preexistingDir[dir] = statErr == nil
	}
}

// Snapshot records the current bytes and mode of one root-relative path, or
// its absence. Repeat calls for the same path are no-ops, so the first
// recorded state is always the pre-run state.
func (j *fileJournal) Snapshot(path string) error {
	if _, ok := j.entries[path]; ok {
		return nil
	}
	j.noteDirs(path)
	full := filepath.Join(j.root, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err == nil {
		mode := fs.FileMode(defaultFileMode)
		if info, statErr := os.Stat(full); statErr == nil {
			mode = info.Mode().Perm()
		}
		j.entries[path] = &journalEntry{path: path, existed: true, content: data, mode: mode}
		j.order = append(j.order, path)
		return nil
	}
	if os.IsNotExist(err) {
		j.entries[path] = &journalEntry{path: path, existed: false}
		j.order = append(j.order, path)
		return nil
	}
	return fmt.Errorf("snapshot %s: %w", path, err)
}

// Rollback restores every journalled path and reports what it could not
// restore. Silently swallowing a restore failure is the worst outcome here:
// disk-full is the likeliest cause of the failure that triggered the
// rollback, it is just as likely to defeat the restore, and the caller's one
// job is to tell the operator whether the tree is trustworthy.
func (j *fileJournal) Rollback() error {
	failures := make([]string, 0)
	// Restore newest-first so created paths are removed before updated paths
	// regain their exact prior bytes.
	for i := len(j.order) - 1; i >= 0; i-- {
		entry := j.entries[j.order[i]]
		full := filepath.Join(j.root, filepath.FromSlash(entry.path))
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
	created := make([]string, 0, len(j.preexistingDir))
	for dir, existed := range j.preexistingDir {
		if !existed {
			created = append(created, dir)
		}
	}
	sort.Slice(created, func(i, k int) bool {
		if a, b := strings.Count(created[i], "/"), strings.Count(created[k], "/"); a != b {
			return a > b
		}
		return created[i] < created[k]
	})
	for _, dir := range created {
		err := os.Remove(filepath.Join(j.root, filepath.FromSlash(dir)))
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

// slashParent returns the parent of a slash-separated relative path, or "" at
// the top. It is deliberately not filepath.Dir: journal keys are slash paths,
// and on Windows filepath.Dir would not split them.
func slashParent(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return ""
	}
	return path[:index]
}
