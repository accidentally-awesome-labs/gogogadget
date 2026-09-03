package modkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A vendored file is third-party code committed into the repository. Without
// provenance nobody can answer where it came from, which version it is, or
// whether the bytes on disk are the bytes that were reviewed - and "re-download
// it and see" is not an answer once the CDN has moved on.
func TestVendorArtifactRequiresProvenance(t *testing.T) {
	base := VendorArtifact{
		Path:      "static/vendor/thing-1.0.0.js",
		Source:    "https://cdn.jsdelivr.net/npm/thing@1.0.0/dist/thing.js",
		Version:   "1.0.0",
		Bytes:     12,
		SHA256:    strings.Repeat("a", 64),
		License:   "MIT",
		LicenseAt: "static/vendor/LICENSES.md",
	}
	require.NoError(t, validateVendors([]VendorArtifact{base}, true))

	missing := map[string]func(*VendorArtifact){
		"source":  func(v *VendorArtifact) { v.Source = "" },
		"version": func(v *VendorArtifact) { v.Version = "" },
		"bytes":   func(v *VendorArtifact) { v.Bytes = 0 },
		"sha256":  func(v *VendorArtifact) { v.SHA256 = "" },
		"license": func(v *VendorArtifact) { v.License = "" },
	}
	for field, break_ := range missing {
		bad := base
		break_(&bad)
		assert.Error(t, validateVendors([]VendorArtifact{bad}, true),
			"a vendored artifact with no %s is unreviewable", field)
	}
}

// The source has to be a real https origin. A bare package name or an http URL
// cannot be re-fetched and verified, which is the only thing that makes the
// recorded digest meaningful.
func TestVendorSourceMustBeHTTPS(t *testing.T) {
	artifact := VendorArtifact{
		Path: "static/vendor/a.js", Source: "http://cdn.example/a.js", Version: "1",
		Bytes: 1, SHA256: strings.Repeat("b", 64), License: "MIT",
	}

	err := validateVendors([]VendorArtifact{artifact}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")
}

// A truncated or mistyped digest is worse than none: it looks like a check.
func TestVendorDigestMustBeAFullSHA256(t *testing.T) {
	artifact := VendorArtifact{
		Path: "static/vendor/a.js", Source: "https://cdn.example/a.js", Version: "1",
		Bytes: 1, SHA256: "abc123", License: "MIT",
	}

	assert.Error(t, validateVendors([]VendorArtifact{artifact}, true))
}

// eval and its relatives turn a data path into a code path. A vendored file
// containing one cannot be reasoned about, and the CSP that forbids it in the
// browser says nothing about what the file does with the strings it is given.
func TestScanRejectsDynamicCodeExecution(t *testing.T) {
	cases := map[string]string{
		"eval":            `var x = eval("1+1");`,
		"new Function":    `var f = new Function("a", "return a");`,
		"string timeout":  `setTimeout("doThing()", 10);`,
		"string interval": `setInterval("tick()", 10);`,
	}
	for name, source := range cases {
		findings := scanVendorSource("static/vendor/a.js", []byte(source))
		assert.NotEmpty(t, findings, "%s must be rejected", name)
	}
}

// A vendored file that reaches out to an origin nobody declared defeats the
// point of vendoring it: the review covers bytes on disk, not what those bytes
// fetch at runtime.
func TestScanRejectsUndeclaredOrigins(t *testing.T) {
	findings := scanVendorSource("static/vendor/a.js", []byte(`fetch("https://telemetry.example.com/v1")`))

	require.NotEmpty(t, findings)
	assert.Contains(t, strings.Join(findings, " "), "telemetry.example.com")
}

// Function-valued timers are the normal, safe form and must not be flagged, or
// the scan is noise that gets switched off.
func TestScanAllowsOrdinaryCode(t *testing.T) {
	safe := [][]byte{
		[]byte(`setTimeout(function () { tick(); }, 10);`),
		[]byte(`setTimeout(() => tick(), 10);`),
		[]byte(`const evaluate = (x) => x + 1;`),
		[]byte(`element.dataset.evaluation = "ok";`),
		[]byte(`new FunctionalThing();`),
	}
	for _, source := range safe {
		assert.Empty(t, scanVendorSource("static/vendor/a.js", source),
			"ordinary code must not be flagged: %s", source)
	}
}

// Same-origin references are how every adapter loads its own assets.
func TestScanAllowsSameOriginReferences(t *testing.T) {
	assert.Empty(t, scanVendorSource("static/vendor/a.js",
		[]byte(`fetch("/static/vendor/a.js"); img.src = "/media/x.png";`)))
}

// The digest is checked against the file, so a swapped vendor file fails the
// build rather than shipping.
func TestVendorChecksumDriftIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static", "vendor")
	require.NoError(t, os.MkdirAll(path, 0o755))
	file := filepath.Join(path, "a.js")
	require.NoError(t, os.WriteFile(file, []byte("console.log(1)\n"), 0o644))

	artifact := VendorArtifact{
		Path: "static/vendor/a.js", Source: "https://cdn.example/a.js", Version: "1",
		Bytes: 15, SHA256: strings.Repeat("c", 64), License: "MIT",
	}

	err := VerifyVendorArtifacts(dir, []VendorArtifact{artifact})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256")
}

// A byte count that disagrees with the file is drift too, and it is the cheapest
// thing to get wrong when updating a pin by hand.
func TestVendorByteCountMustMatch(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "static", "vendor"), 0o755))
	body := []byte("console.log(1)\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "static", "vendor", "a.js"), body, 0o644))

	artifact := VendorArtifact{
		Path: "static/vendor/a.js", Source: "https://cdn.example/a.js", Version: "1",
		Bytes: 999, SHA256: sha256Hex(body), License: "MIT",
	}

	err := VerifyVendorArtifacts(dir, []VendorArtifact{artifact})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bytes")
}

// treeRoot walks up to the repository root. The internal test package cannot use
// the external suite's helper, and hardcoding "../.." breaks the moment a test
// file moves.
func treeRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "no go.mod above the working directory")
		dir = parent
	}
}

// Removing a module must delete the build output derived from its sources.
//
// A deleted `X.templ` whose generated `X_templ.go` survives still defines the
// renderer, so the code compiles and the component keeps rendering: removal
// reports success and changes nothing until the next `templ generate`. That is
// worse than refusing, because the operator believes it worked.
func TestGeneratedSiblingsOfRemovedSourcesAreDeleted(t *testing.T) {
	deleted := []string{
		"internal/web/templates/ui/kanban.templ",
		"static/ui/kanban.js",
		"internal/web/templates/ui/kanban_test.go",
	}

	extra := generatedSiblings(deleted)

	assert.Contains(t, extra, "internal/web/templates/ui/kanban_templ.go")
	// Only templ sources have a generated sibling; a plain Go file or an asset
	// has none, and inventing one would delete a file nobody generated.
	assert.Len(t, extra, 1)
}

// A source whose generated sibling is itself declared must not be listed twice:
// removal would try to delete the same path in one plan.
func TestGeneratedSiblingIsNotDuplicated(t *testing.T) {
	deleted := []string{
		"internal/web/templates/ui/kanban.templ",
		"internal/web/templates/ui/kanban_templ.go",
	}

	assert.Empty(t, generatedSiblings(deleted))
}
