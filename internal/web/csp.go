package web

import (
	"fmt"
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
	// inheritsDefault marks a directive with no base sources of its own. It is
	// absent from the header until something contributes to it, so default-src
	// governs — and CSP's override rule is why that matters: a rendered
	// `frame-src https://vendor.example` REPLACES default-src for frames
	// rather than adding to it, so every same-origin iframe in the app would
	// stop loading the first time a contribution used one. When contributed
	// to, these render 'self' first.
	inheritsDefault bool
}{
	// This is v0.7.1's order, deliberately. CSP does not care, but a header
	// that reorders under a refactor makes every future diff of it unreadable,
	// and TestClerkSelectedPolicyMatchesTheHardcodedHeader pins the exact
	// bytes the hardcoded assembly produced.
	{directive: "default-src", sources: []string{"'self'"}},
	{directive: "script-src", sources: []string{"'self'"}},
	{directive: "worker-src", sources: []string{"'self'"}},
	{directive: "style-src", sources: []string{"'self'", "'unsafe-inline'"}},
	{directive: "img-src", sources: []string{"'self'", "data:"}},
	{directive: "media-src", inheritsDefault: true},
	{directive: "frame-src", inheritsDefault: true},
	{directive: "font-src", sources: []string{"'self'"}},
	{directive: "connect-src", sources: []string{"'self'"}},
	{directive: "frame-ancestors", sources: []string{"'none'"}},
	{directive: "base-uri", sources: []string{"'self'"}},
	{directive: "form-action", sources: []string{"'self'"}},
}

// cspContributableSource mirrors modkit.ValidateCSPSource: an https origin with
// at most one leading wildcard label, or blob:/data:. Duplicated as a compiled
// pattern rather than imported because internal/web must not depend on the
// registry engine — and because this is the runtime half of the same rule,
// which has to hold for values that came from configuration and therefore
// could not be checked when the plan was made.
var cspContributableSource = regexp.MustCompile(
	`^(?:blob:|data:|https://(?:\*\.)?[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+(?::(?:6553[0-5]|655[0-2][0-9]|65[0-4][0-9]{2}|6[0-4][0-9]{3}|[1-9][0-9]{0,3}))?)$`)

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
				s.cspRejected(id, directive, "",
					"directive is not granted by the contributing module's manifest")
				continue
			}
			for _, source := range sources {
				canonical := canonicalCSPSource(source)
				if !cspContributableSource.MatchString(canonical) {
					s.cspRejected(id, directive, source,
						"source is not contributable: expected an https origin with at most one leading wildcard label, or blob:/data:")
					continue
				}
				added[directive] = append(added[directive], canonical)
			}
		}
	}

	parts := make([]string, 0, len(cspBase))
	for _, entry := range cspBase {
		contributed := added[entry.directive]
		if len(entry.sources) == 0 && len(contributed) == 0 {
			// No base and nothing contributed: not rendered at all, so
			// default-src governs it. That is stricter than an empty directive,
			// and an empty directive is invalid CSP anyway.
			continue
		}
		base := entry.sources
		if entry.inheritsDefault && len(contributed) != 0 {
			// Rendering this directive takes it out from under default-src, so
			// same-origin has to be restated or it is lost.
			base = append([]string{"'self'"}, base...)
		}
		sources := append(append([]string{}, base...), contributed...)
		parts = append(parts, entry.directive+" "+strings.Join(dedupeSources(sources), " "))
	}
	return strings.Join(parts, "; ")
}

// cspRejected records a dropped contribution.
//
// Dropping is the safe direction — a server that refuses to boot over a
// malformed vendor hostname takes the whole site down to protect a widget —
// but a drop that only reaches stdout is a production-only silent failure: the
// case this exists for is a provider origin that fails the grammar, and its
// consequence is that clerk-js cannot refresh the ~60s __session JWT and
// authentication expires a minute after login. So it goes to the reporter as
// well as the log, which is the difference between "drop and log" and "drop
// and someone notices".
func (s *Server) cspRejected(contribution, directive, source, reason string) {
	err := fmt.Errorf("csp contribution %s: %s: %s%s", contribution, directive, reason, cspSourceSuffix(source))
	s.log.Error("csp contribution rejected", "contribution", contribution,
		"directive", directive, "source", source, "reason", reason)
	if s.reporter != nil {
		s.reporter.Capture(err)
	}
}

func cspSourceSuffix(source string) string {
	if source == "" {
		return ""
	}
	return " (" + source + ")"
}

// canonicalCSPSource lower-cases an https source's scheme and host, because
// hostnames are case-insensitive and the header should carry one spelling: two
// cases of one origin would both survive dedupe and read as two permissions.
// It is the only transformation applied to a contributed source — nothing is
// trimmed, so a path or a trailing slash still fails the grammar rather than
// being quietly repaired into something that looks narrower than it is.
func canonicalCSPSource(source string) string {
	if !strings.HasPrefix(strings.ToLower(source), "https://") {
		return source
	}
	host := source[len("https://"):]
	if strings.ContainsAny(host, "/?#") {
		return source
	}
	return "https://" + strings.ToLower(host)
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
