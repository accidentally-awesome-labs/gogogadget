// Package shell renders Clerk's browser surfaces into the shell's slots.
//
// It exists because ggg/system/server used to render them itself: a
// clerk-publishable-key meta tag, a script tag for an asset only this module
// installs, and two clerk-*-slot classes toggled off a provider-named field on
// templates.Page. Deselecting Clerk left that shell with dead mount points and
// no diagnostic, and made the seam unbuildable without knowing a vendor's name.
//
// Everything here is declared as a runtime.slots contribution, so it renders
// only in the environments that select this adapter, and it imports the
// identity seam rather than the templates package — the generated registry
// imports this package, and the arrow only points one way.
package shell

import (
	"context"
	"strings"
	"unicode"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Head renders the clerk-js loader, or nothing when the project selected Clerk
// without a publishable key. values carries this module's own declared
// non-secret configuration; CLERK_SECRET_KEY is never among them, and a
// renderer would have no use for it — anything handed to a renderer reaches
// the document.
//
// Booting clerk.browser.js with an empty key throws in the browser and mounts
// nothing, so an unconfigured selection renders no loader at all. In
// production the key is production_required and boot refuses first.
func Head(_ context.Context, values map[string]string) templ.Component {
	key := values["CLERK_PUBLISHABLE_KEY"]
	if key == "" {
		return templ.NopComponent
	}
	return head(key)
}

// OrgSwitcher and UserButton render the mount roots unconditionally: the
// adapter is selected, so the widgets belong on the page. They stay in the
// document even without a publishable key, because clerk-js's own stylesheet
// then shows the placeholder — a labelled box beats a missing one, and the ids
// are what static/app.js looks up.
//
// The placeholder text comes from the request context, the same rows the
// shell's own fallback reads, so both paths describe the same viewer.
func OrgSwitcher(ctx context.Context, _ map[string]string) templ.Component {
	name := "Organization"
	if org := identity.OrgFrom(ctx); org != nil && org.Name != "" {
		name = org.Name
	}
	return orgSwitcher(name)
}

func UserButton(ctx context.Context, _ map[string]string) templ.Component {
	initial := "?"
	if user := identity.UserFrom(ctx); user != nil {
		initial = avatarInitial(user.Name)
	}
	return userButton(initial)
}

// avatarInitial is the first letter of a display name, upper-cased. Six lines
// duplicated from the shell rather than a new exported seam helper: the value
// is a cosmetic placeholder, and widening internal/identity's contract to
// share it would make every consumer of that seam recompile for a letter.
func avatarInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(unicode.ToUpper(r))
	}
	return "?"
}
