package modkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No vendored file anywhere in the tree may execute code built from a string.
//
// This is asserted across the whole directory rather than per module because it
// is a property of the vendoring decision, not of any one dependency: a version
// bump that introduces an eval is exactly the change that would otherwise slip
// through review of a minified diff. All 61 files satisfy it today, so the test
// is a line in the sand rather than a wish.
func TestNoVendoredFileExecutesStrings(t *testing.T) {
	root := treeRoot(t)
	dir := filepath.Join(root, "static", "vendor")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var offenders []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		rel := "static/vendor/" + entry.Name()
		for _, finding := range scanVendorSource(rel, body) {
			// Origin references are declared per artifact and checked by
			// VerifyVendorArtifacts; this test is only about dynamic code.
			if strings.Contains(finding, "undeclared origin") {
				continue
			}
			offenders = append(offenders, finding)
		}
	}
	assert.Empty(t, offenders, "vendored files that build code from strings")
}
