// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The manifest digest of a generated payload is written by `registry build`
// and read by nothing: readPlannedPayloadsWithSources returns early on FileClassGenerated,
// before the check that raises "payload ... sha256 mismatch". Recording one
// rewrote manifests on every build for no consumer.
func TestRegistryBuildRecordsNoDigestForGeneratedPayloads(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "registry", "modules", "system", "static", "module.json"))
	require.NoError(t, err)

	var document ModuleDocument
	require.NoError(t, decodeStrict(raw, &document))

	generated := 0
	for _, file := range document.Module.Files {
		if file.Class != FileClassGenerated {
			continue
		}
		generated++
		assert.Emptyf(t, file.SHA256,
			"%s is generated, so its digest is never verified; recording one is churn that rewrites this manifest on every build", file.Target)
	}
	require.Positive(t, generated, "ggg/system/static declares no generated payload, so this proves nothing")
}
