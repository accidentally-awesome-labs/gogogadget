package modkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The very first sync has no lock on disk yet — Apply writes it last, after
// generation. A generator that reads the on-disk lock therefore generates
// nothing on a fresh install, which is precisely when the aggregates are needed.
func TestRegistryGeneratorEmitsOnFirstSync(t *testing.T) {
	root, engine, _ := installedRemovalProject(t)
	if err := os.Remove(filepath.Join(root, LockFileName)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove lock: %v", err)
	}

	engine.generator = RegistryGenerator{}
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	bootstrap := filepath.Join(root, "internal", "modules", "bootstrap_registry_gen.go")
	data, err := os.ReadFile(bootstrap)
	if err != nil {
		t.Fatalf("first sync produced no bootstrap registry: %v", err)
	}
	if !strings.Contains(string(data), "func Boot(") {
		t.Fatalf("bootstrap registry has no Boot:\n%s", data)
	}
}
