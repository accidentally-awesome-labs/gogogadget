package modkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assetOwner(id string, targets ...string) Manifest {
	files := make([]ManifestFile, 0, len(targets))
	for _, target := range targets {
		files = append(files, ManifestFile{Source: target, Target: target, Class: FileClassAsset})
	}
	return Manifest{ID: id, Kind: ModuleSystem, Name: "owner", Files: files}
}

func referrer(id, target, body string, class FileClass) (Manifest, map[string][]byte) {
	return Manifest{ID: id, Kind: ModuleSystem, Name: "referrer",
			Files: []ManifestFile{{Source: target, Target: target, Class: class}}},
		map[string][]byte{target: []byte(body)}
}

// The incident, reproduced as a unit: an adapter's head slot names the asset
// the adapter used to declare, and the declaration is gone.
func TestAssetReferencesRefuseAnUndeclaredAsset(t *testing.T) {
	slot, files := referrer("ggg/system/analytics-posthog",
		"internal/web/templates/slots/posthog.templ",
		"templ posthogHead(key string) {\n\t<script defer src=\"/static/analytics.js\"></script>\n}\n",
		FileClassTempl)

	err := ValidateAssetReferences([]Manifest{slot}, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/static/analytics.js")
	assert.Contains(t, err.Error(), "ggg/system/analytics-posthog")
	assert.Contains(t, err.Error(), "posthog.templ:2")
	assert.Contains(t, err.Error(), "no installed module declares")

	// Declared by the same module, or by any other installed one: resolved.
	require.NoError(t, ValidateAssetReferences(
		[]Manifest{slot, assetOwner("ggg/system/static", "static/analytics.js")}, files))
}

// The comparison is against declarations, never disk or environment: an asset
// is served only where its owning module is selected, so a stricter test would
// refuse every development project for not serving Clerk's bundles.
func TestAssetReferencesResolveThroughDeclarationsNotDisk(t *testing.T) {
	shell, files := referrer("ggg/system/server", "internal/web/templates/layouts.templ",
		"<link rel=\"stylesheet\" href=\"/static/app.css\"/>\n<script src=\"/static/vendor/clerk.browser.js\"></script>\n",
		FileClassTempl)
	generated := Manifest{ID: "ggg/system/static", Kind: ModuleSystem, Files: []ManifestFile{
		{Source: "static/app.css", Target: "static/app.css", Class: FileClassGenerated},
	}}
	adapter := Manifest{ID: "ggg/system/identity-clerk", Kind: ModuleSystem, Files: []ManifestFile{
		{Source: "static/vendor/clerk.browser.js", Target: "static/vendor/clerk.browser.js", Class: FileClassAsset},
	}}

	require.NoError(t, ValidateAssetReferences([]Manifest{shell, generated, adapter}, files))
}

// A route that merely contains the word static is not an asset reference.
// /ingest/static/array.js is the analytics proxy path, and matching its tail
// made the check invent four failures against itself.
func TestAssetReferencesIgnoreAProxiedPath(t *testing.T) {
	slot, files := referrer("ggg/system/analytics-posthog",
		"internal/web/templates/slots/posthog.templ",
		"<script defer src=\"/ingest/static/array.js\"></script>\n", FileClassTempl)

	require.NoError(t, ValidateAssetReferences([]Manifest{slot}, files))
}

// Prefixes and directories are not files. A design-system assertion about
// /static/ui/ and a content root at /static/guides resolve to nothing this
// check can look up.
func TestAssetReferencesIgnorePrefixes(t *testing.T) {
	seam, files := referrer("ggg/system/server", "internal/web/fragments.go",
		"const uiRoot = \"/static/ui/\"\nconst guides = \"/static/guides\"\n", FileClassGo)

	require.NoError(t, ValidateAssetReferences([]Manifest{seam}, files))
}

// Page and component markup is content the project owns and edits.
// ggg/page/dev-gallery's media-library scenario names six illustrative
// /static/images paths that do not exist; refusing them would be a rule about
// somebody's demo copy, and moving them would move committed baselines.
func TestAssetReferencesIgnoreNonSystemModules(t *testing.T) {
	page, files := referrer("ggg/page/dev-gallery", "internal/web/templates/scenario_content.templ",
		"{URL: \"/static/images/queue-latency.png\"}\n", FileClassTempl)
	page.Kind = ModulePage

	require.NoError(t, ValidateAssetReferences([]Manifest{page}, files))
}

// A test may name a path it invents: vendor_test.go's /static/vendor/a.js is a
// fixture, not a promise.
func TestAssetReferencesIgnoreTestPayloads(t *testing.T) {
	seam, files := referrer("ggg/system/modkit", "internal/modkit/vendor_test.go",
		"const fixture = \"/static/vendor/a.js\"\n", FileClassTest)

	require.NoError(t, ValidateAssetReferences([]Manifest{seam}, files))
}

// The declaration side, which needs no reference at all to be wrong: a
// runtime.assets entry or a vendored pin whose module owns no payload for it.
func TestAssetReferencesRefuseADeclarationWithNoPayload(t *testing.T) {
	// Manifests write the path with the static/ prefix; the generated registry
	// strips it rather than adding one, so both shapes resolve to one target.
	module := Manifest{ID: "ggg/system/static", Kind: ModuleSystem,
		Runtime: RuntimeContributions{Assets: []AssetContribution{
			{ID: "ui.grid", Path: "static/ui/grid.js", Kind: AssetScript},
		}}}
	require.ErrorContains(t, ValidateAssetReferences([]Manifest{module}, nil),
		"declares asset ui.grid but owns no payload at static/ui/grid.js")

	module.Files = []ManifestFile{{Source: "static/ui/grid.js", Target: "static/ui/grid.js", Class: FileClassScript}}
	require.NoError(t, ValidateAssetReferences([]Manifest{module}, nil))

	module.Vendors = []VendorArtifact{{Path: "static/vendor/htmx.min.js"}}
	require.ErrorContains(t, ValidateAssetReferences([]Manifest{module}, nil),
		"declares vendored asset static/vendor/htmx.min.js but owns no payload")
}
