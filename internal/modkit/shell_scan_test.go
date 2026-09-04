package modkit

import (
	"context"
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

// A mount slot is exclusive because the contribution carries the same id the
// shell's fallback does. Two contributions would emit one id twice, and the
// shell has no way to choose, so generation refuses and names both modules.
// The check runs over the installed union — see ExclusiveShellSlots for why
// that is stricter than the runtime needs.
func TestShellSlotsRegistryRefusesTwoContributionsToAnExclusiveSlot(t *testing.T) {
	mount := func(id, module string) Manifest {
		return Manifest{ID: module, Kind: ModuleSystem, Runtime: RuntimeContributions{
			System: &SystemContribution{Adapter: &AdapterContribution{
				Slot:    "ggg/identity",
				Targets: []ServiceTarget{{ID: "local", Mode: "development"}},
			}},
			Slots: []SlotContribution{{
				ID: id, Slot: ShellSlotOrgSwitcher,
				Package: "internal/web/templates/slots", Renderer: "Mount",
			}},
		}}
	}
	first, second := mount("first.mount", "ggg/system/identity-a"), mount("second.mount", "ggg/system/identity-b")
	lock := Lock{Schema: 2, Order: []string{first.ID, second.ID}}

	_, err := emitShellSlotsRegistry(context.Background(), "example.com/app", lock, []Manifest{first, second})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `shell slot "org-switcher" is exclusive`)
	assert.Contains(t, err.Error(), "ggg/system/identity-a")
	assert.Contains(t, err.Error(), "ggg/system/identity-b")

	// One contribution is the whole point: it must still generate.
	out, err := emitShellSlotsRegistry(context.Background(), "example.com/app",
		Lock{Schema: 2, Order: []string{first.ID}}, []Manifest{first})
	require.NoError(t, err)
	assert.Contains(t, out.Content, `"org-switcher": []string{`)
	assert.Contains(t, out.Content, `"first.mount": shellSlot0.Mount,`)

	// An additive slot takes both, in installed-module order.
	head := func(id, module string) Manifest {
		m := mount(id, module)
		m.Runtime.Slots[0].Slot = ShellSlotHead
		return m
	}
	headA, headB := head("a.head", "ggg/system/identity-a"), head("b.head", "ggg/system/identity-b")
	out, err = emitShellSlotsRegistry(context.Background(), "example.com/app",
		Lock{Schema: 2, Order: []string{headA.ID, headB.ID}}, []Manifest{headA, headB})
	require.NoError(t, err)
	assert.Contains(t, out.Content, "\t\t\"a.head\",\n\t\t\"b.head\",\n")
}

// The renderer signature is the thing a slot contributor actually breaks
// against, and it is checked before any write rather than inherited as a
// compile error inside the generated registry. A contract range could not do
// this job: requires is a construction edge, so a slot contributor cannot
// declare one on the module that owns the slot mechanism at all.
func TestShellSlotRenderersRefuseAWrongSignature(t *testing.T) {
	contributor := func(renderer string) Manifest {
		return Manifest{ID: "ggg/system/identity-clerk", Kind: ModuleSystem, Name: "identity-clerk",
			Runtime: RuntimeContributions{Slots: []SlotContribution{{
				ID: "identity-clerk-head", Slot: ShellSlotHead,
				Package: "internal/web/templates/slots", Renderer: renderer,
			}}}}
	}
	source := func(body string) map[string][]byte {
		return map[string][]byte{"internal/web/templates/slots/clerk.go": []byte(
			"package slots\n\nimport (\n\t\"context\"\n\n\t\"github.com/a-h/templ\"\n)\n\n" + body)}
	}

	require.NoError(t, ValidateShellSlotRenderers([]Manifest{contributor("ClerkHead")}, source(
		"func ClerkHead(_ context.Context, values map[string]string) templ.Component {\n\treturn templ.NopComponent\n}\n")))

	// The pre-v0.5.0 shape: no context, no values.
	err := ValidateShellSlotRenderers([]Manifest{contributor("ClerkHead")}, source(
		"func ClerkHead() templ.Component {\n\treturn templ.NopComponent\n}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity-clerk-head")
	assert.Contains(t, err.Error(), "ggg/system/identity-clerk")
	assert.Contains(t, err.Error(), shellSlotRendererSignature)

	// The right arity with the wrong value type, which a contract number would
	// have called compatible.
	require.ErrorContains(t, ValidateShellSlotRenderers([]Manifest{contributor("ClerkHead")}, source(
		"func ClerkHead(_ context.Context, values map[string]any) templ.Component {\n\treturn templ.NopComponent\n}\n")),
		"is not "+shellSlotRendererSignature)

	// A symbol nothing declares: the generated registry would not compile, and
	// no version range would ever have caught it.
	require.ErrorContains(t, ValidateShellSlotRenderers([]Manifest{contributor("ClerkHeader")}, source(
		"func ClerkHead(_ context.Context, values map[string]string) templ.Component {\n\treturn templ.NopComponent\n}\n")),
		"which no installed payload in that package declares")

	// A method with the right name is not a renderer.
	require.ErrorContains(t, ValidateShellSlotRenderers([]Manifest{contributor("ClerkHead")}, source(
		"type shell struct{}\n\nfunc (shell) ClerkHead(_ context.Context, values map[string]string) templ.Component {\n\treturn templ.NopComponent\n}\n")),
		"which no installed payload in that package declares")

	// Grouped parameters are the same declaration.
	require.NoError(t, ValidateShellSlotRenderers([]Manifest{contributor("Mount")}, source(
		"func Mount(ctx context.Context, values map[string]string) templ.Component {\n\t_, _ = ctx, values\n\treturn templ.NopComponent\n}\n"+
			"func Mount2(ctx context.Context, values map[string]string) templ.Component { return templ.NopComponent }\n")))
}

func presenceAdapter() Manifest {
	m := vendorAdapterManifest("ggg/system/analytics-posthog", "analytics-posthog", "ggg/analytics", "posthog", "managed")
	m.Environment = []EnvironmentVariable{
		{Key: "POSTHOG_API_KEY", Field: "PostHogAPIKey"},
		{Key: "POSTHOG_HOST", Field: "PostHogHost"},
	}
	return m
}

func presenceReader(class FileClass) Manifest {
	return Manifest{ID: "ggg/system/config", Kind: ModuleSystem, Name: "config",
		Files: []ManifestFile{{Source: "internal/config/config.go", Target: "internal/config/config.go", Class: class}}}
}

// The selector this task deleted, and the reason it cannot come back: it gated
// a route on whether a credential happened to be set, in a project where the
// adapter's selection already decides and its absence is a boot refusal.
func TestNoCredentialPresenceSelectorsRefusesTheDeletedShape(t *testing.T) {
	source := func(body string) map[string][]byte {
		return map[string][]byte{"internal/config/config.go": []byte("package config\n\n" + body)}
	}
	modules := func(class FileClass) []Manifest {
		return []Manifest{presenceAdapter(), presenceReader(class)}
	}

	err := ValidateNoCredentialPresenceSelectors(modules(FileClassGo), source(
		"func (c Config) PostHogEnabled() bool { return c.Value(\"POSTHOG_API_KEY\") != \"\" }\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PostHogEnabled")
	assert.Contains(t, err.Error(), "POSTHOG_API_KEY")
	assert.Contains(t, err.Error(), "credential-presence selector")

	// The two-key form (LLMConfigured) and the inverted comparison.
	require.ErrorContains(t, ValidateNoCredentialPresenceSelectors(modules(FileClassGo), source(
		"func (c Config) LLMConfigured() bool {\n\treturn c.Value(\"POSTHOG_API_KEY\") != \"\" && c.Value(\"POSTHOG_HOST\") != \"\"\n}\n")),
		"credential-presence selector")
	require.ErrorContains(t, ValidateNoCredentialPresenceSelectors(modules(FileClassGo), source(
		"func (c Config) Missing() bool { return \"\" == c.Value(\"POSTHOG_HOST\") }\n")),
		"credential-presence selector")

	// The typed-field form is the same defect in the shape most natural for
	// the module that declares the key — and that module is precisely the one
	// allowed to touch the field, so this is the form a well-behaved adapter
	// author would reach for.
	require.ErrorContains(t, ValidateNoCredentialPresenceSelectors(modules(FileClassGo), source(
		"func (c Config) PostHogEnabled() bool { return c.PostHogAPIKey != \"\" }\n")),
		"credential-presence selector")
	require.ErrorContains(t, ValidateNoCredentialPresenceSelectors(modules(FileClassGo), source(
		"func (c Config) Both() bool {\n\treturn c.PostHogAPIKey != \"\" && c.Value(\"POSTHOG_HOST\") != \"\"\n}\n")),
		"credential-presence selector")

	// A test may name a provider's key deliberately: proving the boot matrix
	// reacts to a credential is not branching on one in product code.
	require.NoError(t, ValidateNoCredentialPresenceSelectors(modules(FileClassTest), source(
		"func TestX() bool { return c.Value(\"POSTHOG_API_KEY\") != \"\" }\n")))
}

// The sanctioned by-key read must stay legal: it is what
// ValidateConfigFieldOwnership tells a module that does not declare a key to
// use, and the value being empty is a normal rendering decision.
func TestNoCredentialPresenceSelectorsAllowsOrdinaryReads(t *testing.T) {
	modules := []Manifest{presenceAdapter(),
		Manifest{ID: "ggg/page/settings-account", Kind: ModulePage,
			Files: []ManifestFile{{Source: "internal/web/page.go", Target: "internal/web/page.go", Class: FileClassGo}}}}
	for _, body := range []string{
		// A handler reading a key and branching inline.
		"func (s *Server) handle() {\n\tif host := s.cfg.Value(\"POSTHOG_HOST\"); host != \"\" {\n\t\tuse(host)\n\t}\n}\n",
		// A predicate over a key this adapter does not own.
		"func (c Config) DevBypass() bool { return c.Value(\"DEV_AUTH_BYPASS\") != \"\" }\n",
		// A bool that is not a presence test.
		"func (c Config) Production() bool { return c.Env == \"production\" }\n",
		// A field this adapter does not own.
		"func (c Config) HasURL() bool { return c.AppURL != \"\" }\n",
		// More than one statement: not a named stand-in for selection.
		"func (c Config) Ready() bool {\n\tlog()\n\treturn c.Value(\"POSTHOG_HOST\") != \"\"\n}\n",
	} {
		require.NoErrorf(t, ValidateNoCredentialPresenceSelectors(modules,
			map[string][]byte{"internal/web/page.go": []byte("package web\n\n" + body)}), "body: %s", body)
	}
}
