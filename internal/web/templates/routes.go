package templates

import "strings"

// RouteAvailable reports whether a selected module declares the named route.
//
// This is the render-time half of the rule ValidateRouteReferences enforces at
// plan time: every route a template targets must be declared by a selected
// module. A control whose route no module declares must not render, because a
// button that 404s is worse than an absent one — the user cannot tell the
// difference between a broken product and a product that does not offer the
// feature, and the server has no handler to explain it.
//
// It is the same shape as ShellSlotFilled: the shell asks whether anything is
// there and renders accordingly, rather than depending on the module that would
// fill it. For a control that targets a route, `requires` is usually not even
// available — ggg/workflow/appearance requires ggg/system/server and
// ggg/page/settings-account, and ggg/workflow/impersonation requires
// ggg/page/admin-overview, so an edge from any of those three to the workflow
// whose route they target closes a cycle. Eleven of the fourteen dangling
// reference pairs measured in this tree are cyclic in exactly that way.
func RouteAvailable(id string) bool {
	_, ok := RouteTargets[id]
	return ok
}

// RoutePath resolves a declared route id to a request path, substituting the
// pattern's wildcard segments with args in declared order.
//
// The point is that the pattern lives in one place — the manifest the mux is
// generated from — so a template cannot spell a target the router does not
// serve, and a route that moves moves for every caller at once. It replaces
// string concatenation of the form `"/admin/users/" + u.UserID + "/impersonate"`,
// which restates a declared pattern in a form nothing checks.
//
// An unknown id yields the empty string rather than a guess. Callers gate on
// RouteAvailable first; the empty return is the second line, and it renders a
// control with no target instead of one pointed at the wrong handler.
//
// Too few args leave their `{name}` segments in place, which is a visibly wrong
// URL rather than a silently plausible one. ValidateRouteReferences checks the
// arity of every call site against the declared pattern, so that case does not
// reach a running server.
func RoutePath(id string, args ...string) string {
	pattern, ok := RouteTargets[id]
	if !ok {
		return ""
	}
	segments := strings.Split(pattern, "/")
	// A trailing {$} anchors the pattern to an exact path rather than naming a
	// segment: `/{$}` addresses `/`, and substituting into it would produce a
	// path with a literal wildcard in it.
	if last := len(segments) - 1; last >= 0 && segments[last] == "{$}" {
		segments[last] = ""
	}
	next := 0
	for i, segment := range segments {
		if next >= len(args) || !isRouteWildcard(segment) {
			continue
		}
		segments[i] = args[next]
		next++
	}
	return strings.Join(segments, "/")
}

// isRouteWildcard reports whether a pattern segment names a path value.
//
// The arity rule is enforced in internal/modkit, over the declared pattern,
// because that is where a call site can be refused before it ships; this copy
// is the one the substitution itself needs. A disagreement between them shows
// up as a plan refusal on this tree, not as a wrong URL in a browser.
func isRouteWildcard(segment string) bool {
	return len(segment) > 2 && segment[0] == '{' && segment[len(segment)-1] == '}' && segment != "{$}"
}
