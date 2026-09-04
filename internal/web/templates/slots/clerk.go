// Clerk's browser surfaces, rendered into the shell's slots.
//
// They live here because ggg/system/server used to render them itself: a
// clerk-publishable-key meta tag, a script tag for an asset only this module
// installs, and two clerk-*-slot classes toggled off a provider-named field on
// templates.Page. Deselecting Clerk left that shell with dead mount points and
// no diagnostic, and made the seam unbuildable without knowing a vendor's name.
//
// Everything here is declared as a runtime.slots contribution, so it renders
// only in the environments that select this adapter. The package is the
// shell's declared home for slot renderers — the same arrangement as
// internal/web/templates/ui, where each component module owns its own file —
// and it imports the identity seam rather than the templates package, because
// the generated registry imports this package and the arrow points one way.
package slots

import (
	"context"
	"strings"
	"unicode"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// ClerkHead renders the clerk-js loader, or nothing when the project selected Clerk
// without a publishable key. values carries this module's own declared
// non-secret configuration; CLERK_SECRET_KEY is never among them, and a
// renderer would have no use for it — anything handed to a renderer reaches
// the document.
//
// Booting clerk.browser.js with an empty key throws in the browser and mounts
// nothing, so an unconfigured selection renders no loader at all. In
// production the key is production_required and boot refuses first.
func ClerkHead(_ context.Context, values map[string]string) templ.Component {
	key := values["CLERK_PUBLISHABLE_KEY"]
	if key == "" {
		return templ.NopComponent
	}
	return clerkHead(key)
}

// ClerkOrgSwitcher and ClerkUserButton render the mount roots unconditionally: the
// adapter is selected, so the widgets belong on the page. They stay in the
// document even without a publishable key, because clerk-js's own stylesheet
// then shows the placeholder — a labelled box beats a missing one, and the ids
// are what static/app.js looks up.
//
// The placeholder text comes from the request context, the same rows the
// shell's own fallback reads, so both paths describe the same viewer.
func ClerkOrgSwitcher(ctx context.Context, _ map[string]string) templ.Component {
	name := "Organization"
	if org := identity.OrgFrom(ctx); org != nil && org.Name != "" {
		name = org.Name
	}
	return clerkOrgSwitcher(name)
}

func ClerkUserButton(ctx context.Context, _ map[string]string) templ.Component {
	initial := "?"
	if user := identity.UserFrom(ctx); user != nil {
		initial = clerkAvatarInitial(user.Name)
	}
	return clerkUserButton(initial)
}

// clerkAvatarInitial is the first letter of a display name, upper-cased. Six lines
// duplicated from the shell rather than a new exported seam helper: the value
// is a cosmetic placeholder, and widening internal/identity's contract to
// share it would make every consumer of that seam recompile for a letter.
func clerkAvatarInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(unicode.ToUpper(r))
	}
	return "?"
}
