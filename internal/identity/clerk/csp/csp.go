// Package csp returns the Content-Security-Policy sources clerk-js needs.
//
// All three used to be hardcoded in ggg/system/security's header assembly: the
// frontend API origin read by key in connect-src, `https://img.clerk.com`
// written as a literal in img-src, and `blob:` in worker-src — a real CSP
// relaxation every project carried, whether or not it had ever chosen Clerk.
// The first at least degraded to 'self'; the second shipped a vendor's hostname
// in a policy the vendor had nothing to do with, and blocked avatars for any
// project whose provider serves them from somewhere else.
//
// Sources is a pure function of this module's own declared non-secret
// configuration, and the manifest grants exactly the three directives below,
// so reading the manifest tells you the whole blast radius without reading
// this file.
package csp

// Sources maps directive to the sources clerk-js requires.
//
// Each one is load-bearing, and each is here rather than in the seam:
//
//   - connect-src: clerk-js calls the Frontend API to keep the ~60s __session
//     JWT fresh. Without the origin the refresh is blocked and authentication
//     expires a minute after login. The value is derived at config load
//     (internal/identity/clerkurl) — `https://clerk.<host>` in production, the
//     Clerk development wildcard otherwise, which is why one leading wildcard
//     label is a legitimate shape for a contributed origin.
//   - img-src: the UserButton and OrganizationSwitcher render avatars from
//     Clerk's image CDN.
//   - worker-src: clerk-js v5 runs its session handshake inside a blob: Web
//     Worker. Without blob: the handshake never completes and auth loops
//     forever — reported by clerk-js at integration, which is how the seam
//     came to carry it for everyone.
//
// An empty frontend API URL contributes nothing rather than an empty source:
// selection is the gate, and a selected-but-unconfigured provider must not
// widen the policy with a stray space.
func Sources(values map[string]string) map[string][]string {
	sources := map[string][]string{
		"img-src":    {"https://img.clerk.com"},
		"worker-src": {"blob:"},
	}
	if frontendAPI := values["CLERK_FRONTEND_API_URL"]; frontendAPI != "" {
		sources["connect-src"] = []string{frontendAPI}
	}
	return sources
}
