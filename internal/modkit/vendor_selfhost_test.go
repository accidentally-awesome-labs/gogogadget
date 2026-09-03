// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every vendored byte in the tree must be declared by some module. An
// undeclared file is third-party code with no provenance and no owner, which is
// also a file no removal will ever clean up.
func TestEveryVendoredFileIsDeclared(t *testing.T) {
	root := treeRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	require.NoError(t, err)

	declared := map[string]struct{}{}
	for _, module := range catalog.Modules {
		for _, artifact := range module.Vendors {
			declared[artifact.Path] = struct{}{}
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "static", "vendor"))
	require.NoError(t, err)

	var undeclared []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join("static", "vendor", entry.Name())
		if _, ok := declared[path]; !ok {
			undeclared = append(undeclared, path)
		}
	}
	assert.Empty(t, undeclared, "vendored files with no declared provenance")
}

// The published schema has to describe the field, or an adopter's editor and
// validator disagree with the tool that reads it.
func TestVendorSchemaIsPublished(t *testing.T) {
	root := treeRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "registry", "schema", "module.schema.json"))
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	defs, ok := doc["$defs"].(map[string]any)
	require.True(t, ok)
	_, ok = defs["VendorArtifact"]
	assert.True(t, ok, "VendorArtifact must be published in the module schema")
}

// Every declared artifact must survive the same verification the build runs, so
// a stale pin fails here as well as at the CLI.
func TestDeclaredVendorsMatchTheTree(t *testing.T) {
	root := treeRoot(t)
	require.NoError(t, VerifyCatalogVendors(root))
}
