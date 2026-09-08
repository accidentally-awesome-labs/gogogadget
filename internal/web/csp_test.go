package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// policyFor drives the real middleware chain and returns the header a browser
// would receive. Asserting the header rather than the assembly is the point:
// the assembly moved, the bytes must not have.
func policyFor(t *testing.T, environment string, values map[string]string) string {
	t.Helper()
	s := testServer(t, func(cfg *config.Config) {
		cfg.Env = environment
		// The fixture pre-seeds CLERK_FRONTEND_API_URL; a nil values map here
		// means "whatever the fixture has", and an explicit one replaces it so
		// a test can assert the unconfigured case.
		if values != nil {
			cfg.Values = map[string]string{}
			for key, value := range values {
				cfg.Values[key] = value
			}
		}
	})
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/healthz", nil))
	policy := recorder.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, policy, "a page with no policy is a page with no protection")
	return policy
}

// The header a Clerk-selected project serves, compared against the exact
// string v0.7.1's hardcoded assembly produced — directives in that order, with
// that spelling. The literal below is transcribed from the v0.7.1 source
// (middleware.go's strings.Join list), so it is a golden rather than a
// tautology: nothing in this change generated it.
//
// media-src and frame-src joined the table and are absent here because they
// inherit default-src until something contributes to them.
func TestClerkSelectedPolicyMatchesTheHardcodedHeader(t *testing.T) {
	policy := policyFor(t, "production", map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	assert.Equal(t, strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"worker-src 'self' blob:",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https://img.clerk.com",
		"font-src 'self'",
		"connect-src 'self' https://clerk.example.com",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; "), policy)
}

// A project whose identity slot selects another adapter gets no vendor origin,
// no avatar host, and — the one that matters most — no blob: worker source.
// That relaxation was carried unconditionally by every project because one
// vendor's session handshake needs it.
func TestPolicyWithoutClerkCarriesNoVendorSourceAndNoBlob(t *testing.T) {
	policy := policyFor(t, "test", map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	for _, want := range []string{
		"connect-src 'self'", "img-src 'self' data:", "worker-src 'self'",
	} {
		assert.Contains(t, policy, want+";", "directive %q must stand alone", want)
	}
	assert.NotContains(t, policy, "img.clerk.com")
	assert.NotContains(t, policy, "blob:")
	assert.NotContains(t, strings.ToLower(policy), "clerk")

	// The posture is unchanged in both directions.
	assert.Contains(t, policy, "script-src 'self';")
	assert.NotContains(t, policy, "unsafe-eval")
}

// A contribution may only add to a directive its manifest granted, and only a
// source the grammar allows. Both are plan-time refusals; this is the runtime
// half, which has to hold because a source can arrive from configuration and
// therefore could not be checked when the plan was made.
func TestPolicyDropsUngrantedDirectivesAndInvalidSources(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"rogue": func(map[string]string) map[string][]string {
			return map[string][]string{
				// Granted, and every source refused for a different reason.
				"img-src": {
					"'unsafe-inline'", "*", "http://plain.example.com",
					"https://*.*.example.com", "https://ok.example.com",
				},
				// Not granted at all.
				"script-src": {"https://cdn.example.com"},
			}
		},
	}
	CSPDirectiveGrants = map[string][]string{"rogue": {"img-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "img-src 'self' data: https://ok.example.com;",
		"the one valid source is added and the rest are dropped")
	assert.Contains(t, policy, "script-src 'self';", "an ungranted directive is never widened")

	// Scoped to the directive that WAS granted: 'unsafe-inline' legitimately
	// appears in the base style-src, so asserting over the whole header would
	// pass for the wrong reason or fail for one.
	imgSrc := ""
	for _, part := range strings.Split(policy, "; ") {
		if strings.HasPrefix(part, "img-src ") {
			imgSrc = part
		}
	}
	require.NotEmpty(t, imgSrc)
	for _, refused := range []string{"unsafe-inline", "http://", "*.*.", " * "} {
		assert.NotContains(t, imgSrc, refused, "img-src must carry only the valid source")
	}
	assert.NotContains(t, policy, "cdn.example.com", "an ungranted directive's sources never appear")
}

// A directive with no base sources and no contribution is not rendered at all,
// so default-src governs it. An empty directive is invalid CSP and a rendered
// one would be looser than absence.
func TestPolicyOmitsDirectivesNothingContributesTo(t *testing.T) {
	policy := policyFor(t, "test", map[string]string{})

	assert.NotContains(t, policy, "media-src")
	assert.NotContains(t, policy, "frame-src")
	assert.NotContains(t, policy, ";;")
	assert.False(t, strings.HasSuffix(policy, ";"))
	for _, part := range strings.Split(policy, "; ") {
		assert.NotEmpty(t, strings.TrimSpace(part))
		assert.Equal(t, len(strings.Fields(part)), len(strings.Split(part, " ")),
			"no directive may carry a double space: %q", part)
	}
}

// The header is composed once, so two requests cannot disagree and a
// contribution cannot observe a request.
func TestPolicyIsStableAcrossRequests(t *testing.T) {
	s := testServer(t, func(cfg *config.Config) { cfg.Env = "production" })
	seen := map[string]struct{}{}
	for range 5 {
		recorder := httptest.NewRecorder()
		s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/healthz", nil))
		seen[recorder.Header().Get("Content-Security-Policy")] = struct{}{}
	}
	assert.Len(t, seen, 1)
}

// A directive that inherits default-src must restate 'self' the moment it is
// rendered. CSP's override rule is the whole reason: `frame-src https://vendor`
// REPLACES default-src for frames, so without this every same-origin iframe in
// the app breaks the first time a contribution uses one of these grants.
func TestContributedInheritingDirectivesKeepSelf(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"widget": func(map[string]string) map[string][]string {
			return map[string][]string{
				"frame-src": {"https://widget.example.com"},
				"media-src": {"https://media.example.com"},
			}
		},
	}
	CSPDirectiveGrants = map[string][]string{"widget": {"frame-src", "media-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "media-src 'self' https://media.example.com;")
	assert.Contains(t, policy, "frame-src 'self' https://widget.example.com;")
}

// Hostnames are case-insensitive, so an operator-supplied origin that differs
// only in case must reach the header rather than being dropped — and it must
// arrive in one canonical spelling, so two cases of one origin cannot both
// survive dedupe and read as two permissions.
func TestContributedOriginIsCanonicalisedNotDropped(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"mixed": func(map[string]string) map[string][]string {
			return map[string][]string{"img-src": {
				"https://IMG.Clerk.com", "https://img.clerk.com",
				// Still refused: a path was never part of an origin, and
				// nothing is trimmed into looking narrower than it is.
				"https://img.clerk.com/avatars/",
			}}
		},
	}
	CSPDirectiveGrants = map[string][]string{"mixed": {"img-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "img-src 'self' data: https://img.clerk.com;")
	assert.NotContains(t, policy, "IMG.Clerk.com")
	assert.NotContains(t, policy, "avatars")
}

// The grammar exists twice: modkit.ValidateCSPSource refuses at plan time and
// cspContributableSource enforces at runtime. The runtime copy is the one that
// actually gates what reaches a browser, and nothing fails if it drifts
// LOOSER — so the two must agree, and the agreement has to be checked over a
// population neither copy chose.
//
// It used to be checked against 21 string literals a human wrote down: 6 the
// runtime had to accept, 15 it had to refuse. That makes the dangerous
// direction exactly fifteen literals wide. Loosening the runtime pattern to
// also accept `filesystem:` and `wss?://[a-z0-9.\-]+` — a plaintext-WebSocket
// exfiltration channel in connect-src, refused at plan time — left the whole
// package green, live database and all.
//
// So the corpus is DERIVED from the runtime pattern's own syntax tree: every
// branch it admits is expanded into sample strings, and each sample must be
// accepted by the plan-time validator too. A new alternation in the runtime
// pattern generates its own counter-examples, which is the property a literal
// table cannot have. The mutation pass then walks a dense neighbourhood
// around every accepted sample — one byte deleted, replaced or inserted, from
// an alphabet drawn from the samples themselves plus the separators and
// keyword characters a source may not contain — and requires agreement on
// every string in both directions.
//
// The one direction this cannot enumerate is a plan-time grammar that accepts
// something the runtime pattern's tree can never produce. That is the safe
// direction (the runtime stays strict, so nothing extra reaches a browser),
// and it is held by modkit's own fixtures over its own tables. Narrowing plan
// time instead — dropping blob: from modkit's scheme sources — IS caught
// here, because blob: is a branch of the runtime pattern and therefore in the
// corpus.
//
// internal/web must not depend on the registry engine, and it does not: this
// is a test file, so the edge exists only in the test binary. route_test.go
// already imports modkit for the same reason — the duplication can only be
// held together from the one place that can see both copies.
func TestRuntimeGrammarAgreesWithThePlanTimeGrammar(t *testing.T) {
	pattern, err := syntax.Parse(cspContributableSource.String(), syntax.Perl)
	require.NoError(t, err, "the runtime grammar is no longer a parseable pattern")

	corpus := cspGrammarCorpus(t, pattern)

	accepted, refused := 0, 0
	for _, source := range corpus {
		runtime := cspContributableSource.MatchString(source)
		planTime := modkit.ValidateCSPSource(source) == nil
		if runtime {
			accepted++
		} else {
			refused++
		}
		if runtime == planTime {
			continue
		}
		if runtime {
			assert.Failf(t, "runtime grammar is looser than plan time",
				"cspContributableSource accepts %q and modkit.ValidateCSPSource refuses it: %v\n"+
					"This is the direction that reaches a browser: a source refused when the plan was made "+
					"but accepted at runtime is a permission no review saw. Fix the runtime pattern, or move "+
					"the relaxation into modkit's grammar where the plan-time refusal lives.",
				source, modkit.ValidateCSPSource(source))
			continue
		}
		assert.Failf(t, "runtime grammar is stricter than plan time",
			"modkit.ValidateCSPSource accepts %q and cspContributableSource refuses it.\n"+
				"A contribution that passes the plan and is dropped at runtime is a vendor whose widget "+
				"stops working with only a log line to say why. The two copies are one grammar.",
			source)
	}

	// Floors. An enumerator that collapsed, a pattern that stopped parsing and
	// a mutation pass that produced nothing all report perfect agreement, which
	// is exactly the evidence this test exists to distinguish from the real
	// thing. Both sides of the corpus are required: samples with no mutations
	// would check only the accepted language, and mutations with no samples
	// cannot exist.
	require.Greater(t, accepted, 20,
		"only %d of %d corpus entries are accepted sources; the enumerator has collapsed, not the grammar", accepted, len(corpus))
	require.Greater(t, refused, 500,
		"only %d refused sources generated; the mutation pass has collapsed and the dangerous direction is untested", refused)

	// And the shapes the refusal list used to name by hand must still be in
	// the corpus, so a mutation alphabet that stopped covering them is visible
	// rather than silent.
	for _, shape := range []string{"'unsafe-inline'", "http://plain.example.com", "*", "https://*.*.example.com"} {
		assert.Containsf(t, corpus, shape,
			"the derived corpus no longer contains %q; widen the mutation alphabet", shape)
	}
}

// cspGrammarCorpus is every sample BOTH grammars' syntax trees yield, plus a
// one-byte neighbourhood around each, deduplicated.
//
// Both sides are enumerated because either can be the one that moved. Deriving
// only from the runtime pattern would leave a runtime NARROWING invisible: drop
// blob: from the runtime alternation and no sample the tree produces mentions
// it any more, so the disagreement would generate no counter-example. So the
// plan-time grammar is read out of modkit's own source — the pattern and the
// scheme table it validates against — and every literal it yields is verified
// to be plan-time-accepted before it is trusted, which is what makes the
// extraction self-checking rather than a second hand-written copy.
func cspGrammarCorpus(t *testing.T, pattern *syntax.Regexp) []string {
	t.Helper()
	samples := cspGrammarSamples(pattern, 64)
	require.Greater(t, len(samples), 20,
		"the pattern expanded to %d sample sources; a grammar with an https origin, two schemes and a port range yields more", len(samples))
	samples = append(samples, cspPlanTimeSamples(t)...)

	seen := make(map[string]struct{}, len(samples)*64)
	for _, sample := range samples {
		seen[sample] = struct{}{}
	}
	// The keyword sources, separators and shapes a source may never carry.
	// Fixed on purpose: these are the bytes an attacker reaches for, and they
	// are not derivable from a grammar that excludes them.
	alphabet := []byte("'*/:. -\t\n\r;,?#%@[]<>\\htps01")
	for _, sample := range cspGrammarMutationSeeds(samples, 48) {
		for i := range len(sample) {
			seen[sample[:i]+sample[i+1:]] = struct{}{}
			for _, b := range alphabet {
				seen[sample[:i]+string(b)+sample[i+1:]] = struct{}{}
				seen[sample[:i]+string(b)+sample[i:]] = struct{}{}
			}
		}
		for _, b := range alphabet {
			seen[sample+string(b)] = struct{}{}
		}
		seen[strings.ToUpper(sample)] = struct{}{}
		seen[sample+" "+sample] = struct{}{}
	}
	// The keyword and wildcard sources, spelled out: a one-byte edit of an
	// https origin never reaches 'unsafe-inline', and those are the sources the
	// whole mechanism exists to refuse.
	for _, keyword := range []string{
		"'unsafe-inline'", "'unsafe-eval'", "'self'", "'none'", "'strict-dynamic'", "'unsafe-hashes'",
		"*", "*.example.com", "https://*", "https://*.com", "https://*.*.example.com",
		"http://plain.example.com", "javascript:", "filesystem:", "ws://a.example.com",
		"wss://a.example.com", "", "https://ok.example.com/api",
	} {
		seen[keyword] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for source := range seen {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

// cspPlanTimeSamples expands the plan-time grammar the same way, read out of
// modkit's source because its pattern and scheme table are unexported and
// internal/web takes no compile-time dependency on the engine.
//
// Every extracted literal is checked against ValidateCSPSource before it is
// used, so an extraction that stopped matching what modkit declares fails here
// instead of quietly contributing nothing. Reading fails loudly too: if the
// grammar moves, this parity has to be re-derived against its new shape, and
// silently checking one side is the state this replaced.
func cspPlanTimeSamples(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "modkit", "csp.go"))
	require.NoError(t, err, "modkit's CSP grammar is where the plan-time half lives; this parity cannot be derived without it")
	body := string(source)

	origin := regexp.MustCompile("(?s)cspHTTPSOrigin = regexp.MustCompile\\(\\s*`([^`]+)`").FindStringSubmatch(body)
	require.NotNil(t, origin,
		"modkit no longer declares cspHTTPSOrigin as one backquoted pattern; re-derive this parity against its new shape")
	parsed, parseErr := syntax.Parse(origin[1], syntax.Perl)
	require.NoError(t, parseErr, "modkit's plan-time origin pattern does not parse")
	out := cspGrammarSamples(parsed, 64)
	require.Greater(t, len(out), 20, "modkit's origin pattern expanded to only %d samples", len(out))

	table := regexp.MustCompile(`(?s)cspSchemeSources = map\[string\]struct\{\}\{(.*?)\n\}`).FindStringSubmatch(body)
	require.NotNil(t, table,
		"modkit no longer declares cspSchemeSources as one map literal; re-derive this parity against its new shape")
	schemes := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(table[1], -1)
	require.NotEmpty(t, schemes, "modkit's scheme table extracted empty, so the extraction is what broke, not the grammar")
	for _, scheme := range schemes {
		require.NoErrorf(t, modkit.ValidateCSPSource(scheme[1]),
			"extracted %q from modkit's scheme table and modkit does not accept it, so the extraction is reading the wrong bytes", scheme[1])
		out = append(out, scheme[1])
	}
	return out
}

// cspGrammarMutationSeeds is the sample subset the neighbourhood is grown
// around, bounded so the corpus stays in the tens of thousands rather than the
// millions. Sorted first, so the selection is the same on every run.
func cspGrammarMutationSeeds(samples []string, limit int) []string {
	seeds := slices.Clone(samples)
	sort.Strings(seeds)
	if len(seeds) > limit {
		// Spread across the sorted samples rather than taking a prefix: the
		// prefix of this pattern's expansion is all ports.
		step := len(seeds) / limit
		picked := make([]string, 0, limit)
		for i := 0; i < len(seeds) && len(picked) < limit; i += step {
			picked = append(picked, seeds[i])
		}
		return picked
	}
	return seeds
}

// cspGrammarSamples expands one parsed pattern into strings it matches, at most
// limit per node.
//
// Bounded rather than exhaustive because the accepted language is infinite (a
// hostname has unbounded labels), and bounded per NODE rather than globally so
// every alternation contributes: a new scheme branch is one alternative among
// three and must not be crowded out by the port range.
func cspGrammarSamples(re *syntax.Regexp, limit int) []string {
	switch re.Op {
	case syntax.OpNoMatch:
		return nil
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return []string{""}
	case syntax.OpLiteral:
		return []string{string(re.Rune)}
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return []string{"a"}
	case syntax.OpCharClass:
		return cspCharClassSamples(re.Rune, limit)
	case syntax.OpCapture:
		return cspGrammarSamples(re.Sub[0], limit)
	case syntax.OpQuest:
		return append([]string{""}, cspGrammarSamples(re.Sub[0], limit)...)
	case syntax.OpStar:
		return cspRepeatSamples(re.Sub[0], 0, 2, limit)
	case syntax.OpPlus:
		return cspRepeatSamples(re.Sub[0], 1, 2, limit)
	case syntax.OpRepeat:
		max := re.Max
		if max < 0 || max > re.Min+1 {
			max = re.Min + 1
		}
		return cspRepeatSamples(re.Sub[0], re.Min, max, limit)
	case syntax.OpAlternate:
		var out []string
		for _, sub := range re.Sub {
			out = append(out, cspGrammarSamples(sub, limit)...)
		}
		return cspCap(out, limit)
	case syntax.OpConcat:
		out := []string{""}
		for _, sub := range re.Sub {
			pieces := cspGrammarSamples(sub, limit)
			if len(pieces) == 0 {
				return nil
			}
			next := make([]string, 0, min(len(out)*len(pieces), limit))
			for _, prefix := range out {
				for _, piece := range pieces {
					next = append(next, prefix+piece)
					if len(next) >= limit {
						break
					}
				}
				if len(next) >= limit {
					break
				}
			}
			out = next
		}
		return out
	default:
		return []string{""}
	}
}

// cspCharClassSamples takes the ends of every range in a character class, so a
// class that grows a range contributes a new sample rather than hiding behind
// the first one.
func cspCharClassSamples(ranges []rune, limit int) []string {
	var out []string
	for i := 0; i+1 < len(ranges); i += 2 {
		lo, hi := ranges[i], ranges[i+1]
		out = append(out, string(lo))
		if hi != lo {
			out = append(out, string(hi))
		}
	}
	return cspCap(out, limit)
}

func cspRepeatSamples(sub *syntax.Regexp, low, high, limit int) []string {
	pieces := cspGrammarSamples(sub, limit)
	var out []string
	for count := low; count <= high; count++ {
		if count == 0 {
			out = append(out, "")
			continue
		}
		for _, piece := range pieces {
			out = append(out, strings.Repeat(piece, count))
		}
	}
	return cspCap(out, limit)
}

func cspCap(values []string, limit int) []string {
	if len(values) > limit {
		return values[:limit]
	}
	return values
}
