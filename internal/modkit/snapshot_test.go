package modkit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestSignedRegistrySnapshotVerifiesPayloadsAndRejectsUnlistedFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"external","canonical_module":"example.com/external/catalog","includes":[]}`))
	writeTestFile(t, root, "registry/modules/element/declared.txt", []byte("declared"))
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSignedRegistrySnapshot(root, private); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(public)
	if _, err := verifySnapshotFiles(os.DirFS(root), encoded, false); err != nil {
		t.Fatalf("verify: %v", err)
	}
	writeTestFile(t, root, "registry/modules/element/tampered.txt", []byte("not listed"))
	if _, err := verifySnapshotFiles(os.DirFS(root), encoded, false); err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Fatalf("verify unlisted = %v, want unlisted refusal", err)
	}
}
