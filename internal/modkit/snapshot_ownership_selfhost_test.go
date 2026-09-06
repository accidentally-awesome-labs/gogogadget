// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository's
// four registry roots.

package modkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// selfHostRegistryRoots are every registry root this repository publishes or
// ships. The three nested ones are the reason the rule resolves ownership per
// root: registry/testdata and registry/external-testdata live INSIDE the core
// payload root and are listed in the core signed snapshot, and
// templates/external-registry is the maintained publisher template. A flat
// "must appear in some core files list" rule refuses all three outright.
var selfHostRegistryRoots = []string{
	".",
	"registry/testdata",
	"registry/external-testdata",
	"templates/external-registry",
}

// Six files no module declares reached this repository's signed snapshot and
// four gates passed with them in it. This is the assertion that would have
// failed.
func TestEveryFileInThePublishedRegistryTreesHasADeclaringOwner(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	for _, relative := range selfHostRegistryRoots {
		t.Run(relative, func(t *testing.T) {
			root := filepath.Join(repo, filepath.FromSlash(relative))
			require.FileExists(t, filepath.Join(root, "registry.json"),
				"%s is listed as a registry root but carries no registry.json", relative)
			require.NoError(t, ValidateRegistryTreeOwnership(os.DirFS(root)))
		})
	}
}

// The core snapshot is what actually gets signed, so the gate is checked
// against the committed file list rather than only against a fresh walk. A
// snapshot entry the ownership rule would reject is an undeclared payload
// already inside the signature.
func TestTheCommittedCoreSnapshotListsOnlyOwnedFiles(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(repo, RegistrySnapshotPath))
	require.NoError(t, err)
	var snapshot RegistrySnapshot
	require.NoError(t, decodeStrict(data, &snapshot))
	require.NotEmpty(t, snapshot.Files)

	// Ownership per root: build the declared set for the core catalog and for
	// each nested root, keyed by the path the core snapshot lists.
	owned := map[string]struct{}{}
	for _, relative := range []string{".", "registry/testdata", "registry/external-testdata"} {
		root := filepath.Join(repo, filepath.FromSlash(relative))
		declared, declErr := declaredRegistryPaths(os.DirFS(root))
		require.NoError(t, declErr)
		prefix := ""
		if relative != "." {
			prefix = relative + "/"
		}
		for path := range declared {
			owned[prefix+path] = struct{}{}
		}
		for path := range registryFormatOwnedPaths {
			owned[prefix+path] = struct{}{}
		}
	}

	unowned := make([]string, 0)
	for _, file := range snapshot.Files {
		if _, ok := owned[file.Path]; ok {
			continue
		}
		// The snapshot lists `_templ.go` siblings of the fixture registry's
		// own `.templ` payloads: `make generate` writes them beside their
		// source, and the snapshot walk does not exclude generated outputs
		// even though DirectorySource.Resolve does. Tool-owned, same as
		// everywhere else in this tree.
		if IsGeneratedOutputPath(file.Path) {
			continue
		}
		unowned = append(unowned, file.Path)
	}
	require.Emptyf(t, unowned,
		"the signed snapshot lists %d file(s) no module declares; a signature over bytes nobody declared makes them look deliberate",
		len(unowned))
}
