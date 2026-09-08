// Self-host assertions. This file is declared self_host by ggg/system/server:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. A derivative's chain is its own — a profile
// without i18n or telemetry assembles fewer wrappers — so only the publishing
// repository can hold AGENTS.md's chain to the one this package builds.
//
// Spec: AGENTS.md line 230, "**Middleware order is load-bearing**".
//
// The order is DERIVED from Handler(), appChain() and adminChain() by reading
// the construction, never from a list written here. A golden list in a test
// is the same artefact as a golden list in a document: it moves the drift, it
// does not stop it.

package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// anonymousWrapper is the derived name for an inline `http.HandlerFunc(func…)`
// wrapper. The document cannot quote a function name for one, so the check
// requires a prose label at that position and pins the wrapper's behaviour
// separately.
const anonymousWrapper = "«anonymous»"

// parseWebFile parses one file of this package.
func parseWebFile(t *testing.T, name string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

// findFunc returns one top-level function or method body.
func findFunc(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("this package declares no %s; the middleware check reads it", name)
	return nil
}

// wrapperName renders one call expression as the name the chain is documented
// under: a method on the receiver is its bare name, a package function keeps
// its qualifier, and an inline handler is anonymous.
func wrapperName(call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name, true
	case *ast.SelectorExpr:
		qualifier, ok := fun.X.(*ast.Ident)
		if !ok {
			// e.g. s.api.middleware.RequireAPIToken — the leaf names it.
			return fun.Sel.Name, true
		}
		if qualifier.Name == "http" && fun.Sel.Name == "Handler" {
			// A conversion of the mux, not a wrapper.
			return "", false
		}
		if qualifier.Name == "http" && fun.Sel.Name == "HandlerFunc" {
			return anonymousWrapper, true
		}
		if qualifier.Name == "s" {
			return fun.Sel.Name, true
		}
		return qualifier.Name + "." + fun.Sel.Name, true
	}
	return "", false
}

// globalChain derives the request-time middleware order out of Handler().
// Handler assembles inside-out — each statement wraps the previous — so the
// statements are collected in source order and reversed.
func globalChain(t *testing.T) ([]string, *ast.FuncLit) {
	t.Helper()
	handler := findFunc(t, parseWebFile(t, "server.go"), "Handler")

	var assembled []string
	var inline *ast.FuncLit
	consider := func(expr ast.Expr) {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return
		}
		name, ok := wrapperName(call)
		if !ok {
			return
		}
		if name == anonymousWrapper {
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.FuncLit); ok {
					inline = lit
				}
			}
		}
		assembled = append(assembled, name)
	}
	for _, stmt := range handler.Body.List {
		switch stmt := stmt.(type) {
		case *ast.AssignStmt:
			for _, rhs := range stmt.Rhs {
				consider(rhs)
			}
		case *ast.ReturnStmt:
			for _, result := range stmt.Results {
				consider(result)
			}
		}
	}
	if len(assembled) < 5 {
		t.Fatalf("Handler() no longer reads as a chain of wrappers (%v); re-derive this check against its new shape", assembled)
	}

	order := make([]string, 0, len(assembled))
	for i := len(assembled) - 1; i >= 0; i-- {
		order = append(order, assembled[i])
	}
	return order, inline
}

// nestedChain derives the order out of a one-expression guard sequence such as
// appChain: `s.a(s.b(s.c(h)))` runs a, then b, then c.
func nestedChain(t *testing.T, file *ast.File, name string) []string {
	t.Helper()
	fn := findFunc(t, file, name)
	if len(fn.Body.List) != 1 {
		t.Fatalf("%s is no longer one nested expression; re-derive this check", name)
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		t.Fatalf("%s no longer returns one expression; re-derive this check", name)
	}

	var order []string
	expr := ret.Results[0]
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			break
		}
		wrapper, ok := wrapperName(call)
		if !ok {
			break
		}
		order = append(order, wrapper)
		if len(call.Args) == 0 {
			break
		}
		expr = call.Args[len(call.Args)-1]
	}
	if len(order) == 0 {
		t.Fatalf("%s yielded no guards", name)
	}
	return order
}

// documentedArrows splits one `a → b → c` list into its names.
func documentedArrows(list string) []string {
	var names []string
	for _, token := range strings.Split(list, "→") {
		token = strings.TrimSpace(strings.Trim(strings.TrimSpace(token), "`"))
		if token != "" {
			names = append(names, token)
		}
	}
	return names
}

// middlewareBullet returns the collapsed "Middleware order is load-bearing"
// bullet of AGENTS.md.
func middlewareBullet(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "- **Middleware order is load-bearing**") {
			return line
		}
	}
	t.Fatal("AGENTS.md no longer carries the \"Middleware order is load-bearing\" bullet this check compares against Handler()")
	return ""
}

// The global chain, name by name and in order. A reorder fails naming the
// position, the name the document has there and the name the code puts there.
func TestAgentsMiddlewareOrderMatchesTheAssembledChain(t *testing.T) {
	derived, inline := globalChain(t)
	bullet := middlewareBullet(t)

	const anchor = "load-bearing**:"
	at := strings.Index(bullet, anchor)
	if at < 0 {
		t.Fatalf("the AGENTS.md middleware bullet no longer opens with %q", anchor)
	}
	list := bullet[at+len(anchor):]
	end := strings.Index(list, "route groups")
	if end < 0 {
		t.Fatal("the AGENTS.md middleware bullet no longer ends its global chain with \"route groups\"")
	}
	documented := documentedArrows(list[:end])

	// The mux is the innermost handler, not a middleware; the document ends
	// the chain at the route groups instead of naming it.
	if len(derived) > 0 && derived[len(derived)-1] == "mux" {
		derived = derived[:len(derived)-1]
	}

	compareChain(t, "AGENTS.md's middleware bullet", documented, derived)

	// The inline wrapper's job is pinned by name, so repurposing it is caught
	// even though its position carries a prose label.
	if inline == nil {
		t.Fatal("Handler() no longer contains an inline http.HandlerFunc wrapper; the chain shape changed")
	}
	var contextCalls []string
	ast.Inspect(inline, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "With") {
				contextCalls = append(contextCalls, sel.Sel.Name)
			}
		}
		return true
	})
	for _, want := range []string{"WithProviderEnvironment", "WithConfigLookup"} {
		found := false
		for _, got := range contextCalls {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the inline chain wrapper no longer calls templates.%s; AGENTS.md documents it as the provider-environment/config-lookup step (its context calls are %v)",
				want, contextCalls)
		}
	}
}

// The three group chains. `admin` is documented as the app chain plus two
// guards, which is exactly how adminChain is written, so the check compares it
// that way rather than restating twelve names.
func TestAgentsGroupChainsMatchTheGuardSequences(t *testing.T) {
	auth := parseWebFile(t, "auth.go")
	app := nestedChain(t, auth, "appChain")
	admin := nestedChain(t, auth, "adminChain")
	bullet := middlewareBullet(t)

	appDocumented := documentedArrows(betweenLiteral(t, bullet, "(app: `", "`;"))
	if strings.Join(appDocumented, " → ") != strings.Join(app, " → ") {
		t.Errorf("AGENTS.md documents the /app group as %v; appChain assembles %v.\n"+
			"Correct whichever is wrong — the guards run in this order and requireOrg depends on requireAuth having run.", appDocumented, app)
	}

	extraDocumented := documentedArrows(betweenLiteral(t, bullet, "that chain + `", "`;"))
	wantAdmin := append(append([]string{}, app...), extraDocumented...)
	if strings.Join(wantAdmin, " → ") != strings.Join(admin, " → ") {
		t.Errorf("AGENTS.md documents the /admin group as the app chain + %v, i.e. %v; adminChain assembles %v.\n"+
			"Correct whichever is wrong.", extraDocumented, wantAdmin, admin)
	}

	apiDocumented := documentedArrows(betweenLiteral(t, bullet, "`/api`: `", "`)"))
	apiDerived := apiWrapChain(t)
	if strings.Join(apiDocumented, " → ") != strings.Join(apiDerived, " → ") {
		t.Errorf("AGENTS.md documents the /api group as %v; routes.go wraps API handlers in %v.", apiDocumented, apiDerived)
	}
}

// apiWrapChain derives the /api group's guard out of the apiWrap literal
// routes() hands to registerRoutes.
func apiWrapChain(t *testing.T) []string {
	t.Helper()
	routes := findFunc(t, parseWebFile(t, "routes.go"), "routes")
	var guards []string
	ast.Inspect(routes, func(node ast.Node) bool {
		kv, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "apiWrap" {
			return true
		}
		ast.Inspect(kv.Value, func(inner ast.Node) bool {
			if call, ok := inner.(*ast.CallExpr); ok {
				if name, ok := wrapperName(call); ok {
					guards = append(guards, name)
				}
			}
			return true
		})
		return false
	})
	if len(guards) == 0 {
		t.Fatal("routes() no longer supplies an apiWrap that calls a guard; re-derive this check")
	}
	return guards
}

// betweenLiteral returns the text between two literal anchors.
func betweenLiteral(t *testing.T, text, after, before string) string {
	t.Helper()
	start := strings.Index(text, after)
	if start < 0 {
		t.Fatalf("the AGENTS.md middleware bullet no longer contains %q", after)
	}
	rest := text[start+len(after):]
	end := strings.Index(rest, before)
	if end < 0 {
		t.Fatalf("the AGENTS.md middleware bullet no longer contains %q after %q", before, after)
	}
	return rest[:end]
}

// compareChain holds one written statement of the chain against the derived
// one, position by position. The inline wrapper has no function name, so at
// that position a prose label is required instead — which is still an
// ordering assertion, because the label has to sit at that index.
func compareChain(t *testing.T, where string, documented, derived []string) {
	t.Helper()
	if len(documented) != len(derived) {
		t.Errorf("%s states %d middlewares and Handler() assembles %d.\n  stated:    %v\n  assembled: %v\n"+
			"Bring the two in line.", where, len(documented), len(derived), documented, derived)
		return
	}
	for i := range derived {
		if derived[i] == anonymousWrapper {
			if !strings.ContainsAny(documented[i], "-/") {
				t.Errorf("%s: position %d is an inline http.HandlerFunc wrapper and it is named %q.\n"+
					"An unnamed wrapper needs a prose label (the current one is \"provider-environment/config-lookup\"), not a function name.",
					where, i+1, documented[i])
			}
			continue
		}
		if documented[i] != derived[i] {
			t.Errorf("%s: middleware position %d says %q, Handler() assembles %q.\n  stated:    %v\n  assembled: %v\n"+
				"The order is load-bearing; correct whichever one is wrong.", where, i+1, documented[i], derived[i], documented, derived)
		}
	}
}

// Every copy of the chain inside this package must agree with the chain the
// package assembles. Two package comments restated it and both had gone stale
// — they omitted the provider-environment wrapper and telemetry.HTTP — which
// is the same defect as a stale document, at closer range.
func TestEveryChainCommentInThisPackageAgreesWithTheAssembledChain(t *testing.T) {
	derived, _ := globalChain(t)
	if len(derived) > 0 && derived[len(derived)-1] == "mux" {
		derived = derived[:len(derived)-1]
	}
	// The comments end the chain at `routes`, which is the mux.
	want := append(append([]string{}, derived...), "routes")

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	// A wrapped comment continues on the next `//` line, so the continuations
	// are joined before the list is matched.
	unwrap := regexp.MustCompile(`\n//\s*`)
	arrowList := regexp.MustCompile(`maxBytes(?: → [A-Za-z0-9./-]+)+`)
	copies := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			entry.Name() == "agents_middleware_selfhost_test.go" {
			continue
		}
		raw, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, found := range arrowList.FindAllString(unwrap.ReplaceAllString(string(raw), " "), -1) {
			copies++
			compareChain(t, entry.Name()+"'s chain comment", documentedArrows(found), want)
		}
	}
	if copies == 0 {
		t.Error("no comment in this package restates the middleware chain any more; either restore one or delete this check")
	}
}
