package gggcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// `ggg new --registry github:OWNER/REPO` fetches this tree and verifies it
// against coreRegistryPublicKey, so registry.snapshot.sig is a published
// artifact of this repository, not a local build leftover. It was gitignored on
// the opposite theory, and every genesis from GitHub refused with
// "read registry.snapshot.sig: no such file or directory" — no adoption path
// worked at all. This test is the gate: it fails if the signature is missing,
// stale relative to registry.snapshot.json, or produced by a key the shipped
// CLI does not pin.
func TestCommittedSnapshotVerifiesUnderThePinnedCoreKey(t *testing.T) {
	root := repoRootFromTest(t)
	digest, err := modkit.VerifyRegistrySnapshot(root, coreRegistryPublicKey)
	if err != nil {
		t.Fatalf("the committed core snapshot does not verify under the pinned key: %v\n"+
			"remedy: ggg registry build && ggg registry sign --dir . --key-file <core signing key>", err)
	}
	if digest == "" {
		t.Fatal("verification returned an empty digest, so nothing was checked")
	}
}

// repoRootFromTest walks up to the project root. The test asserts on committed
// artifacts, so it needs the real tree rather than a fixture.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "gogogadget.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no gogogadget.json above the test working directory")
		}
		dir = parent
	}
}
