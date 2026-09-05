package modkit

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// cspHTTPSOrigin is the only host shape a contribution may return: https, a
// hostname, an optional port, and nothing else. At most ONE leading wildcard
// label is allowed, because that is the shape a real provider publishes —
// Clerk's development frontend API is https://*.clerk.accounts.dev — and
// because a wildcard anywhere else, or more than one, stops describing a
// vendor and starts describing the internet.
//
// No path, no query, no fragment: CSP ignores the path for most directives, so
// accepting one would let a contribution look narrower than it is.
var cspHTTPSOrigin = regexp.MustCompile(
	`^https://(?:\*\.)?[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+(?::(?:6553[0-5]|655[0-2][0-9]|65[0-4][0-9]{2}|6[0-4][0-9]{3}|[1-9][0-9]{0,3}))?$`)

// cspSchemeSources are the non-origin sources a contribution may return. Both
// are relaxations and both are here because a real vendor needs one: clerk-js
// runs its session handshake in a blob: Web Worker, and inline images arrive
// as data: URIs. Everything else — and in particular 'unsafe-inline',
// 'unsafe-eval' and bare * — is refused by not being in this list.
var cspSchemeSources = map[string]struct{}{
	"blob:": {},
	"data:": {},
}

// ValidateCSPSource reports why a source may not be contributed, or nil.
//
// This is the constraint the whole mechanism turns on: an adapter may ADD
// sources and may never weaken the policy. A contribution mechanism that can
// undo `script-src 'self'` would be worse than the hardcoded vendor hostname
// it replaces, because the hardcoding was at least visible in one file that a
// reviewer reads. So the grammar is an allowlist, the refusal message names
// the alternative rather than only the rule, and the same function runs at
// plan time over the literals in a contribution's source and at runtime over
// whatever it actually returned.
func ValidateCSPSource(source string) error {
	switch {
	case source == "":
		return fmt.Errorf("an empty source adds nothing and renders as a stray space")
	case strings.ContainsAny(source, " \t\n;,"):
		return fmt.Errorf(
			"%q contains a separator; one source per entry, because a string with a space in it can smuggle a whole directive past a per-source check", source)
	}
	if _, ok := cspSchemeSources[source]; ok {
		return nil
	}
	if cspHTTPSOrigin.MatchString(source) {
		return nil
	}
	switch {
	case strings.HasPrefix(source, "'"):
		return fmt.Errorf(
			"%s is a keyword source; a contribution may add origins and blob:/data:, never a keyword — 'unsafe-inline' and 'unsafe-eval' would undo the posture the base policy exists to hold, and 'self' is already in every directive", source)
	case source == "*" || strings.HasPrefix(source, "*"):
		return fmt.Errorf(
			"%q is a wildcard host; at most one leading label may be a wildcard (https://*.vendor.example is legitimate), because a bare or interior wildcard stops describing a vendor", source)
	case strings.HasPrefix(source, "http://"):
		return fmt.Errorf(
			"%q is plaintext http; a contributed origin must be https, and a provider that only offers http is a provider whose traffic anyone on the path can read", source)
	}
	return fmt.Errorf(
		"%q is not a contributable source; use an https origin (optionally with one leading wildcard label) or one of blob:, data:", source)
}

// validateCSPContributions checks the declaration: ids, the closed directive
// set, and the package and symbol the generator will name.
func validateCSPContributions(items []CSPContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("manifest runtime csp[%d] id is invalid", i)
		}
		if len(item.Directives) == 0 {
			return fmt.Errorf("manifest runtime csp[%s] must name at least one directive", item.ID)
		}
		grants := make(map[CSPDirective]struct{}, len(item.Directives))
		previous := CSPDirective("")
		for _, directive := range item.Directives {
			if !contributableCSPDirective(directive) {
				return fmt.Errorf(
					"manifest runtime csp[%s] directive %q is not contributable; a module may add sources to %s only — script-src, default-src, base-uri, form-action and frame-ancestors are the framework's posture, not a list to extend",
					item.ID, directive, joinCSPDirectives(ContributableCSPDirectives))
			}
			if _, duplicate := grants[directive]; duplicate {
				return fmt.Errorf("manifest runtime csp[%s] names directive %q twice", item.ID, directive)
			}
			grants[directive] = struct{}{}
			if canonical && previous != "" && previous > directive {
				return fmt.Errorf("manifest runtime csp[%s] directives must be sorted", item.ID)
			}
			previous = directive
		}
		if !validPackagePath(item.Package) {
			return fmt.Errorf("manifest runtime csp[%s] package is invalid", item.ID)
		}
		if !validIdentifier(item.Sources) {
			return fmt.Errorf("manifest runtime csp[%s] sources %q is not a Go identifier", item.ID, item.Sources)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("manifest runtime csp contains duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime csp must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func contributableCSPDirective(directive CSPDirective) bool {
	for _, allowed := range ContributableCSPDirectives {
		if allowed == directive {
			return true
		}
	}
	return false
}

func joinCSPDirectives(directives []CSPDirective) string {
	names := make([]string, 0, len(directives))
	for _, directive := range directives {
		names = append(names, string(directive))
	}
	return strings.Join(names, ", ")
}

// emitCSPRegistry renders the installed Content-Security-Policy contributions:
// which directives each may add to, the function that returns its sources, the
// configuration keys that function receives, and the per-environment
// activation predicate.
//
// The values are the contributing module's own declared non-secret environment
// keys, exactly as the shell slots receive them — CSP is per-deployment, so the
// composed header is computed once at server construction rather than per
// request.
func emitCSPRegistry(ctx context.Context, modulePath string, lock Lock, graph []Manifest) (*GeneratedFile, error) {
	type contribution struct {
		id, pkg, alias, symbol, owner string
		directives                    []CSPDirective
		values                        []string
	}
	var items []contribution
	counter := 0
	for _, m := range orderedModules(lock, graph) {
		nonSecret := make([]string, 0, len(m.Environment))
		for _, item := range m.Environment {
			if !item.Secret {
				nonSecret = append(nonSecret, item.Key)
			}
		}
		sort.Strings(nonSecret)
		for _, item := range m.Runtime.CSP {
			items = append(items, contribution{
				id: item.ID, pkg: qualifyPackage(modulePath, item.Package),
				alias: fmt.Sprintf("cspSource%d", counter), symbol: item.Sources,
				owner: m.ID, directives: item.Directives, values: nonSecret,
			})
			counter++
		}
	}

	var b strings.Builder
	b.WriteString(genHeader(modulePath, lock))
	b.WriteString("package web\n\n")
	if len(items) > 0 {
		b.WriteString("import (\n")
		for _, item := range items {
			fmt.Fprintf(&b, "\t%s %q\n", item.alias, item.pkg)
		}
		b.WriteString(")\n\n")
	}
	b.WriteString("var CSPSourceProviders = map[string]CSPSourceProvider{\n")
	for _, item := range items {
		fmt.Fprintf(&b, "\t%s: %s.%s,\n", goString(item.id), item.alias, item.symbol)
	}
	b.WriteString("}\n\nvar CSPDirectiveGrants = map[string][]string{\n")
	for _, item := range items {
		fmt.Fprintf(&b, "\t%s: []string{\n", goString(item.id))
		for _, directive := range item.directives {
			fmt.Fprintf(&b, "\t\t%s,\n", goString(string(directive)))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\nvar CSPValueKeys = map[string][]string{\n")
	for _, item := range items {
		if len(item.values) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\t%s: []string{\n", goString(item.id))
		for _, key := range item.values {
			fmt.Fprintf(&b, "\t\t%s,\n", goString(key))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\nvar CSPActive = map[string]func(string) bool{\n")
	for _, item := range items {
		if slot, adapter, ok := adapterForModule(graph, item.owner); ok {
			fmt.Fprintf(&b, "\t%s: func(env string) bool { return providerActive(env, %q, %q) },\n",
				goString(item.id), slot, adapter)
		}
	}
	b.WriteString("}\n")
	_ = ctx
	return &GeneratedFile{Path: "internal/web/csp_registry_gen.go", Content: b.String()}, nil
}

// ValidateCSPContributionSources refuses a contribution whose source literals
// are not contributable, and one that returns a directive its manifest did not
// grant.
//
// Both are checked statically, over the declared function's own payload bytes,
// because that is where these values are actually written: a source list is a
// literal slice in a small function, not something assembled from a database.
//
// The SHAPE that assumption buys, stated plainly rather than implied: the walk
// finds string literals inside a composite-literal key/value pair in the named
// function. A contribution that builds its map imperatively, or returns the
// result of a helper, is not checked here — the literals are simply not
// where the walk looks. That is a deliberate limit, not an oversight:
// broadening it to whole-function constant folding would be a small abstract
// interpreter, and it would still miss a value that arrives from
// configuration. The backstop is the runtime composer, which validates every
// source it is handed, drops what fails, and reports the drop through
// observability.Reporter — so the constraint holds either way, and what plan
// time buys is the module author seeing it before anything is written.
func ValidateCSPContributionSources(modules []Manifest, files map[string][]byte) error {
	for _, module := range modules {
		for _, contribution := range module.Runtime.CSP {
			granted := make(map[string]struct{}, len(contribution.Directives))
			for _, directive := range contribution.Directives {
				granted[string(directive)] = struct{}{}
			}
			pkg := strings.Trim(contribution.Package, "/")
			found := false
			for _, target := range sortedKeys(files) {
				if !strings.HasSuffix(target, ".go") || path.Dir(target) != pkg {
					continue
				}
				parsed, err := parser.ParseFile(token.NewFileSet(), target, files[target], parser.SkipObjectResolution)
				if err != nil {
					return fmt.Errorf("scan %s: %w", target, err)
				}
				for _, decl := range parsed.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Recv != nil || fn.Name == nil || fn.Name.Name != contribution.Sources {
						continue
					}
					found = true
					if err := inspectCSPSourceFunction(fn, module.ID, contribution, granted); err != nil {
						return err
					}
				}
			}
			if !found {
				return fmt.Errorf(
					"csp contribution %s declared by %s names %s.%s, which no installed payload in that package declares",
					contribution.ID, module.ID, pkg, contribution.Sources)
			}
		}
	}
	return nil
}

// inspectCSPSourceFunction walks one declared sources function. Every string
// literal in a map key position is checked against the grant, and every other
// string literal against the source grammar — which is what makes a returned
// 'unsafe-inline' a plan refusal rather than a runtime surprise.
func inspectCSPSourceFunction(fn *ast.FuncDecl, moduleID string, contribution CSPContribution, granted map[string]struct{}) error {
	var failure error
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		switch typed := node.(type) {
		case *ast.KeyValueExpr:
			key, ok := stringLiteralValue(typed.Key)
			if !ok {
				return true
			}
			if _, allowed := granted[key]; !allowed {
				failure = fmt.Errorf(
					"csp contribution %s declared by %s returns sources for %q, which its manifest does not grant; declare the directive or drop the sources — reading the manifest has to tell you the whole blast radius",
					contribution.ID, moduleID, key)
				return false
			}
			// The values under a granted directive are sources.
			ast.Inspect(typed.Value, func(inner ast.Node) bool {
				if failure != nil {
					return false
				}
				if value, ok := stringLiteralValue(inner); ok {
					if err := ValidateCSPSource(value); err != nil {
						failure = fmt.Errorf("csp contribution %s declared by %s: %w", contribution.ID, moduleID, err)
						return false
					}
				}
				return true
			})
			return false
		}
		return true
	})
	return failure
}

func stringLiteralValue(expr ast.Node) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	unquoted, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return unquoted, true
}
