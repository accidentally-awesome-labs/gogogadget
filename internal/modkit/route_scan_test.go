package modkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routeOwner(id string, contributions ...RouteContribution) Manifest {
	return Manifest{ID: id, Kind: ModuleWorkflow, Name: "owner",
		Runtime: RuntimeContributions{Routes: contributions}}
}

func setTheme() RouteContribution {
	return RouteContribution{ID: "appearance.set-theme", Method: "POST", Pattern: "/set-theme",
		Scope: RoutePublic, Package: "internal/web", Handler: "handleSetTheme"}
}

func impersonate() RouteContribution {
	return RouteContribution{ID: "admin.users.impersonate.form", Method: "GET",
		Pattern: "/admin/users/{id}/impersonate", Scope: RouteAdmin,
		Package: "internal/web", Handler: "handleAdminImpersonateForm"}
}

// routeBaseline is the shell's own route. Every fixture installs it, because a
// closure that declares no route at all has no mux and nothing to compare
// against — the same reason ValidateAssetReferences returns early on an empty
// declaration set.
func routeBaseline() Manifest {
	return routeOwner("ggg/system/server", RouteContribution{
		ID: "server.healthz", Method: "GET", Pattern: "/healthz", Scope: RoutePublic,
		Package: "internal/web", Handler: "handleHealthz"})
}

func routeReferrer(id, target, body string, class FileClass) (Manifest, map[string][]byte) {
	return Manifest{ID: id, Kind: ModulePage, Name: "referrer",
			Files: []ManifestFile{{Source: target, Target: target, Class: class}}},
		map[string][]byte{target: []byte(body)}
}

// The incident, reproduced as a unit: a page renders a control that posts to a
// route only a deselected workflow declares.
func TestRouteReferencesRefuseAnUndeclaredRoute(t *testing.T) {
	page, files := routeReferrer("ggg/page/settings-account",
		"internal/web/templates/settings.templ",
		"templ themeChoice() {\n\t@ui.Button(ui.ButtonOpts{HX: ui.HX{Post: \"/set-theme\"}})\n}\n",
		FileClassTempl)

	err := ValidateRouteReferences([]Manifest{page, routeBaseline()}, []Manifest{page, routeBaseline()}, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/set-theme")
	assert.Contains(t, err.Error(), "ggg/page/settings-account")
	assert.Contains(t, err.Error(), "settings.templ:2")
	assert.Contains(t, err.Error(), "no installed module declares")
	// The refusal names both mechanisms, because which one is right depends on
	// whether the referrer is meaningless without the route.
	assert.Contains(t, err.Error(), "templates.RouteAvailable")
	assert.Contains(t, err.Error(), "requires")

	owner := routeOwner("ggg/workflow/appearance", setTheme())
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{page, owner}, []Manifest{page, owner}, files))
}

// The refusal names both sides: the module that targets the route and the fact
// that nothing declares it. Deleting a declaration a template targets is the
// mutation this gate exists for.
func TestRouteReferencesNameBothSidesWhenADeclarationIsDeleted(t *testing.T) {
	page, files := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\t@ui.ButtonLink(ui.ButtonLinkOpts{\n\t\tHref: \"/admin/users/\" + u.UserID + \"/impersonate\",\n\t})\n}\n",
		FileClassTempl)
	owner := routeOwner("ggg/workflow/impersonation", impersonate())

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{page, owner}, []Manifest{page, owner}, files))

	// The same tree with the declaration removed from the owning manifest.
	stripped := routeOwner("ggg/workflow/impersonation")
	err := ValidateRouteReferences(
		[]Manifest{page, stripped, routeBaseline()}, []Manifest{page, stripped, routeBaseline()}, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ggg/page/admin-overview")
	assert.Contains(t, err.Error(), "/admin/users/")
	assert.Contains(t, err.Error(), "admin.templ:3")
}

// Concatenation is the shape a bare-literal scan cannot read: three literals,
// two of which resolve to nothing on their own.
func TestRouteReferencesReconstructConcatenatedPaths(t *testing.T) {
	page, files := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\t@ui.ConfirmAction(ui.ConfirmActionOpts{HX: ui.HX{Post: \"/admin/users/\" + u.UserID + \"/impersonate\"}})\n}\n",
		FileClassTempl)
	owner := routeOwner("ggg/workflow/impersonation", impersonate())

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{page, owner}, []Manifest{page, owner}, files))

	// A printf verb is the same wildcard by another spelling.
	sprintf, sprintfFiles := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\t@ui.Button(ui.ButtonOpts{HX: ui.HX{Post: fmt.Sprintf(\"/admin/users/%s/impersonate\", u.UserID)}})\n}\n",
		FileClassTempl)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{sprintf, owner}, []Manifest{sprintf, owner}, sprintfFiles))
}

// The enumerated exclusions, each in the position that would otherwise read as
// a reference. None of these is a route and none may refuse.
func TestRouteReferencesExcludeEverythingThatIsNotAnAppPath(t *testing.T) {
	body := "templ footer() {\n" +
		"\t<a href=\"https://example.com/help\">help</a>\n" +
		"\t<a href=\"mailto:support@example.com\">mail</a>\n" +
		"\t<a href=\"tel:+15550000\">call</a>\n" +
		"\t<a href=\"//cdn.example.com/x\">cdn</a>\n" +
		"\t<a href=\"#features\">features</a>\n" +
		"\t<a href=\"relative/page\">relative</a>\n" +
		"\t<a href=\"\">empty</a>\n" +
		"\t<a href={ item.Href }>dynamic</a>\n" +
		"}\n"
	page, files := routeReferrer("ggg/page/home", "internal/web/templates/home.templ", body, FileClassTempl)

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{page, routeBaseline()}, []Manifest{page, routeBaseline()}, files))
}

// A comment is documentation, not a promise. markdown-editor.templ documents
// its upload contract by quoting a form element, and that quote was one of the
// two references this scan invented before comments were removed.
func TestRouteReferencesIgnoreComments(t *testing.T) {
	body := "package ui\n\n" +
		"// The caller renders\n" +
		"//\n" +
		"//\t<form id=\"...\" action=\"/upload\" method=\"post\">\n" +
		"//\n" +
		"// outside its own form.\n" +
		"type MediaPickerOpts struct{ UploadForm string }\n"
	component, files := routeReferrer("ggg/component/markdown-editor",
		"internal/web/templates/ui/markdown-editor.templ", body, FileClassTempl)

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{component, routeBaseline()}, []Manifest{component, routeBaseline()}, files))
}

// A content type declares paths, not routes, and the router expands them. A
// scan that read only runtime.routes would call /blog/{slug} undeclared and
// refuse the page that links to it.
func TestRouteReferencesResolveContentTypePaths(t *testing.T) {
	page, files := routeReferrer("ggg/page/blog", "internal/web/templates/blog.templ",
		"templ list(posts []post) {\n\t@ui.Link(ui.LinkOpts{Href: \"/blog/\" + post.Slug})\n}\n",
		FileClassTempl)
	content := Manifest{ID: "ggg/system/content", Kind: ModuleSystem, Name: "content",
		Runtime: RuntimeContributions{ContentTypes: []ContentTypeContribution{{
			ID: "post", Package: "internal/web", Mode: ContentModePages, Paths: []string{"/blog"},
		}}}}

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{page, content}, []Manifest{page, content}, files))
}

// ServeMux pattern semantics, which the matcher must not approximate: {$}
// anchors an exact path and is not a wildcard that swallows any segment.
func TestRouteReferencesHonourExactAndSubtreePatterns(t *testing.T) {
	root := routeOwner("ggg/page/home", RouteContribution{
		ID: "home.index", Method: "GET", Pattern: "/{$}", Scope: RoutePublic,
		Package: "internal/web", Handler: "handleHome"})
	assets := routeOwner("ggg/system/static", RouteContribution{
		ID: "static.assets", Method: "GET", Pattern: "/static/", Scope: RoutePublic,
		Package: "internal/web", Handler: "handleStatic"})

	home, homeFiles := routeReferrer("ggg/system/server", "internal/web/templates/nav.templ",
		"templ logo() {\n\t<a href=\"/\">home</a>\n}\n", FileClassTempl)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{home, root, assets}, []Manifest{home, root, assets}, homeFiles))

	// /{$} must not resolve an arbitrary one-segment path.
	upload, uploadFiles := routeReferrer("ggg/system/server", "internal/web/templates/nav.templ",
		"templ form() {\n\t<form action=\"/upload\"></form>\n}\n", FileClassTempl)
	require.Error(t, ValidateRouteReferences(
		[]Manifest{upload, root, assets}, []Manifest{upload, root, assets}, uploadFiles))

	// A trailing slash is a subtree and covers everything under it.
	asset, assetFiles := routeReferrer("ggg/system/server", "internal/web/templates/layouts.templ",
		"templ head() {\n\t<link href=\"/static/app.css\"/>\n}\n", FileClassTempl)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{asset, root, assets}, []Manifest{asset, root, assets}, assetFiles))
}

// A mistyped route id must NOT refuse the plan, however wrong it is. A
// derivative's catalog is the core registry pruned to what its profile can
// reach, so an id naming a module the project cannot see is indistinguishable
// from a gate doing its job. Existence is asserted over the published catalog
// in route_scan_selfhost_test.go instead.
func TestRouteReferencesDoNotRefuseAnUnknownRouteID(t *testing.T) {
	shell, files := routeReferrer("ggg/system/server", "internal/web/templates/nav.templ",
		"templ themeToggle() {\n\tif RouteAvailable(\"appearance.set-thmee\") {\n\t\t@ui.ThemeToggle(ui.ThemeToggleOpts{})\n\t}\n}\n",
		FileClassTempl)
	owner := routeOwner("ggg/workflow/appearance", setTheme())

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{shell, owner}, []Manifest{shell, owner}, files))
}

// The id alphabet comes from the CATALOG, not the installed graph. A gate whose
// route is deselected is the gate working; refusing it would defeat the remedy.
func TestRouteReferencesAcceptAGateOnADeselectedRoute(t *testing.T) {
	shell, files := routeReferrer("ggg/system/server", "internal/web/templates/nav.templ",
		"templ themeToggle() {\n\tif RouteAvailable(\"appearance.set-theme\") {\n\t\t@ui.ThemeToggle(ui.ThemeToggleOpts{})\n\t}\n}\n",
		FileClassTempl)
	owner := routeOwner("ggg/workflow/appearance", setTheme())

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{shell}, []Manifest{shell, owner}, files))
}

// Arity, because too few arguments leave a literal {id} in the URL and too
// many are silently dropped. Both are invisible until somebody clicks.
func TestRouteReferencesCheckRoutePathArity(t *testing.T) {
	owner := routeOwner("ggg/workflow/impersonation", impersonate())

	right, rightFiles := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\t@ui.ButtonLink(ui.ButtonLinkOpts{Href: RoutePath(\"admin.users.impersonate.form\", u.UserID)})\n}\n",
		FileClassTempl)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{right, owner}, []Manifest{right, owner}, rightFiles))

	wrong, wrongFiles := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\t@ui.ButtonLink(ui.ButtonLinkOpts{Href: RoutePath(\"admin.users.impersonate.form\")})\n}\n",
		FileClassTempl)
	err := ValidateRouteReferences([]Manifest{wrong, owner}, []Manifest{wrong, owner}, wrongFiles)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0 path argument(s)")
	assert.Contains(t, err.Error(), "/admin/users/{id}/impersonate")

	// RouteAvailable takes no path arguments, so it is never an arity site.
	gate, gateFiles := routeReferrer("ggg/page/admin-overview",
		"internal/web/templates/admin.templ",
		"templ row(u user) {\n\tif RouteAvailable(\"admin.users.impersonate.form\") {\n\t\t@ui.Text(ui.TextOpts{})\n\t}\n}\n",
		FileClassTempl)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{gate, owner}, []Manifest{gate, owner}, gateFiles))
}

// The typed field alphabet is enumerated rather than suffix-matched, and the
// boundary is the package that renders markup: BaseURL means "where to send
// this request" on a ui component and "which vendor host to call" on a provider
// client, and only the first is a route.
func TestRouteReferencesScopeTypedFieldsToTheRenderPackage(t *testing.T) {
	client, files := routeReferrer("ggg/system/llm-openai-compatible",
		"internal/llm/openai/openai.go",
		"package openai\n\nfunc newClient() client {\n\treturn client{BaseURL: \"/v1/chat\"}\n}\n",
		FileClassGo)
	require.NoError(t, ValidateRouteReferences(
		[]Manifest{client, routeBaseline()}, []Manifest{client, routeBaseline()}, files))

	fixture, fixtureFiles := routeReferrer("ggg/page/dev-gallery",
		"internal/web/templates/gallery_fixtures.go",
		"package templates\n\nfunc tableOpts() ui.DataTableOpts {\n\treturn ui.DataTableOpts{BaseURL: \"/dev/ui/table/sort\"}\n}\n",
		FileClassGo)
	require.Error(t, ValidateRouteReferences(
		[]Manifest{fixture, routeBaseline()}, []Manifest{fixture, routeBaseline()}, fixtureFiles))
}

// Server-side redirects target routes too, and both helpers take the target as
// their third argument.
func TestRouteReferencesCoverRedirectHelpers(t *testing.T) {
	dashboard := routeOwner("ggg/page/dashboard", RouteContribution{
		ID: "dashboard.index", Method: "GET", Pattern: "/app", Scope: RouteApp,
		Package: "internal/web", Handler: "handleDashboard"})
	workflow, files := routeReferrer("ggg/workflow/impersonation",
		"internal/web/handlers_impersonation.go",
		"package web\n\nfunc (s *Server) exit(w http.ResponseWriter, r *http.Request) {\n\tRedirect(w, r, \"/app\")\n}\n",
		FileClassGo)

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{workflow, dashboard}, []Manifest{workflow, dashboard}, files))

	err := ValidateRouteReferences(
		[]Manifest{workflow, routeBaseline()}, []Manifest{workflow, routeBaseline()}, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/app")
}

// Shipped browser sources fetch routes, and the shell's own script was one of
// the five known cases.
func TestRouteReferencesCoverShippedScripts(t *testing.T) {
	shell, files := routeReferrer("ggg/system/static", "static/app.js",
		"function persistTheme(theme) {\n  fetch(\"/set-theme\", { method: \"POST\" });\n}\n",
		FileClassAsset)
	owner := routeOwner("ggg/workflow/appearance", setTheme())

	require.NoError(t, ValidateRouteReferences(
		[]Manifest{shell, owner}, []Manifest{shell, owner}, files))

	err := ValidateRouteReferences(
		[]Manifest{shell, routeBaseline()}, []Manifest{shell, routeBaseline()}, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "static/app.js:2")
}

// Generated output is rendered FROM the installed set and cannot disagree with
// it; a test names the paths its own fixture invents; a vendored bundle's bytes
// are a third party's.
func TestRouteReferencesSkipGeneratedTestAndVendoredPayloads(t *testing.T) {
	for _, skipped := range []struct {
		target string
		class  FileClass
	}{
		{"internal/web/templates/chrome_registry_gen.go", FileClassGenerated},
		{"internal/web/handlers_test.go", FileClassTest},
		{"e2e/appearance.spec.ts", FileClassTest},
		{"static/vendor/htmx.min.js", FileClassAsset},
	} {
		module, files := routeReferrer("ggg/system/server", skipped.target,
			"fetch(\"/nothing-declares-this\");\nvar href = \"/nothing-declares-this\";\n", skipped.class)
		require.NoError(t, ValidateRouteReferences(
			[]Manifest{module, routeBaseline()}, []Manifest{module, routeBaseline()}, files),
			"%s must not be scanned", skipped.target)
	}
}
