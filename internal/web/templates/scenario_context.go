package templates

import "github.com/gogogadget/gogogadget/internal/web/templates/ui"

// The closed sets each axis accepts. They are closed for the same reason the
// component enums are: a value nothing renders is a control that lies, and the
// reviewer only finds out by comparing two screenshots that look the same.
const (
	DirectionLTR = "ltr"
	DirectionRTL = "rtl"

	ContentNormal = "normal"
	ContentLong   = "long"
)

// ScenarioContextFrom validates the query axes and builds the render context.
//
// It returns false rather than correcting a bad value. Falling back to the
// default would render a surface the URL does not describe, which is worse than
// a 404: the reader believes they are looking at the state they asked for.
func ScenarioContextFrom(state string, page int, dir, content, density string) (GalleryContext, bool) {
	gc := GalleryContext{State: scenarioActiveState(state), Page: page}

	switch dir {
	case "", DirectionLTR:
		gc.Direction = DirectionLTR
	case DirectionRTL:
		gc.Direction = DirectionRTL
	default:
		return GalleryContext{}, false
	}

	switch content {
	case "", ContentNormal:
	case ContentLong:
		gc.LongContent = true
	default:
		return GalleryContext{}, false
	}

	// Density reuses the product's own enum rather than a parallel set, so a
	// scenario cannot be rendered at a density no component supports.
	switch density {
	case "":
		gc.Density = ui.DensityComfortable
	default:
		parsed := ui.Density(density)
		if !parsed.Valid() {
			return GalleryContext{}, false
		}
		gc.Density = parsed
	}
	return gc, true
}

// ScenarioAxis is one control group on a scenario page: a label, the query key
// it writes, and the values it offers.
type ScenarioAxis struct {
	Label   string
	Key     string
	Options []string
}

// densityAware is the set of components that take a Density. Density is the
// row-spacing axis for tables and lists, so a scenario built from anything else
// cannot respond to it.
// The list is exactly the components whose options struct has a Density field.
// data-grid is deliberately absent: it renders its own table with fixed padding
// and takes no density, so claiming it here would offer an inert control.
var densityAware = map[string]struct{}{
	"data-table":       {},
	"description-list": {},
	"table":            {},
}

// scenarioAxes lists the axes this scenario accepts. States come first because
// they change the surface most.
//
// Density is offered only where the scenario actually holds a density-aware
// component. Showing it everywhere would put a control on ten pages that
// changes nothing on six of them, which is the same lie as a disabled button
// with no explanation: the reader clicks, sees no difference, and concludes the
// axis does not matter.
func scenarioAxes(scenario Scenario) []ScenarioAxis {
	axes := []ScenarioAxis{
		{Label: "State", Key: "state", Options: scenarioStateOptions(scenario)},
		{Label: "Direction", Key: "dir", Options: []string{DirectionLTR, DirectionRTL}},
		{Label: "Content", Key: "content", Options: []string{ContentNormal, ContentLong}},
	}
	if scenarioRespondsToDensity(scenario) {
		axes = append(axes, ScenarioAxis{Label: "Density", Key: "density", Options: densityOptions()})
	}
	return axes
}

func scenarioRespondsToDensity(scenario Scenario) bool {
	for _, surface := range scenario.Surfaces {
		if _, ok := densityAware[surface]; ok {
			return true
		}
	}
	return false
}

func densityOptions() []string {
	out := make([]string, 0, len(ui.Densities))
	for _, density := range ui.Densities {
		out = append(out, string(density))
	}
	return out
}

// scenarioAxisValue reports the currently selected value for an axis, so exactly
// one option in each group is marked current.
func scenarioAxisValue(gc GalleryContext, key string) string {
	switch key {
	case "state":
		return scenarioActiveState(gc.State)
	case "dir":
		if gc.Direction == "" {
			return DirectionLTR
		}
		return gc.Direction
	case "content":
		if gc.LongContent {
			return ContentLong
		}
		return ContentNormal
	case "density":
		return string(gc.Density.Value())
	default:
		return ""
	}
}

// scenarioAxisURL builds the link for one option while preserving every other
// axis. Dropping the others would make the controls mutually exclusive, so a
// reviewer could never look at, say, the empty state in rtl at compact density.
//
// Default values are omitted from the query so each view has one canonical URL:
// a shared link and a clicked link must match, or the two disagree in history.
func scenarioAxisURL(scenario Scenario, gc GalleryContext, key, value string) string {
	values := map[string]string{
		"state":   scenarioAxisValue(gc, "state"),
		"dir":     scenarioAxisValue(gc, "dir"),
		"content": scenarioAxisValue(gc, "content"),
		"density": scenarioAxisValue(gc, "density"),
	}
	values[key] = value

	defaults := map[string]string{
		"state":   "default",
		"dir":     DirectionLTR,
		"content": ContentNormal,
		"density": string(ui.DensityComfortable),
	}
	query := ""
	// Fixed order so the same selection always produces the same URL.
	for _, axis := range []string{"state", "dir", "content", "density"} {
		if values[axis] == defaults[axis] {
			continue
		}
		if query == "" {
			query = "?"
		} else {
			query += "&"
		}
		query += axis + "=" + values[axis]
	}
	return "/dev/scenarios/" + scenario.Slug + query
}
