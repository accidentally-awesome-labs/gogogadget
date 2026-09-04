// Package shell renders PostHog's browser surfaces into the shell's slots.
//
// PostHog had the identical leak to Clerk — a ph-key meta tag, two script tags
// and a consent banner, all rendered by ggg/system/server off a
// provider-named field on templates.Page — which is what proved the shell was
// missing a mechanism rather than merely being sloppy. Both providers now
// contribute their own head and body markup.
package shell

import (
	"context"

	"github.com/a-h/templ"
)

// Head renders the PostHog loader, or nothing when the project selected
// PostHog without a project key. values carries this module's own declared
// non-secret configuration.
func Head(_ context.Context, values map[string]string) templ.Component {
	key := values["POSTHOG_API_KEY"]
	if key == "" {
		return templ.NopComponent
	}
	return head(key)
}

// Consent renders the consent dialog, on the same condition as the loader:
// without a key nothing is ever captured, so asking for permission would be
// theatre.
func Consent(_ context.Context, values map[string]string) templ.Component {
	if values["POSTHOG_API_KEY"] == "" {
		return templ.NopComponent
	}
	return consent()
}
