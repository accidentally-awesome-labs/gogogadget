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

// The committed snapshot is what actually gets signed, so it is checked
// entry by entry against the real gate rather than against a hand-rebuilt
// approximation of it. Two things must hold: nothing listed is unowned, and
// nothing listed went unexamined. The second is the one that failed — this
// repository shipped registry/external-testdata's own snapshot and signature
// inside the core signature while the gate pruned them out of its walk.
func TestTheCommittedCoreSnapshotListsOnlyOwnedFiles(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(repo, RegistrySnapshotPath))
	require.NoError(t, err)
	var snapshot RegistrySnapshot
	require.NoError(t, decodeStrict(data, &snapshot))
	require.NotEmpty(t, snapshot.Files)

	report, err := registryTreeOwnership(os.DirFS(repo))
	require.NoError(t, err)
	require.NoError(t, report.refusal(),
		"a signature over bytes nobody declared makes them look deliberate")

	examined := make(map[string]struct{}, len(report.examined))
	for _, path := range report.examined {
		examined[path] = struct{}{}
	}
	unexamined := make([]string, 0)
	for _, file := range snapshot.Files {
		if _, ok := examined[file.Path]; !ok {
			unexamined = append(unexamined, file.Path)
		}
	}
	require.Emptyf(t, unexamined,
		"the committed signature covers %d file(s) the ownership gate never examined: %v",
		len(unexamined), unexamined)
}
