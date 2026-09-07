// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository's
// catalog and payload bytes.

package modkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The whole tree agrees with the gate, and the gate is not vacuous.
//
// These two halves are one claim. ValidateUIComponentRequires is a lexical
// scan over .templ bytes — it has to be, because a templ body is not Go and
// there is no type information at plan time — so the question that decides
// whether it may ship is how often it invents an edge. Measured across every
// authored payload in this repository against a go/types derivation of the
// same references (TypesInfo.Uses, attributed to the file at obj.Pos()): 384
// payloads, 2374 reference sites, 945 distinct (module, renderer) pairs, ZERO
// false positives and ZERO false negatives. The two variants that were
// rejected measured 3 (no comment/string stripping: two "ui.Text" in the
// legal pages' prose and one "ui.TerminalPage" in a billing comment) and 14
// (test payloads in scope, all 14 attributed to ggg/system/server).
//
// The mutation half is the one that matters more, because a scan that refuses
// nothing also has zero false positives. Removing one real edge from one real
// manifest has to refuse — and the edge removed here is the incident: seven of
// `minimal`'s pages call ui.Notice, and before this ggg/component/notice was
// held in that closure only by ggg/component/kanban's requires, a component
// nothing referenced.
func TestTheUIComponentGateHoldsAndRefusesARemovedEdge(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	catalog, err := LoadCatalog(os.DirFS(repo))
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Modules)

	files := map[string][]byte{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			content, readErr := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file.Target)))
			if readErr != nil {
				continue
			}
			files[file.Target] = content
		}
	}
	require.NoError(t, ValidateUIComponentRequires(catalog.Modules, catalog.Modules, files),
		"the committed manifests disagree with the payloads that reference them")

	const (
		consumer  = "ggg/page/settings-billing"
		component = "ggg/component/notice"
	)
	mutated := make([]Manifest, 0, len(catalog.Modules))
	dropped := false
	for _, module := range catalog.Modules {
		if module.ID == consumer {
			kept := make([]Requirement, 0, len(module.Requires))
			for _, requirement := range module.Requires {
				if requirement.ID == component {
					dropped = true
					continue
				}
				kept = append(kept, requirement)
			}
			module.Requires = kept
		}
		mutated = append(mutated, module)
	}
	require.Truef(t, dropped, "%s no longer declares %s, so this mutation proves nothing", consumer, component)
	require.ErrorContains(t, ValidateUIComponentRequires(mutated, catalog.Modules, files),
		consumer+" renders ui.Notice")
}
