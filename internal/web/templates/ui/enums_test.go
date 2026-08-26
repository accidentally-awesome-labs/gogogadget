package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every closed enum must answer an invalid value with a defined default rather
// than passing it through. A passed-through value reaches a class name, and a
// component missing its modifier class is a silent visual break in production.
//
// The defaults are asserted individually because each one is a judgement: the
// inert or quietest option wins, so a typo can never promote a control's
// importance, dress a harmless action as destructive, or submit a form.
func TestEveryEnumNormalizesInvalidValues(t *testing.T) {
	assert.Equal(t, SizeMD, Size("").Value())
	assert.Equal(t, SizeMD, Size("medium").Value())
	assert.Equal(t, SizeMD, Size("MD").Value())

	assert.Equal(t, EmphasisSubtle, Emphasis("").Value())
	assert.Equal(t, EmphasisSubtle, Emphasis("filled").Value())

	assert.Equal(t, ActionGhost, Action("").Value())
	assert.Equal(t, ActionGhost, Action("destructive").Value())

	assert.Equal(t, TypeButton, ButtonType("").Value(),
		"an unset button type must be inert: HTML defaults to submit, which submits the form")
	assert.Equal(t, TypeButton, ButtonType("SUBMIT").Value())

	assert.Equal(t, DensityComfortable, Density("").Value())

	// Layout scales: an unset gap or padding is real (flush), so the zero value
	// stays a declared member rather than normalizing up to a spaced default.
	assert.Equal(t, GapMD, Gap("").Value(),
		"an unset gap must keep the standard spacing every existing caller renders")
	assert.Equal(t, GapMD, Gap("2rem").Value())
	assert.Equal(t, GapNone, Gap("none").Value(), "flush must be expressible")
	assert.Equal(t, PaddingMD, Padding("").Value())
	assert.Equal(t, WidthPage, Width("").Value(),
		"an unset container width must constrain: an unbounded measure is unreadable")
	assert.Equal(t, WidthPage, Width("wide").Value(),
		"there is no --container-wide token, so wide is not a declared measure")
	assert.Equal(t, HeightAuto, Height("").Value())
	assert.Equal(t, RatioAuto, Ratio("").Value())
	assert.Equal(t, SideTop, Side("").Value())
	// LiveOff is a meaningful empty: most regions are not live, and announcing
	// every one of them would make a screen reader unusable.
	assert.Equal(t, ChatRoleAssistant, ChatRole("").Value(),
		"an unattributed message is the assistant's: attributing it to the user "+
			"would put words in their mouth in a transcript")
	assert.Equal(t, DeliveryPending, DeliveryState("").Value(),
		"an unrecognised delivery state must never read as delivered: claiming "+
			"a webhook arrived when nobody knows is the one wrong answer")
	assert.Equal(t, DeliveryPending, DeliveryState("bounced").Value())
	assert.Equal(t, EmptyCard, EmptyVariant("").Value(),
		"an unset empty state is the standalone card: inline would render "+
			"without the border its container expects to supply")
	assert.Equal(t, EmptyCard, EmptyVariant("banner").Value())
	assert.Equal(t, LiveOff, Live("").Value())
	assert.Equal(t, LiveOff, Live("aggressive").Value(),
		"an unrecognised urgency must not become assertive: interrupting a "+
			"screen reader mid-sentence is the most disruptive default available")
	assert.Equal(t, InputTypeText, InputType("").Value(),
		"an unset input type must be text, which is what HTML itself falls back to")
	assert.Equal(t, InputTypeText, InputType("datetime").Value(),
		"an unsupported input type must not reach the attribute: browsers treat "+
			"an unknown type as text but skip the validation the caller expected")
	assert.Equal(t, OrientationHorizontal, Orientation("").Value())
	assert.Equal(t, AlignStart, Align("").Value())
	assert.Equal(t, PlacementBottom, Placement("").Value())
	assert.Equal(t, GalleryFoundations, GalleryFamily("").Value(),
		"a component with a typo'd family must still appear somewhere in the gallery")
	assert.Equal(t, KindNeutral, NormalizeKind(""))

	// SortNone and BreakpointNone are meaningful empties, not missing values.
	assert.Equal(t, SortNone, SortDirection("").Value())
	assert.Equal(t, SortNone, SortDirection("ascending").Value())
	assert.Equal(t, BreakpointNone, Breakpoint("").Value())
	assert.Equal(t, BreakpointNone, Breakpoint("xl").Value())
}

// A declared value must survive normalization untouched, and Valid must agree
// with the declared set. Case and whitespace are deliberately not repaired:
// "Danger" and "danger " are typos, and accepting them would make the closed
// set a suggestion.
func TestDeclaredEnumValuesRoundTrip(t *testing.T) {
	for _, v := range Sizes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Emphases {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Actions {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range ButtonTypes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Kinds {
		assert.Equal(t, v, NormalizeKind(v))
		assert.True(t, v.Valid())
	}
	for _, v := range append([]Density{}, Densities...) {
		assert.True(t, v.Valid())
	}
	for _, v := range Orientations {
		assert.True(t, v.Valid())
	}
	for _, v := range Aligns {
		assert.True(t, v.Valid())
	}
	for _, v := range Placements {
		assert.True(t, v.Valid())
	}
	for _, v := range Gaps {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Paddings {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Widths {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Heights {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Ratios {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Sides {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range ChatRoles {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range DeliveryStates {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	// A question type that normalizes to a text input is the safe default: an
	// unrecognised type must still render a control the user can answer, not
	// nothing.
	assert.Equal(t, QuestionShortText, QuestionType("interpretive-dance").Value())
	for _, v := range QuestionTypes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range EmptyVariants {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Lives {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range InputTypes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range GalleryFamilies {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	// IconName has no Value() on purpose - see its Valid() doc comment. Every
	// registry entry must still answer Valid, and a name outside the registry
	// must not.
	for _, v := range IconNames {
		assert.True(t, v.Valid(), "%q is in IconNames but Valid rejects it", v)
	}
	assert.False(t, IconName("trashcan").Valid(),
		"an unregistered icon name must be rejected, not silently substituted")

	assert.False(t, Size("md ").Valid(), "trailing space is a typo, not a value")
	assert.False(t, Kind("Danger").Valid(), "capitalisation is a typo, not a value")
}

// The declared sets must have no duplicates and no accidental empties, since a
// duplicate would make Valid pass for two spellings of one value.
func TestEnumSetsAreWellFormed(t *testing.T) {
	assertDistinct(t, "Sizes", Sizes)
	assertDistinct(t, "Emphases", Emphases)
	assertDistinct(t, "Actions", Actions)
	assertDistinct(t, "ButtonTypes", ButtonTypes)
	assertDistinct(t, "Kinds", Kinds)
	assertDistinct(t, "Densities", Densities)
	assertDistinct(t, "Orientations", Orientations)
	assertDistinct(t, "Aligns", Aligns)
	assertDistinct(t, "Placements", Placements)
	assertDistinct(t, "SortDirections", SortDirections)
	assertDistinct(t, "Breakpoints", Breakpoints)
	assertDistinct(t, "Gaps", Gaps)
	assertDistinct(t, "Paddings", Paddings)
	assertDistinct(t, "Widths", Widths)
	assertDistinct(t, "Heights", Heights)
	assertDistinct(t, "Ratios", Ratios)
	assertDistinct(t, "Sides", Sides)
}

// The two tests above are hand-listed, so a newly declared enum would escape
// both of them silently - which is exactly how a component option ends up with
// no normalization. This scan makes the declarations authoritative: every
// string enum in this package must carry the full contract and must appear in
// the checks above.
func TestEveryDeclaredEnumIsCovered(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	require.NoError(t, err)

	// A closed enum is a string type with declared constants. A bare string
	// type with no constants (PlanFeature) is free-form text, not a closed set.
	stringTypes := map[string]bool{}
	hasConsts := map[string]bool{}
	setsFor := map[string][]string{}
	methods := map[string]map[string]bool{}
	var testSource string

	for _, pk := range pkg {
		for path, file := range pk.Files {
			if strings.HasSuffix(path, "_test.go") {
				if strings.HasSuffix(path, "enums_test.go") {
					body, readErr := os.ReadFile(path)
					require.NoError(t, readErr)
					testSource = string(body)
				}
				continue
			}
			for _, d := range file.Decls {
				switch decl := d.(type) {
				case *ast.GenDecl:
					for _, spec := range decl.Specs {
						switch sp := spec.(type) {
						case *ast.TypeSpec:
							if id, ok := sp.Type.(*ast.Ident); ok && id.Name == "string" && sp.Name.IsExported() {
								stringTypes[sp.Name.Name] = true
							}
						case *ast.ValueSpec:
							if id, ok := sp.Type.(*ast.Ident); ok && decl.Tok == token.CONST {
								hasConsts[id.Name] = true
							}
							// The declared set is found by element type, never
							// by guessing a plural: "Emphasis" pluralizes to
							// "Emphases", and a naive rule would demand a set
							// that does not and should not exist.
							for i, v := range sp.Values {
								lit, ok := v.(*ast.CompositeLit)
								if !ok {
									continue
								}
								arr, ok := lit.Type.(*ast.ArrayType)
								if !ok {
									continue
								}
								if elem, ok := arr.Elt.(*ast.Ident); ok && i < len(sp.Names) {
									setsFor[elem.Name] = append(setsFor[elem.Name], sp.Names[i].Name)
								}
							}
						}
					}
				case *ast.FuncDecl:
					if decl.Recv == nil || len(decl.Recv.List) == 0 {
						continue
					}
					if id, ok := decl.Recv.List[0].Type.(*ast.Ident); ok {
						if methods[id.Name] == nil {
							methods[id.Name] = map[string]bool{}
						}
						methods[id.Name][decl.Name.Name] = true
					}
				}
			}
		}
	}

	require.NotEmpty(t, stringTypes)
	require.NotEmpty(t, testSource, "enums_test.go must be readable to check coverage")
	checked := 0
	for name := range stringTypes {
		if !hasConsts[name] {
			continue // free-form string type, not a closed set
		}
		// Kind predates the Value()/Valid() pair and normalizes through the
		// exported NormalizeKind, which the checks above already exercise.
		if name == "Kind" {
			continue
		}
		checked++
		// A registry key must not normalize. A style modifier falling back to a
		// default renders something slightly wrong; an icon name falling back
		// renders a different icon, silently mislabelling an action. Valid() is
		// still required so callers can check.
		registryKey := name == "IconName"
		sets := setsFor[name]
		if !assert.NotEmpty(t, sets, "closed enum %s declares no set of its values", name) {
			continue
		}
		if registryKey {
			assert.False(t, methods[name]["Value"],
				"%s is a registry key: normalizing it would substitute a different entry", name)
		} else {
			assert.True(t, methods[name]["Value"], "closed enum %s has no Value() normalizer", name)
		}
		assert.True(t, methods[name]["Valid"], "closed enum %s has no Valid() predicate", name)
		for _, set := range sets {
			assert.Contains(t, testSource, set,
				"closed enum %s is declared but no test in this file exercises %s", name, set)
		}
	}
	assert.GreaterOrEqual(t, checked, 10, "the scan stopped finding the declared enums")
}

func assertDistinct[T ~string](t *testing.T, name string, values []T) {
	t.Helper()
	seen := map[T]bool{}
	for _, v := range values {
		assert.False(t, seen[v], "%s contains %q twice", name, string(v))
		seen[v] = true
	}
	assert.NotEmpty(t, values, "%s is empty", name)
}
