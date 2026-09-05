package web

import (
	"regexp"
	"sort"
	"strings"
)

// CSPSourceProvider is the contract a module's Content-Security-Policy
// contribution satisfies.
//
// values are the CONTRIBUTING module's own declared non-secret configuration,
// resolved from the generated key list — the same rule the shell slots use,
// and the reason this seam holds no provider field. The return is keyed by
// directive; a key the manifest did not grant is refused at plan time, and
// again here.
//
// No context: CSP is a per-deployment decision, so the header is composed once
// at server construction rather than rebuilt per request.
type CSPSourceProvider func(values map[string]string) map[string][]string

// cspBase is the policy this framework holds regardless of what is installed.
// Three directives are absent from ContributableCSPDirectives on purpose:
// script-src is 'self' and nothing else, default-src is the fallback the rest
// narrow, and base-uri/form-action/frame-ancestors are navigation controls. A
// module may add sources to the others; nothing may weaken these.
var cspBase = []struct {
	directive string
	sources   []string
}{
	{"default-src", []string{"'self'"}},
	{"script-src", []string{"'self'"}},
	{"style-src", []string{"'self'", "'unsafe-inline'"}},
	{"img-src", []string{"'self'", "data:"}},
	{"font-src", []string{"'self'"}},
	{"connect-src", []string{"'self'"}},
	{"worker-src", []string{"'self'"}},
	{"media-src", nil},
	{"frame-src", nil},
	{"frame-ancestors", []string{"'none'"}},
	{"base-uri", []string{"'self'"}},
	{"form-action", []string{"'self'"}},
}

// cspContributableSource mirrors modkit.ValidateCSPSource: an https origin with
// at most one leading wildcard label, or blob:/data:. Duplicated as a compiled
// pattern rather than imported because internal/web must not depend on the
// registry engine — and because this is the runtime half of the same rule,
// which has to hold for values that came from configuration and therefore
// could not be checked when the plan was made.
var cspContributableSource = regexp.MustCompile(
	`^(?:blob:|data:|https://(?:\*\.)?[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+(?::[0-9]{1,5})?)$`)

// contentSecurityPolicy composes the header from the base policy plus every
// active contribution.
//
// Composition rules, all of them one-directional: a contribution may only add,
// only to a directive its manifest granted, and only a source that satisfies
// the grammar. An invalid source is dropped and reported rather than served —
// dropping is the safe direction, and a policy that refuses to boot over a
// malformed vendor hostname would take the whole site down to protect a
// widget. Order is the base order with contributions appended in installed
// order, deduplicated, so the header is stable and testable.
func (s *Server) contentSecurityPolicy() string {
	added := make(map[string][]string, len(cspBase))
	for _, id := range sortedCSPContributions() {
		if active, ok := CSPActive[id]; ok && !active(s.cfg.Env) {
			continue
		}
		provider := CSPSourceProviders[id]
		if provider == nil {
			continue
		}
		granted := make(map[string]struct{}, len(CSPDirectiveGrants[id]))
		for _, directive := range CSPDirectiveGrants[id] {
			granted[directive] = struct{}{}
		}
		values := make(map[string]string, len(CSPValueKeys[id]))
		for _, key := range CSPValueKeys[id] {
			values[key] = s.cfg.Value(key)
		}
		for _, directive := range sortedKeysOf(provider(values)) {
			sources := provider(values)[directive]
			if _, ok := granted[directive]; !ok {
				s.log.Error("csp contribution returned an ungranted directive",
					"contribution", id, "directive", directive)
				continue
			}
			for _, source := range sources {
				if !cspContributableSource.MatchString(source) {
					s.log.Error("csp contribution returned a source that may not be contributed",
						"contribution", id, "directive", directive, "source", source)
					continue
				}
				added[directive] = append(added[directive], source)
			}
		}
	}

	parts := make([]string, 0, len(cspBase))
	for _, entry := range cspBase {
		sources := append(append([]string{}, entry.sources...), added[entry.directive]...)
		if len(sources) == 0 {
			// A directive with no base and no contribution is not rendered at
			// all, so default-src governs it — which is stricter than an empty
			// directive, and an empty directive is invalid CSP anyway.
			continue
		}
		parts = append(parts, entry.directive+" "+strings.Join(dedupeSources(sources), " "))
	}
	return strings.Join(parts, "; ")
}

// dedupeSources keeps the first occurrence of each source, so a contribution
// that repeats what the base already allows cannot lengthen the header.
func dedupeSources(sources []string) []string {
	seen := make(map[string]struct{}, len(sources))
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		out = append(out, source)
	}
	return out
}

func sortedCSPContributions() []string {
	ids := make([]string, 0, len(CSPSourceProviders))
	for id := range CSPSourceProviders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedKeysOf(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
