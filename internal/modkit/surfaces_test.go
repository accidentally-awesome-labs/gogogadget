package modkit

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// surfaceFixture is a two-module catalog: the dev catalog module declares the
// scenarios and its own reference sheet, a product page module declares a
// persona-authenticated surface with a mask. Both halves matter, because the
// emitter has to derive family and scenario entries itself and carry the page
// entries through verbatim.
func surfaceFixture() (Lock, []Manifest) {
	mods := []Manifest{
		{
			ID: "page/dev-gallery", Kind: ModulePage, Name: "dev-gallery", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{
				Scenarios: []ScenarioContribution{
					{
						Slug: "billing", Title: "Billing", Summary: "Plans and quotas.",
						Layout: "app", Surfaces: []string{"plan-card"}, States: []string{"default", "error"},
					},
					{
						Slug: "system-states", Title: "System states", Summary: "Terminal pages.",
						Layout: "public", Surfaces: []string{"terminal-page"}, States: []string{"default"},
					},
				},
				Visual: []VisualContribution{
					{ID: "gallery", Path: "/dev/gallery", FullPage: true, Viewports: []string{"desktop"}},
				},
			},
		},
		{
			ID: "page/projects", Kind: ModulePage, Name: "projects", Revision: 1, Contract: 1,
			Personas: []PersonaContribution{{ID: "pro", User: "user_pro", Org: "org_pro", Role: "org:admin"}},
			Runtime: RuntimeContributions{
				Visual: []VisualContribution{
					{
						ID: "projects", Path: "/app/projects", Persona: "pro",
						Viewports: []string{"desktop"}, Masks: []string{`[data-testid="relative-time"]`},
					},
				},
			},
		},
	}
	lock := Lock{
		Order:   []string{"page/dev-gallery", "page/projects"},
		Modules: []LockedModule{{ID: "page/dev-gallery"}, {ID: "page/projects"}},
	}
	return lock, mods
}

// A scenario's layout selects a real shell renderer. An unknown value would
// reach Page.Layout, which has no branch for it, so the scenario would render
// in whatever the fallback shell is and the baseline would compare chrome no
// product page ships.
func TestScenarioLayoutMustBeAKnownShell(t *testing.T) {
	valid := []ScenarioContribution{{
		Slug: "billing", Title: "Billing", Summary: "Plans.", Layout: "app",
		Surfaces: []string{"plan-card"}, States: []string{"default"},
	}}
	if err := validateScenarios(valid, true); err != nil {
		t.Fatalf("declared scenario must validate: %v", err)
	}
	// "docs" is a layout the renderer genuinely has, which is exactly why it is
	// the mutation worth testing: only the scenario vocabulary refuses it.
	for _, layout := range []string{"docs", "", "App"} {
		mutated := []ScenarioContribution{valid[0]}
		mutated[0].Layout = layout
		err := validateScenarios(mutated, true)
		if err == nil {
			t.Fatalf("layout %q must be refused", layout)
		}
		if !strings.Contains(err.Error(), "layout") {
			t.Fatalf("error must name the layout field: %v", err)
		}
	}
}

// A scenario's states are the query values the page accepts and the reference
// renders. A value outside the closed set names a state nothing can produce, so
// the control would exist and change nothing.
func TestScenarioStateMustBeAKnownRenderingState(t *testing.T) {
	base := ScenarioContribution{
		Slug: "billing", Title: "Billing", Summary: "Plans.", Layout: "app",
		Surfaces: []string{"plan-card"}, States: []string{"default", "error"},
	}
	// "hover" is applied by the browser, not by a renderer: it is the exact
	// value an author would reach for and the one that must not be accepted.
	mutated := base
	mutated.States = []string{"default", "hover"}
	err := validateScenarios([]ScenarioContribution{mutated}, true)
	if err == nil {
		t.Fatal("state \"hover\" must be refused")
	}
	if !strings.Contains(err.Error(), "hover") {
		t.Fatalf("error must name the rejected state: %v", err)
	}
	// The same shape with a declared state passes, so the refusal is about the
	// vocabulary rather than about states being present at all.
	if err := validateScenarios([]ScenarioContribution{base}, true); err != nil {
		t.Fatalf("declared states must validate: %v", err)
	}
}

// Canonical manifests are sorted so the generated descriptor table, the catalog
// index and the visual matrix all walk one order. An unsorted declaration would
// silently reorder the baselines a later sync compares.
func TestScenariosMustBeSortedWhenCanonical(t *testing.T) {
	unsorted := []ScenarioContribution{
		{Slug: "team", Title: "Team", Summary: "Members.", Layout: "app",
			Surfaces: []string{"avatar"}, States: []string{"default"}},
		{Slug: "billing", Title: "Billing", Summary: "Plans.", Layout: "app",
			Surfaces: []string{"plan-card"}, States: []string{"default"}},
	}
	if err := validateScenarios(unsorted, true); err == nil {
		t.Fatal("canonical manifests must refuse unsorted scenarios")
	} else if !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("error must say the list is unsorted: %v", err)
	}
	// A locked snapshot is validated non-canonically, where order is whatever
	// was recorded; the same input must pass there or every existing lock would
	// start failing.
	if err := validateScenarios(unsorted, false); err != nil {
		t.Fatalf("non-canonical validation must accept recorded order: %v", err)
	}
	// Duplicate slugs are refused in both modes: the slug is a URL and a
	// baseline file name, so two of them is never recoverable.
	duplicate := []ScenarioContribution{unsorted[1], unsorted[1]}
	if err := validateScenarios(duplicate, false); err == nil {
		t.Fatal("duplicate scenario slugs must be refused")
	}
}

// tsSurface is one parsed entry of the emitted matrix.
type tsSurface struct {
	id, kind, path, persona string
	fullPage                bool
	viewports, masks        []string
}

var tsSurfaceLine = regexp.MustCompile(
	`^  \{ id: '([^']*)', kind: '([^']*)', path: '([^']*)', fullPage: (true|false), ` +
		`viewports: \[([^\]]*)\], persona: '([^']*)', masks: \[(.*)\] \},$`)

// parseEmittedSurfaces reads the emitted TypeScript back. Parsing rather than
// substring-matching is the point: it proves every entry has the exact declared
// shape, so a field the runners destructure cannot go missing.
func parseEmittedSurfaces(t *testing.T, content string) []tsSurface {
	t.Helper()
	for _, want := range []string{
		"// Generated by ggg sync; DO NOT EDIT.",
		"export type SurfaceKind = 'family' | 'scenario' | 'page';",
		"export type Viewport = 'desktop' | 'tablet' | 'mobile';",
		"export const surfaces: Surface[] = [",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("emitted matrix is missing %q:\n%s", want, content)
		}
	}
	for _, field := range []string{"id: string;", "kind: SurfaceKind;", "path: string;",
		"fullPage: boolean;", "viewports: Viewport[];", "persona: string;", "masks: string[];"} {
		if !strings.Contains(content, "  "+field) {
			t.Fatalf("Surface interface is missing %q", field)
		}
	}

	var out []tsSurface
	body := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case line == "export const surfaces: Surface[] = [":
			body = true
			continue
		case line == "];":
			body = false
			continue
		}
		if !body {
			continue
		}
		m := tsSurfaceLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("entry does not have the declared shape: %q", line)
		}
		out = append(out, tsSurface{
			id: m[1], kind: m[2], path: m[3], fullPage: m[4] == "true",
			viewports: splitTSStrings(m[5]), persona: m[6], masks: splitTSStrings(m[7]),
		})
	}
	return out
}

func splitTSStrings(list string) []string {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	parts := strings.Split(list, ", ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(p, "'"))
	}
	return out
}

// The matrix is the only statement of what the comparison job covers. If it
// dropped a family, mislabelled a kind or lost a mask, the run would still pass
// while covering less than it claims.
func TestVisualSurfacesEmitsDeclaredMatrix(t *testing.T) {
	lock, mods := surfaceFixture()
	f, err := emitVisualSurfaces(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if f.Path != "e2e/generated/surfaces.ts" {
		t.Fatalf("path = %s", f.Path)
	}
	surfaces := parseEmittedSurfaces(t, f.Content)

	byKind := map[string]int{}
	for _, s := range surfaces {
		byKind[s.kind]++
	}
	if byKind["family"] != len(GalleryFamilies) {
		t.Fatalf("family surfaces = %d, want %d", byKind["family"], len(GalleryFamilies))
	}
	if byKind["scenario"] != 2 || byKind["page"] != 2 {
		t.Fatalf("scenario/page surfaces = %d/%d, want 2/2", byKind["scenario"], byKind["page"])
	}

	// Ordering is families, then scenarios, then pages. The runners key
	// baselines on position-independent ids, but a stable order keeps the diff
	// of a regenerated file readable.
	if surfaces[0].id != "family-foundations" || surfaces[len(GalleryFamilies)-1].id != "family-advanced" {
		t.Fatalf("families are not in GalleryFamilies order: %v", surfaces[0].id)
	}
	got := map[string]tsSurface{}
	for _, s := range surfaces {
		got[s.id] = s
	}

	family := got["family-actions"]
	if family.path != "/dev/gallery/actions" || !family.fullPage ||
		family.persona != "" || len(family.masks) != 0 ||
		len(family.viewports) != 1 || family.viewports[0] != "desktop" {
		t.Fatalf("family sheet is not a full-page anonymous desktop reference: %+v", family)
	}

	// An app scenario is compared as an administrator, because half of what it
	// composes only renders for one, and at all three widths: the shell changes
	// shape at both breakpoints, so a desktop-only capture proves nothing about
	// the layout most of its users see.
	app := got["scenario-billing"]
	if app.path != "/dev/scenarios/billing" || app.fullPage ||
		app.persona != "admin" || len(app.masks) != 0 ||
		strings.Join(app.viewports, ",") != "desktop,tablet,mobile" {
		t.Fatalf("app scenario is not a three-viewport administrator fold: %+v", app)
	}
	// A public scenario has no session; signing in would change the chrome it
	// is there to show.
	if public := got["scenario-system-states"]; public.persona != "" {
		t.Fatalf("public scenario must be anonymous: %+v", public)
	}

	page := got["projects"]
	if page.path != "/app/projects" || page.persona != "pro" || page.fullPage ||
		len(page.masks) != 1 || page.masks[0] != `[data-testid="relative-time"]` {
		t.Fatalf("page surface did not carry its declaration: %+v", page)
	}
	if sheet := got["gallery"]; !sheet.fullPage || sheet.persona != "" {
		t.Fatalf("gallery sheet must stay a full-page anonymous capture: %+v", sheet)
	}
}

// A persona is how the capture authenticates. Naming one the fixtures never
// seed would silently record a redirect to the login page as the baseline for a
// product surface.
func TestVisualSurfaceRefusesUndeclaredPersona(t *testing.T) {
	lock, mods := surfaceFixture()
	mods[1].Runtime.Visual[0].Persona = "ghost"
	if _, err := emitVisualSurfaces(context.Background(), "example.com/app", lock, mods); err == nil {
		t.Fatal("an undeclared persona must be refused")
	} else if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("error must name the persona: %v", err)
	}
}

// Two modules claiming one baseline name would compare two different pages
// against one committed screenshot, and whichever lost would look like a
// regression in the page that never changed.
func TestVisualSurfaceIDsAreExclusive(t *testing.T) {
	lock, mods := surfaceFixture()
	mods[1].Runtime.Visual[0].ID = "gallery"
	if _, err := emitVisualSurfaces(context.Background(), "example.com/app", lock, mods); err == nil {
		t.Fatal("a contested surface id must be refused")
	} else if !strings.Contains(err.Error(), "gallery") {
		t.Fatalf("error must name the contested surface: %v", err)
	}
}

// Every committed screenshot has to be reachable from the matrix. A renamed or
// deleted declaration would orphan the baseline: the file would sit in the tree
// forever while the surface it guards went uncompared.
func TestCommittedBaselinesAreAllDeclared(t *testing.T) {
	root := filepath.Join("..", "..")
	emitted, err := os.ReadFile(filepath.Join(root, "e2e", "generated", "surfaces.ts"))
	if err != nil {
		t.Fatalf("read generated matrix: %v", err)
	}
	declared := map[string]struct{}{}
	for _, s := range parseEmittedSurfaces(t, string(emitted)) {
		declared[s.id] = struct{}{}
	}

	entries, err := os.ReadDir(filepath.Join(root, "e2e", "visual.spec.ts-snapshots"))
	if err != nil {
		t.Fatalf("read baselines: %v", err)
	}
	var orphans []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".png") {
			continue
		}
		stem := name
		for _, suffix := range []string{"-light-chromium-linux.png", "-dark-chromium-linux.png"} {
			stem = strings.TrimSuffix(stem, suffix)
		}
		if stem == name {
			continue
		}
		if _, ok := declared[stem]; !ok {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		t.Fatalf("committed baselines no surface declares: %v", orphans)
	}
}
