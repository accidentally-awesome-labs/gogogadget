package gggcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The built-in command table lives here, so the template's contributed
// command name is checked here too: modkit cannot import this package, and
// the reservation rule belongs to the table rather than to the schema.

func templateAdapterManifest(t *testing.T) modkit.Manifest {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", modkit.ExternalTemplateDir))
	require.NoError(t, err)
	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Modules)
	for _, module := range catalog.Modules {
		if len(module.Runtime.CLI) != 0 {
			return module
		}
	}
	t.Fatal("the external-registry template contributes no command")
	return modkit.Manifest{}
}

// registryBuildDir is what lets `ggg registry build --dir templates/external-registry`
// rebuild a registry that lives beside the project instead of being it, and
// it must never let --dir escape the project root.
func TestRegistryBuildDirIsProjectContained(t *testing.T) {
	controller := &Controller{root: "/tmp/project"}

	dir, err := controller.registryBuildDir("")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/project", dir)

	dir, err = controller.registryBuildDir("templates/external-registry")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/project", "templates", "external-registry"), dir)

	for _, bad := range []string{"/etc", "../escape", "templates/../../escape", " templates"} {
		_, err := controller.registryBuildDir(bad)
		require.Error(t, err, "--dir %q must be refused", bad)
	}
}
