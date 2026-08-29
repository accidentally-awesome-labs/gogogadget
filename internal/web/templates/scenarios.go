package templates

import "github.com/a-h/templ"

// Scenario describes one realistic product surface the dev catalog can render.
//
// The list is declared rather than discovered because a scenario is a
// deliberate composition: which components appear together, in which layout,
// under which states. Discovering it from installed modules would produce a
// component index, which the gallery already is.
type Scenario struct {
	// Slug is the URL segment and the identity used in the visual surface
	// matrix, so renaming one is a visible change rather than a silent
	// baseline reset.
	Slug string
	// Title and Summary describe the surface a reader is looking at.
	Title   string
	Summary string
	// Layout is the shell this scenario renders inside: the real
	// PublicLayout/AppLayout/AdminLayout, never a copy of their markup. It is
	// the same string Page.Layout takes, so a scenario cannot name a shell the
	// renderer does not have.
	Layout string
	// Surfaces names the components this scenario is built to exercise. It is
	// the reason the scenario exists, and it is what the visual matrix and the
	// accessibility sweep read to know what they are covering.
	Surfaces []string
	// States are the query values this scenario accepts. A state absent here is
	// refused rather than silently rendering the default, because a URL that
	// looks like it selected something and did not is worse than a 404.
	States []string
	// Render composes the surface. It renders inside #content only: the shell
	// around it comes from the real PublicLayout/AppLayout/AdminLayout, so a
	// scenario cannot drift from the chrome the product actually ships.
	//
	// Nil means the scenario is declared but has no composition yet, which the
	// page states rather than rendering an empty column.
	Render func(GalleryContext) templ.Component
}

// scenarioRenderers is the hand-owned half of a scenario: its composition. The
// descriptor half is declared data, generated into scenarioDescriptors, because
// the visual surface matrix needs it too and a manifest cannot name a Go
// function. This map is the join between them, keyed by the declared slug.
var scenarioRenderers = map[string]func(GalleryContext) templ.Component{
	"analytics":     ScenarioAnalytics,
	"billing":       ScenarioBilling,
	"communication": ScenarioCommunication,
	"content":       ScenarioContent,
	"dashboard":     ScenarioDashboard,
	"developer":     ScenarioDeveloper,
	"operations":    ScenarioOperations,
	"planning":      ScenarioPlanning,
	"resource-list": ScenarioResourceList,
	"settings":      ScenarioSettings,
	"system-states": ScenarioSystemStates,
	"team":          ScenarioTeam,
}

// ScenarioRegistry is every declared dev scenario joined to its composition, in
// the order the manifest declares them — sorted by slug, so the catalog index,
// the visual matrix and the accessibility sweep all walk one stable order and a
// reordered manifest is a reviewable diff rather than a silent baseline shuffle.
var ScenarioRegistry = buildScenarioRegistry()

// buildScenarioRegistry joins the generated descriptors to the hand-owned
// renderers. A descriptor with no renderer still appears, carrying a nil Render
// that the page reports as an unbuilt composition. A renderer with no
// descriptor is the opposite problem — dead code that no URL can reach — so it
// panics at init rather than staying invisible until someone greps for it.
func buildScenarioRegistry() []Scenario {
	registry := make([]Scenario, 0, len(scenarioDescriptors))
	declared := make(map[string]struct{}, len(scenarioDescriptors))
	for _, d := range scenarioDescriptors {
		declared[d.Slug] = struct{}{}
		registry = append(registry, Scenario{
			Slug:     d.Slug,
			Title:    d.Title,
			Summary:  d.Summary,
			Layout:   d.Layout,
			Surfaces: d.Surfaces,
			States:   d.States,
			Render:   scenarioRenderers[d.Slug],
		})
	}
	for slug := range scenarioRenderers {
		if _, ok := declared[slug]; !ok {
			panic("templates: scenario renderer " + slug + " has no declared descriptor")
		}
	}
	return registry
}

// scenarioAxisTestID names an axis option. The state options keep their
// original "state-<value>" ids because tests and the visual matrix already
// reference them.
func scenarioAxisTestID(key, option string) string {
	if key == "state" {
		return "state-" + option
	}
	return key + "-" + option
}

// scenarioContextSummary states what is on screen, so a screenshot carries its
// own axes rather than needing the URL beside it.
func scenarioContextSummary(gc GalleryContext) string {
	content := ContentNormal
	if gc.LongContent {
		content = ContentLong
	}
	return gc.Direction + " · " + content + " content · " + string(gc.Density.Value())
}

// ScenarioBySlug returns the declared scenario for a URL segment. An unknown
// slug is not found rather than falling back to the first scenario: a typo that
// silently renders a different page teaches the reader the wrong thing.
func ScenarioBySlug(slug string) (Scenario, bool) {
	for _, scenario := range ScenarioRegistry {
		if scenario.Slug == slug {
			return scenario, true
		}
	}
	return Scenario{}, false
}

// HasState reports whether this scenario accepts a state. Every scenario accepts
// "default", so an absent query parameter is always valid.
func (s Scenario) HasState(state string) bool {
	if state == "" || state == "default" {
		return true
	}
	for _, candidate := range s.States {
		if candidate == state {
			return true
		}
	}
	return false
}
