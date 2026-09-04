package modkit

import (
	"strings"
	"testing"
)

// adapterManifest is the smallest manifest that declares an adapter package.
func adapterManifest(id, pkg string) Manifest {
	m := Manifest{ID: id}
	m.Runtime.System = &SystemContribution{
		Package:     pkg,
		Constructor: "NewModule",
		Adapter:     &AdapterContribution{Slot: "ggg/identity"},
	}
	return m
}

// The CLI must reach a selected adapter only through the generated per-slot
// accessors. A direct import pins an unselected adapter into every build,
// which is what made a project without ggg/system/identity-clerk impossible to
// compile — so the scan refuses it rather than letting it compile.
func TestValidateCoreCLIPackagesRefusesAdapterImport(t *testing.T) {
	modules := []Manifest{
		adapterManifest("ggg/system/identity-clerk", "internal/identity/clerk"),
		adapterManifest("ggg/system/identity-dev", "internal/identity/devadapter"),
	}

	for name, tc := range map[string]struct {
		target  string
		source  string
		refused bool
	}{
		"direct adapter import refused": {
			target: "internal/gggcli/handlers.go",
			source: "package gggcli\n\nimport identityclerk \"github.com/gogogadget/gogogadget/internal/identity/clerk\"\n",
			// The rewritten derivative path form must be caught too.
			refused: true,
		},
		"unrewritten adapter path refused": {
			target:  "internal/gggcli/handlers.go",
			source:  "package gggcli\n\nimport _ \"internal/identity/devadapter\"\n",
			refused: true,
		},
		"adapter import in a gggcli test refused": {
			target:  "internal/gggcli/handlers_test.go",
			source:  "package gggcli\n\nimport _ \"example.com/app/internal/identity/clerk\"\n",
			refused: true,
		},
		"generated accessor import allowed": {
			target:  "internal/gggcli/handlers.go",
			source:  "package gggcli\n\nimport \"github.com/gogogadget/gogogadget/internal/modules\"\n",
			refused: false,
		},
		"the seam itself is not an adapter": {
			target:  "internal/gggcli/handlers.go",
			source:  "package gggcli\n\nimport \"github.com/gogogadget/gogogadget/internal/identity\"\n",
			refused: false,
		},
		"outside the CLI tree is not this scan's business": {
			target:  "internal/modules/bootstrap_registry_gen.go",
			source:  "package modules\n\nimport identityclerk \"github.com/gogogadget/gogogadget/internal/identity/clerk\"\n",
			refused: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateCoreCLIPackages(modules, map[string][]byte{tc.target: []byte(tc.source)})
			if tc.refused {
				if err == nil {
					t.Fatalf("%s: adapter import accepted", tc.target)
				}
				if !strings.Contains(err.Error(), "generated per-slot accessors") {
					t.Fatalf("refusal must name the sanctioned indirection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected refusal %v", tc.target, err)
			}
		})
	}
}

// A project with no adapters installed has nothing to ban, and the scan must
// not invent a refusal from an empty catalog.
func TestValidateCoreCLIPackagesWithoutAdapters(t *testing.T) {
	if err := ValidateCoreCLIPackages(nil, map[string][]byte{
		"internal/gggcli/handlers.go": []byte("package gggcli\n\nimport _ \"internal/identity/clerk\"\n"),
	}); err != nil {
		t.Fatalf("unexpected refusal %v", err)
	}
}
