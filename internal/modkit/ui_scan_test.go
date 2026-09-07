package modkit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// uiOwner is a component module that declares one renderer, the way every
// component and element module in the catalog does.
func uiOwner(id, renderer string) Manifest {
	target := "internal/web/templates/ui/" + renderer + ".templ"
	return Manifest{
		ID: id, Kind: ModuleComponent, Name: renderer,
		Files: []ManifestFile{{Source: target, Target: target, Class: FileClassTempl}},
		Runtime: RuntimeContributions{UI: []UIContribution{{
			Name: renderer, Signature: "templ " + renderer + "(o " + renderer + "Opts)",
		}}},
	}
}

// uiConsumer is a page module whose templ payload renders one component.
func uiConsumer(id string, requires []string, body string) (Manifest, map[string][]byte) {
	const target = "internal/web/templates/page.templ"
	needs := make([]Requirement, 0, len(requires))
	for _, requirement := range requires {
		needs = append(needs, Requirement{ID: requirement, Contract: ContractBounds{Min: 1, Max: 1}})
	}
	header := "package templates\n\nimport (\n\t\"example.com/app/internal/web/templates/ui\"\n)\n\n"
	return Manifest{
			ID: id, Kind: ModulePage, Name: "page", Requires: needs,
			Files: []ManifestFile{{Source: target, Target: target, Class: FileClassTempl}},
		},
		map[string][]byte{target: []byte(header + body)}
}

// The incident, reproduced as a unit: a page renders a component and its
// manifest says nothing about it, so a member list that stops naming that
// component takes it out of the closure and the tree stops compiling.
func TestUIComponentRequiresRefusesAnUndeclaredRenderer(t *testing.T) {
	notice := uiOwner("ggg/component/notice", "Notice")
	page, files := uiConsumer("ggg/page/settings-billing", nil,
		"templ Billing() {\n\t@ui.Notice(ui.NoticeOpts{})\n}\n")

	err := ValidateUIComponentRequires([]Manifest{notice, page}, []Manifest{notice, page}, files)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ggg/page/settings-billing renders ui.Notice")
	require.Contains(t, err.Error(), "ggg/component/notice")
	require.Contains(t, err.Error(), "internal/web/templates/page.templ:8")

	// Declared: accepted.
	page, files = uiConsumer("ggg/page/settings-billing", []string{"ggg/component/notice"},
		"templ Billing() {\n\t@ui.Notice(ui.NoticeOpts{})\n}\n")
	require.NoError(t, ValidateUIComponentRequires([]Manifest{notice, page}, []Manifest{notice, page}, files))
}

// Transitive is the right question. A page that requires data-table reaches
// column-header through it, and demanding a direct edge for a component it
// never names would be asking the manifest to restate the component's own
// composition.
func TestUIComponentRequiresFollowsTheRequiresClosure(t *testing.T) {
	columnHeader := uiOwner("ggg/component/column-header", "ColumnHeader")
	dataTable := uiOwner("ggg/component/data-table", "DataTable")
	dataTable.Requires = []Requirement{{ID: columnHeader.ID, Contract: ContractBounds{Min: 1, Max: 1}}}
	page, files := uiConsumer("ggg/page/activity", []string{dataTable.ID},
		"templ Activity() {\n\t@ui.DataTable(ui.DataTableOpts{})\n}\n")

	closure := []Manifest{columnHeader, dataTable, page}
	require.NoError(t, ValidateUIComponentRequires(closure, closure, files))

	// The page naming the inner component itself still needs its own edge:
	// data-table could stop composing it tomorrow.
	page, files = uiConsumer("ggg/page/activity", []string{dataTable.ID},
		"templ Activity() {\n\t@ui.ColumnHeader(ui.ColumnHeaderOpts{})\n}\n")
	closure = []Manifest{columnHeader, dataTable, page}
	require.NoError(t, ValidateUIComponentRequires(closure, closure, files))
}

// A payload that does not import the package cannot reference it, whatever
// words are in it. Without the import parse, prose in a handler about
// "ui.Badge" is a plan refusal.
func TestUIComponentRequiresIgnoresAPayloadThatDoesNotImportTheUI(t *testing.T) {
	badge := uiOwner("ggg/component/badge", "Badge")
	const target = "internal/web/handlers.go"
	page := Manifest{ID: "ggg/page/home", Kind: ModulePage, Name: "home",
		Files: []ManifestFile{{Source: target, Target: target, Class: FileClassGo}}}
	files := map[string][]byte{target: []byte(
		"package web\n\n// The hero used to call ui.Badge and no longer does.\nfunc Home() {}\n")}

	require.NoError(t, ValidateUIComponentRequires([]Manifest{badge, page}, []Manifest{badge, page}, files))
}

// The two measured false-positive sources of this repository's earlier
// attempts at this graph, as units.
//
// A cross-cutting test payload names every renderer in the package —
// ui-core's contract_test.go table names 175 of them, owned by all 144 other
// modules — so attributing test edges collapses the package into one
// strongly-connected component. And a generated registry names every
// component by string literal, which a textual rule reads as 144-way fan-out.
// Neither may reach the alphabet.
func TestUIComponentRequiresSkipsTestAndGeneratedPayloads(t *testing.T) {
	badge := uiOwner("ggg/component/badge", "Badge")
	header := "package templates\n\nimport (\n\t\"example.com/app/internal/web/templates/ui\"\n)\n\n"

	for _, payload := range []ManifestFile{
		{Source: "internal/web/page_test.go", Target: "internal/web/page_test.go", Class: FileClassTest},
		{Source: "internal/web/probe.go", Target: "internal/web/probe.go", Class: FileClassTest},
		{Source: "internal/web/gen.go", Target: "internal/web/gen.go", Class: FileClassGenerated},
	} {
		page := Manifest{ID: "ggg/page/home", Kind: ModulePage, Name: "home",
			Files: []ManifestFile{payload}}
		files := map[string][]byte{payload.Target: []byte(
			header + "var probe = ui.Badge\n")}
		require.NoErrorf(t, ValidateUIComponentRequires([]Manifest{badge, page}, []Manifest{badge, page}, files),
			"%s (%s) reached the scan", payload.Target, payload.Class)
	}
}

// A component naming another one inside the ui package uses a BARE
// identifier, so there is no qualifier to key on and this scan says nothing
// about it. Those edges are derived with go/types out of band; a rule here
// would be reading bare names, which is the degenerate answer.
func TestUIComponentRequiresSaysNothingAboutThePackageItself(t *testing.T) {
	icon := uiOwner("ggg/element/icon", "Icon")
	badge := uiOwner("ggg/component/badge", "Badge")
	target := badge.Files[0].Target
	files := map[string][]byte{target: []byte(
		"package ui\n\ntempl Badge(o BadgeOpts) {\n\t@Icon(IconOpts{})\n}\n")}

	require.NoError(t, ValidateUIComponentRequires([]Manifest{icon, badge}, []Manifest{icon, badge}, files))
}

// A renderer reference inside a comment or a string is not a reference. Three
// exist in this tree — two "ui.Text" in the legal pages' prose and one
// "ui.TerminalPage" in a billing comment — and they are the whole measured
// false-positive count of a scan without this step.
func TestUIComponentRequiresIgnoresCommentsAndStrings(t *testing.T) {
	text := uiOwner("ggg/element/text", "Text")
	page, files := uiConsumer("ggg/page/privacy", nil,
		"templ Privacy() {\n"+
			"\t// Body copy is plain markup rather than ui.Text: the prose\n"+
			"\t// layer already sets the type scale.\n"+
			"\t<p data-note=\"was ui.Text\">hello</p>\n"+
			"\t/* ui.Text */\n"+
			"}\n")

	require.NoError(t, ValidateUIComponentRequires([]Manifest{text, page}, []Manifest{text, page}, files))

	// And the same file with one real call is still refused, so the stripping
	// is not simply blanking the payload.
	page, files = uiConsumer("ggg/page/privacy", nil,
		"templ Privacy() {\n\t// ui.Text\n\t@ui.Text(ui.TextOpts{})\n}\n")
	require.ErrorContains(t, ValidateUIComponentRequires([]Manifest{text, page}, []Manifest{text, page}, files),
		"renders ui.Text")
}

// An aliased import is followed rather than assumed, and a local name that
// merely looks like it is not the package.
func TestUIComponentRequiresFollowsTheImportAlias(t *testing.T) {
	badge := uiOwner("ggg/component/badge", "Badge")
	const target = "internal/web/templates/page.templ"
	page := Manifest{ID: "ggg/page/home", Kind: ModulePage, Name: "home",
		Files: []ManifestFile{{Source: target, Target: target, Class: FileClassTempl}}}

	files := map[string][]byte{target: []byte(
		"package templates\n\nimport kit \"example.com/app/internal/web/templates/ui\"\n\n" +
			"templ Home() {\n\t@kit.Badge(kit.BadgeOpts{})\n}\n")}
	require.ErrorContains(t, ValidateUIComponentRequires([]Manifest{badge, page}, []Manifest{badge, page}, files),
		"renders ui.Badge")

	files = map[string][]byte{target: []byte(
		"package templates\n\nimport kit \"example.com/app/internal/web/templates/ui\"\n\n" +
			"templ Home() {\n\t@myui.Badge(myui.BadgeOpts{})\n}\n")}
	require.NoError(t, ValidateUIComponentRequires([]Manifest{badge, page}, []Manifest{badge, page}, files))
}

// The refusal names the module that would have to install the component when
// nothing in the closure does, because that is the failure the compiler shows
// three steps later with no module in the message at all.
func TestUIComponentRequiresNamesAnUninstalledOwner(t *testing.T) {
	badge := uiOwner("ggg/component/badge", "Badge")
	page, files := uiConsumer("ggg/page/home", nil,
		"templ Home() {\n\t@ui.Badge(ui.BadgeOpts{})\n}\n")

	require.ErrorContains(t, ValidateUIComponentRequires([]Manifest{badge, page}, []Manifest{badge, page}, files),
		"declares no requires path to it")
	// The owner absent from the closure entirely — the incident: this is the
	// case a scan whose alphabet is the installed set cannot see at all.
	require.ErrorContains(t, ValidateUIComponentRequires(
		[]Manifest{page}, []Manifest{badge, page}, files),
		"and nothing installs it")
}
