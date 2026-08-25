package ui

// GalleryFamily groups components into the sections the gallery renders. The
// generated component registry carries these values, so the set here and the
// set the manifests validate against describe the same catalog.
type GalleryFamily string

const (
	GalleryFoundations   GalleryFamily = "foundations"
	GalleryActions       GalleryFamily = "actions"
	GalleryForms         GalleryFamily = "forms"
	GalleryNavigation    GalleryFamily = "navigation"
	GalleryFeedback      GalleryFamily = "feedback"
	GalleryOverlays      GalleryFamily = "overlays"
	GalleryData          GalleryFamily = "data"
	GalleryCommunication GalleryFamily = "communication"
	GalleryLayout        GalleryFamily = "layout"
	GalleryAdvanced      GalleryFamily = "advanced"
)

// GalleryFamilies is every family, in the order the gallery presents them:
// foundations first, then interaction, then composition.
var GalleryFamilies = []GalleryFamily{
	GalleryFoundations, GalleryActions, GalleryForms, GalleryNavigation,
	GalleryFeedback, GalleryOverlays, GalleryData, GalleryCommunication,
	GalleryLayout, GalleryAdvanced,
}

// Value normalizes an unrecognised family to foundations. A component whose
// family is a typo would otherwise vanish from every section of the gallery.
func (f GalleryFamily) Value() GalleryFamily {
	return normalize(f, GalleryFamilies, GalleryFoundations)
}

// Valid reports whether this is a declared family.
func (f GalleryFamily) Valid() bool { return contains(f, GalleryFamilies) }

// ComponentsInFamily returns the installed components of one family, preserving
// the registry's module order.
func ComponentsInFamily(family GalleryFamily) []Component {
	var out []Component
	for _, c := range ComponentRegistry {
		if c.Family == family {
			out = append(out, c)
		}
	}
	return out
}

// ComponentByName returns the installed component with this data-ui value.
func ComponentByName(name string) (Component, bool) {
	for _, c := range ComponentRegistry {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}
