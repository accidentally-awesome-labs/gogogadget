package modkit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ValidateShellProviderNeutrality refuses a provider's name in seam-owned
// template bytes.
//
// The shell renders slots; adapters fill them. That boundary held everywhere
// except the render surface, where `ggg/system/server` owned a `<meta
// name="clerk-publishable-key">`, a `<script src="/static/vendor/clerk.browser.js">`
// and two `clerk-*-slot` classes — naming an asset and a mount contract that
// only ggg/system/identity-clerk installs. A project that deselected Clerk got
// a shell with dead mount points and no diagnostic, and `ggg/system/server`
// could not be installed without knowing two vendors' names. PostHog had the
// identical shape, which is what proved the mechanism was missing rather than
// the code sloppy.
//
// Scope is narrowed twice, deliberately.
//
// By owner: system-kind modules that are NOT provider adapters. Those are the
// framework's own machinery — the shell and the seams — and every project
// installs them, so a vendor name there is load-bearing on a module the
// project cannot remove. A page or component module is application content the
// project owns and edits; the marketing page that lists "Postgres · Clerk ·
// Polar · Resend" as the reference stack is prose about a default, not a
// mechanism, and refusing it would be wrong.
//
// By surface: template bytes — `.templ` payloads and the `.go` payloads that
// sit in a templates directory. That is where the leak was and where the slot
// mechanism now exists to replace it. The remaining named-vendor reads in
// internal/config and internal/web/routes.go are by env-key string, which is
// the sanctioned by-key read; they are not rendering decisions and this scan
// does not pretend to cover them.
//
// The token set is DERIVED from the installed graph, never listed here: an
// adapter that offers a managed service target names somebody's product, and
// that name is the token, together with the ids of its managed targets. An
// adapter with only development or self-hosted targets contributes no token —
// `mail-dev`, `observability-log`, `billing-local` and `rate-limit-memory`
// would otherwise ban the words "dev", "log", "local" and "memory" from every
// template in the tree. Matching requires a word start, so "probably" does not
// trip the Ably adapter and "Resend invitation" does not trip Resend.
func ValidateShellProviderNeutrality(modules []Manifest, files map[string][]byte) error {
	tokens := providerVendorTokens(modules)
	if len(tokens) == 0 {
		return nil
	}
	patterns := make(map[string]*regexp.Regexp, len(tokens))
	for token := range tokens {
		patterns[token] = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(token))
	}
	for _, module := range modules {
		if module.Kind != ModuleSystem || isProviderAdapter(module) {
			continue
		}
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			if isTemplateBytes(file.Target) {
				targets = append(targets, file.Target)
			}
		}
		sort.Strings(targets)
		for _, target := range targets {
			content, ok := files[target]
			if !ok {
				continue
			}
			for _, token := range sortedKeys(patterns) {
				match := patterns[token].FindIndex(content)
				if match == nil {
					continue
				}
				return fmt.Errorf(
					"%s owned by %s names provider %q (%s) at line %d; the shell must not know any provider — contribute the markup from %s as a runtime.slots renderer, which receives its own module's non-secret configuration and activates only in the environments that select the adapter",
					target, module.ID, token, tokens[token], 1+strings.Count(string(content[:match[0]]), "\n"), tokens[token])
			}
		}
	}
	return nil
}

// shellSlotRendererSignature is the contract every shell-slot renderer
// satisfies, spelled the way a manifest author reads it.
const shellSlotRendererSignature = "func(context.Context, map[string]string) templ.Component"

// ValidateShellSlotRenderers refuses a runtime.slots contribution whose
// declared renderer is missing or has the wrong signature.
//
// The generated registry assigns each renderer into
// map[string]templates.ShellSlotRenderer, so a wrong shape is already caught —
// as a compile error inside a DO-NOT-EDIT file, about code nobody wrote,
// pointing at neither the manifest that declared it nor the module that owns
// it. That is the diagnostic the typed map was introduced to replace, not one
// to inherit. This is the same failure reported before any write, naming the
// module, the symbol and the shape it must have.
//
// It checks the renderer's actual signature rather than a contract range on
// the module that owns the slot mechanism, and that is structural rather than
// stylistic: a slot contributor CANNOT declare a requirement on
// ggg/system/server. resolveRuntimeOrders turns every literal requires into a
// runtime CONSTRUCTION edge (resolve.go:472-479), and the shell consumes
// identity.verifier and analytics.capturer, so the capability edge already
// orders server after those adapters — the reverse edge is a boot cycle in
// production. A contract range therefore cannot express "my code compiles
// against this module's exported type" for any module whose capability the
// requirement target consumes, and the signature is what a consumer actually
// breaks against.
//
// The residual is worth naming: this checks the SHAPE, not type identity
// across a version boundary. A renderer whose signature still matches but
// whose semantics moved lands as a compile error, as it should. Nothing here
// is a version check.
func ValidateShellSlotRenderers(modules []Manifest, files map[string][]byte) error {
	for _, module := range modules {
		for _, contribution := range module.Runtime.Slots {
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
					if !ok || fn.Recv != nil || fn.Name == nil || fn.Name.Name != contribution.Renderer {
						continue
					}
					found = true
					if !isShellSlotRenderer(fn.Type) {
						return fmt.Errorf(
							"shell slot %s declared by %s names renderer %s.%s, which is not %s; a renderer receives the request context and its own module's declared non-secret configuration, and returns templ.NopComponent to render nothing",
							contribution.ID, module.ID, pkg, contribution.Renderer, shellSlotRendererSignature)
					}
				}
			}
			if !found {
				return fmt.Errorf(
					"shell slot %s declared by %s names renderer %s.%s, which no installed payload in that package declares; it must be an exported %s",
					contribution.ID, module.ID, pkg, contribution.Renderer, shellSlotRendererSignature)
			}
		}
	}
	return nil
}

// isShellSlotRenderer reports whether a declaration is
// func(context.Context, map[string]string) templ.Component. The renderer is
// referenced from generated code by name only, so the shape is checked
// structurally rather than against a string a manifest would have to repeat
// and could get wrong in a second place.
func isShellSlotRenderer(fn *ast.FuncType) bool {
	if fn.TypeParams != nil || fn.Params == nil || fn.Results == nil {
		return false
	}
	params := flattenFields(fn.Params)
	results := flattenFields(fn.Results)
	if len(params) != 2 || len(results) != 1 {
		return false
	}
	return isQualifiedType(params[0], "context", "Context") &&
		isStringMap(params[1]) &&
		isQualifiedType(results[0], "templ", "Component")
}

// flattenFields expands a field list into one entry per declared value, so
// `func(_ context.Context, values map[string]string)` and a grouped
// declaration read the same.
func flattenFields(list *ast.FieldList) []ast.Expr {
	out := make([]ast.Expr, 0, len(list.List))
	for _, field := range list.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			out = append(out, field.Type)
		}
	}
	return out
}

func isQualifiedType(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

func isStringMap(expr ast.Expr) bool {
	mapType, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, keyOK := mapType.Key.(*ast.Ident)
	value, valueOK := mapType.Value.(*ast.Ident)
	return keyOK && key.Name == "string" && valueOK && value.Name == "string"
}

// isProviderAdapter reports whether the module implements a provider slot.
func isProviderAdapter(module Manifest) bool {
	return module.Runtime.System != nil && module.Runtime.System.Adapter != nil
}

// isTemplateBytes reports whether a target path is part of the render surface:
// a templ source, or the Go beside it that the templates package compiles.
func isTemplateBytes(target string) bool {
	if strings.HasSuffix(target, ".templ") {
		return true
	}
	return strings.HasSuffix(target, ".go") && strings.Contains(target, "/templates/")
}

// providerVendorTokens maps a vendor name onto the adapter module that owns
// it. The adapter's name minus its slot's own name is one token — identity-clerk
// in slot ggg/identity yields "clerk" — and every managed target id is another,
// because storage-s3's managed target is called r2 and the vendor name is not
// derivable from the adapter's name alone.
func providerVendorTokens(modules []Manifest) map[string]string {
	tokens := make(map[string]string)
	for _, module := range modules {
		if !isProviderAdapter(module) {
			continue
		}
		adapter := module.Runtime.System.Adapter
		managed := make([]string, 0, len(adapter.Targets))
		for _, target := range adapter.Targets {
			if target.Mode == "managed" {
				managed = append(managed, target.ID)
			}
		}
		if len(managed) == 0 {
			continue
		}
		slot := adapter.Slot
		if index := strings.LastIndex(slot, "/"); index >= 0 {
			slot = slot[index+1:]
		}
		vendor := strings.TrimPrefix(module.Name, slot+"-")
		for _, token := range append(managed, vendor) {
			if token != "" {
				tokens[token] = module.ID
			}
		}
	}
	return tokens
}

// ValidateNoCredentialPresenceSelectors refuses a function whose whole body is
// a test for whether a provider's credential happens to be set.
//
// The plan ordered this class deleted by name — "Remove credential-presence
// selectors such as ResendConfigured/StorageConfigured: explicit managed
// selection with missing keys returns one joined boot error and never falls
// back locally" — and one survived to gate a route:
// `if s.cfg.PostHogEnabled()` around a hand-registered /ingest/ proxy, with
// `PostHogEnabled` reading `Value("POSTHOG_API_KEY") != ""`.
//
// The predicate is unsound wherever it appears. An adapter is selected per
// environment in gogogadget.json; its declared keys are required, so their
// absence is a boot refusal; and its constructor refuses an empty credential
// anyway. So a presence test can only ever disagree with the thing it stands
// in for — it says "not configured" where selection says "required", and it
// silently degrades instead of refusing.
//
// The rule is narrow on purpose, and the alternatives were measured rather
// than guessed. A lexical rule over seam-owned Go — the shape that works for
// template bytes, where nearly everything is output — matches 125 sites in
// this tree, almost all of them prose that legitimately records which vendor a
// seam was designed against ("handlers never import the Polar SDK directly").
// A rule over configuration-key literals matches 21, and most are the
// SANCTIONED by-key read that ValidateConfigFieldOwnership itself recommends
// for a key the reader does not declare. Neither is the defect. The defect is
// executable: a bool that means "is this vendor configured".
func ValidateNoCredentialPresenceSelectors(modules []Manifest, files map[string][]byte) error {
	keys := adapterDeclaredKeys(modules)
	if len(keys) == 0 {
		return nil
	}
	for _, module := range modules {
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			// A test may name a provider and its keys deliberately: proving
			// the boot matrix reacts to a credential is not the same as
			// branching on one in product code.
			if file.Class == FileClassTest || !strings.HasSuffix(file.Target, ".go") {
				continue
			}
			targets = append(targets, file.Target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			content, ok := files[target]
			if !ok {
				continue
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, target, content, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("scan %s: %w", target, err)
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				key, found := credentialPresenceKey(fn, keys)
				if !found {
					continue
				}
				return fmt.Errorf(
					"%s:%d in %s: %s reports whether %s is set, which is a credential-presence selector; selection is the gate — an adapter chosen for the environment has its keys required at boot and its constructor refuses an empty one, so this can only disagree with both. Gate on the adapter's selection instead (a declared contribution is gated by providerActive) or let the boot refusal speak",
					target, fset.Position(fn.Pos()).Line, module.ID, fn.Name.Name, key)
			}
		}
	}
	return nil
}

// adapterDeclaredKeys is every environment key a provider adapter owns. Only
// adapter keys qualify: a seam's own key cannot disappear, and a presence test
// on one is a plain option check rather than a stand-in for selection.
func adapterDeclaredKeys(modules []Manifest) map[string]string {
	keys := make(map[string]string)
	for _, module := range modules {
		if !isProviderAdapter(module) {
			continue
		}
		for _, item := range module.Environment {
			keys[item.Key] = module.ID
		}
	}
	return keys
}

// credentialPresenceKey reports the adapter key a function's return value is a
// presence test for. It requires a single bool result and a body that is one
// return statement of comparisons against "", so an ordinary handler that
// happens to read a key by name is untouched: the defect is a named predicate
// standing in for selection, not a read.
func credentialPresenceKey(fn *ast.FuncDecl, keys map[string]string) (string, bool) {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return "", false
	}
	result, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	if !ok || result.Name != "bool" || len(fn.Body.List) != 1 {
		return "", false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	return emptinessComparisonKey(ret.Results[0], keys)
}

// emptinessComparisonKey walks a boolean expression built only from
// `<config read of KEY> != ""` comparisons joined by && or ||.
func emptinessComparisonKey(expr ast.Expr, keys map[string]string) (string, bool) {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return emptinessComparisonKey(node.X, keys)
	case *ast.BinaryExpr:
		switch node.Op {
		case token.LAND, token.LOR:
			if key, ok := emptinessComparisonKey(node.X, keys); ok {
				return key, true
			}
			return emptinessComparisonKey(node.Y, keys)
		case token.NEQ, token.EQL:
			if !isEmptyString(node.X) && !isEmptyString(node.Y) {
				return "", false
			}
			for _, side := range []ast.Expr{node.X, node.Y} {
				if key, ok := configReadKey(side, keys); ok {
					return key, true
				}
			}
		}
	}
	return "", false
}

func isEmptyString(expr ast.Expr) bool {
	literal, ok := expr.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING && (literal.Value == `""` || literal.Value == "``")
}

// configReadKey extracts the adapter key from a by-key configuration read.
func configReadKey(expr ast.Expr, keys map[string]string) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch selector.Sel.Name {
	case "Value", "BoolValue", "IntValue":
	default:
		return "", false
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	key := strings.Trim(literal.Value, "\"`")
	if _, owned := keys[key]; !owned {
		return "", false
	}
	return key, true
}
