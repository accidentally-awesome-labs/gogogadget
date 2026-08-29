package static_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/static"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every file under static/ must be embedded. The embed patterns are generated
// from declarations, so an undeclared asset is invisible to the binary — it
// sits on disk and 404s in every environment that runs the compiled server.
// This walks the real directory so it cannot be satisfied by the declarations
// alone.
func TestEveryStaticFileIsEmbedded(t *testing.T) {
	root := "."
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	embedded := map[string]bool{}
	err = fs.WalkDir(static.FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			embedded[name] = true
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, embedded)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".go" {
			continue // source of this package, not an asset
		}
		assert.True(t, embedded[entry.Name()],
			"%s exists on disk but is not embedded; declare it as an asset (or generated) in the owning module", entry.Name())
	}
}

// The reverse direction: a declared asset that vanished from the tree is a
// compile error already, but a nested one could hide behind a directory pattern
// in a future emitter change, so pin the explicit direction too.
func TestEmbeddedFilesExistOnDisk(t *testing.T) {
	err := fs.WalkDir(static.FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		_, statErr := os.Stat(filepath.Join(".", name))
		assert.NoError(t, statErr, "%s is embedded but missing from the working tree", name)
		return nil
	})
	require.NoError(t, err)
}
