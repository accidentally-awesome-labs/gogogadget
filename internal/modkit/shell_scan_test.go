package modkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vendorAdapterManifest(id, name, slot, target, mode string) Manifest {
	return Manifest{
		ID:   id,
		Kind: ModuleSystem,
		Name: name,
		Runtime: RuntimeContributions{System: &SystemContribution{
			Package: "internal/" + name,
			Adapter: &AdapterContribution{
				Slot:    slot,
				Targets: []ServiceTarget{{ID: target, Mode: mode}},
			},
		}},
	}
}

func systemTemplateManifest(id string, targets ...string) Manifest {
	files := make([]ManifestFile, 0, len(targets))
	for _, target := range targets {
		files = append(files, ManifestFile{Source: target, Target: target, Class: FileClassTempl})
	}
	return Manifest{ID: id, Kind: ModuleSystem, Name: "server", Files: files}
}

// The vendor names are derived from the installed adapters, so the rule cannot
// drift from the catalog: adding an adapter for a managed service extends the
// scan without anybody editing a list.
func TestShellProviderNeutralityRefusesVendorInSeamTemplate(t *testing.T) {
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/identity-clerk", "identity-clerk", "ggg/identity", "clerk", "managed"),
		systemTemplateManifest("ggg/system/server", "internal/web/templates/layouts.templ"),
	}
	files := map[string][]byte{
		"internal/web/templates/layouts.templ": []byte("templ head() {\n\t<meta name=\"clerk-publishable-key\"/>\n}\n"),
	}

	err := ValidateShellProviderNeutrality(modules, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/web/templates/layouts.templ")
	assert.Contains(t, err.Error(), "ggg/system/server")
	assert.Contains(t, err.Error(), "ggg/system/identity-clerk")
	assert.Contains(t, err.Error(), "line 2")
}

// Managed target ids are tokens in their own right: storage-s3's product is
// called R2 and the vendor name is not derivable from the adapter's name.
func TestShellProviderNeutralityRefusesManagedTargetName(t *testing.T) {
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/storage-s3", "storage-s3", "ggg/storage", "r2", "managed"),
		systemTemplateManifest("ggg/system/server", "internal/web/templates/page.go"),
	}
	files := map[string][]byte{
		"internal/web/templates/page.go": []byte("package templates\n\n// upload straight to R2\n"),
	}

	require.ErrorContains(t, ValidateShellProviderNeutrality(modules, files), "\"r2\"")
}

func TestShellProviderNeutralityAcceptsNeutralSeamTemplate(t *testing.T) {
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/identity-clerk", "identity-clerk", "ggg/identity", "clerk", "managed"),
		vendorAdapterManifest("ggg/system/analytics-posthog", "analytics-posthog", "ggg/analytics", "posthog", "managed"),
		systemTemplateManifest("ggg/system/server", "internal/web/templates/sidebar.templ"),
	}
	files := map[string][]byte{
		"internal/web/templates/sidebar.templ": []byte(
			"templ Sidebar(page Page) {\n\t@shellMount(\"org-switcher\", orgSwitcherPlaceholder(page))\n}\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality(modules, files))
}

// An adapter's OWN payloads are where a vendor's name belongs. Refusing them
// would leave the markup nowhere to live.
func TestShellProviderNeutralityAllowsAdapterOwnedTemplates(t *testing.T) {
	adapter := vendorAdapterManifest("ggg/system/identity-clerk", "identity-clerk", "ggg/identity", "clerk", "managed")
	adapter.Files = []ManifestFile{{
		Source: "internal/identity/clerk/shell/shell.templ",
		Target: "internal/identity/clerk/shell/shell.templ",
		Class:  FileClassTempl,
	}}
	files := map[string][]byte{
		"internal/identity/clerk/shell/shell.templ": []byte("templ head() {\n\t<meta name=\"clerk-publishable-key\"/>\n}\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality([]Manifest{adapter}, files))
}

// Page and component modules are application content the project owns and
// edits. The marketing page that lists the reference stack is prose about a
// default, not a mechanism a project cannot remove.
func TestShellProviderNeutralityIgnoresNonSystemModules(t *testing.T) {
	page := systemTemplateManifest("ggg/page/home", "internal/web/templates/home.templ")
	page.Kind = ModulePage
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/identity-clerk", "identity-clerk", "ggg/identity", "clerk", "managed"),
		page,
	}
	files := map[string][]byte{
		"internal/web/templates/home.templ": []byte("<span>Postgres</span><span>Clerk</span>\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality(modules, files))
}

// Development-only and self-hosted adapters contribute no token. Otherwise
// "dev", "log", "local" and "memory" would be banned words in every template
// in the tree, and the rule would be about English rather than about vendors.
func TestShellProviderNeutralityIgnoresNonManagedAdapters(t *testing.T) {
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/observability-log", "observability-log", "ggg/observability", "log", "development"),
		vendorAdapterManifest("ggg/system/mail-smtp", "mail-smtp", "ggg/mail", "mailpit", "self-hosted"),
		systemTemplateManifest("ggg/system/server", "internal/web/templates/nav.templ"),
	}
	files := map[string][]byte{
		"internal/web/templates/nav.templ": []byte("<a href=\"/dev/login\">log in</a> <span>mailpit</span>\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality(modules, files))
}

// A word start is required, so ordinary English that merely contains a vendor
// name is not a leak: "probably" is not Ably, "Resend invitation" is not
// Resend's SDK.
func TestShellProviderNeutralityRequiresWordStart(t *testing.T) {
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/realtime-ably", "realtime-ably", "ggg/realtime", "ably", "managed"),
		systemTemplateManifest("ggg/system/server", "internal/web/templates/nav.templ"),
	}
	files := map[string][]byte{
		"internal/web/templates/nav.templ": []byte("// This is probably reasonably comfortable.\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality(modules, files))
}

// The scan covers the render surface. A by-key configuration read elsewhere is
// the sanctioned form and stays out of scope, so the rule does not pretend to
// enforce something it cannot see.
func TestShellProviderNeutralityScansTemplateBytesOnly(t *testing.T) {
	seam := systemTemplateManifest("ggg/system/config", "internal/config/config.go")
	seam.Files[0].Class = FileClassGo
	modules := []Manifest{
		vendorAdapterManifest("ggg/system/analytics-posthog", "analytics-posthog", "ggg/analytics", "posthog", "managed"),
		seam,
	}
	files := map[string][]byte{
		"internal/config/config.go": []byte("package config\n\nvar keys = []string{\"POSTHOG_API_KEY\"}\n"),
	}

	require.NoError(t, ValidateShellProviderNeutrality(modules, files))
}
