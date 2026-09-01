package gggcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The migrate command emits the fixed envelope and rolls the project back
// atomically when the lock write fails.
func TestMigrateSchema1CommandEmitsEnvelopeAndRollsBackAtomically(t *testing.T) {
	projectBefore := []byte(`{"schema":1,"registry":{"repository":".","path":"registry"},"modules":["profile/full"],"exclude":[]}`)
	lockBefore := []byte(`{"schema":1,"registry_commit":"commit-a","registry":{"source":"directory","requested_ref":"local","canonical_module":"github.com/gogogadget/gogogadget","key_fingerprint":""},"order":[],"modules":[]}`)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, modkit.ProjectFileName), projectBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, modkit.LockFileName), lockBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	app := App{Root: root, Out: &out}
	if err := app.Run(context.Background(), []string{"migrate", "schema-1", "--json"}); err != nil {
		t.Fatalf("migrate command: %v", err)
	}
	var envelope modkit.Envelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v\n%s", err, out.String())
	}
	if !envelope.OK || envelope.Exit != modkit.ExitOK || envelope.Command != "migrate" {
		t.Fatalf("envelope = %#v", envelope)
	}
	migratedProject, err := os.ReadFile(filepath.Join(root, modkit.ProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	migratedLock, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modkit.ParseProject(migratedProject); err != nil {
		t.Fatalf("migrated project did not reparse: %v", err)
	}
	if _, err := modkit.ParseLock(migratedLock); err != nil {
		t.Fatalf("migrated lock did not reparse: %v", err)
	}
	if string(migratedProject) == string(projectBefore) || string(migratedLock) == string(lockBefore) {
		t.Fatal("migration did not rewrite schema metadata")
	}

	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, modkit.ProjectFileName), projectBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, modkit.LockFileName), lockBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	writeCalls := 0
	app = App{Root: root, Out: &bytes.Buffer{}, WriteFile: func(name string, data []byte, mode os.FileMode) error {
		writeCalls++
		if strings.HasSuffix(name, modkit.LockFileName) {
			return os.ErrPermission
		}
		return os.WriteFile(name, data, mode)
	}}
	err = app.Run(context.Background(), []string{"migrate", "schema-1"})
	if err == nil || exitOf(t, err) != modkit.ExitRollback {
		t.Fatalf("rollback error = %v (exit %d)", err, exitOf(t, err))
	}
	projectAfter, _ := os.ReadFile(filepath.Join(root, modkit.ProjectFileName))
	if !bytes.Equal(projectBefore, projectAfter) {
		t.Fatalf("project was not restored after lock write failure")
	}
	if writeCalls != 3 {
		t.Fatalf("write calls = %d, want project, lock, restore", writeCalls)
	}
}
