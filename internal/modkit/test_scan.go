package modkit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// recorderPackage is the import path whose ResponseRecorder this scan guards.
const recorderPackage = "net/http/httptest"

// ValidateNoRecorderGoroutineHandoff refuses a test that hands an
// httptest.ResponseRecorder to a goroutine.
//
// The defect prevented is a test reading state that a server goroutine writes,
// without synchronisation — a race that only `-race` reports, so it passes
// locally and fails in CI, or passes nineteen runs and fails the twentieth.
// ResponseRecorder is the sharpest instance of it: the type has no
// synchronisation of any kind, and its Body/Code/HeaderMap are read by the
// test while the handler is still writing them. A streaming handler (SSE)
// makes it unconditional, because there is no moment at which the handler has
// finished.
//
// The rule is the handoff, not the read, and that is deliberate: the read is
// only safe if the test can prove a happens-before edge to every write, which
// is exactly the reasoning that was got wrong. A recorder that never crosses a
// `go` statement needs no such proof. Tests that genuinely need a recorder on
// another goroutine pass a synchronised one — a small ResponseWriter whose
// Write and String take the same mutex — which is also what makes the assertion
// readable.
//
// The narrow shape was chosen against a measured alternative rather than
// guessed. The broad form of this rule — "a local written inside a
// goroutine/handler closure and also referenced outside it" — matches 30 sites
// in this tree, and 28 of them are the ordinary httptest.NewServer fixture
// whose handler has returned before the client call does, which `go test
// -race` proves clean on every run. That rule would refuse correct code 93% of
// the time and re-implement the race detector badly; this one matches 0.
//
// What it does NOT see, stated so the guarantee is not overread. It recognises
// a recorder only where the value is bound in the same top-level function, so
// one returned by a helper is invisible (internal/web/idempotency_test.go
// builds recorders that way). It recognises only a `go` statement, so a
// handoff through errgroup.Go, a worker pool, or any function that starts a
// goroutine for its caller passes. And it says nothing about the general class
// — any other unsynchronised type shared with a server goroutine is still
// `-race`'s business, which is what caught both original defects. This scan
// removes one shape from the space of writable code; it does not decide the
// question.
func ValidateNoRecorderGoroutineHandoff(modules []Manifest, files map[string][]byte) error {
	for _, module := range modules {
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			// Both halves are load-bearing, and each was wrong alone. Class
			// alone hands the Go parser 235 non-Go payloads that are legitimately
			// declared class "test" — every Playwright spec, every committed
			// PNG baseline, internal/gggcli/testdata/new-saas.json. The
			// _test.go suffix alone misses a Go payload declared class "test"
			// that is not named _test.go, and it also used to be the only
			// selector, which is how eight mis-declared payloads went
			// unscanned before their declarations were corrected. The set
			// wanted is Go source a module declares as test.
			if file.Class != FileClassTest || !strings.HasSuffix(file.Target, ".go") {
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
			alias, imported := importAlias(parsed, recorderPackage)
			if !imported {
				continue
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				name, pos, found := recorderCrossingGoStatement(fn, alias)
				if !found {
					continue
				}
				return fmt.Errorf(
					"%s:%d in %s: %s hands the httptest.ResponseRecorder %q to a goroutine; that type has no synchronisation, so every read of its Body, Code or headers races the handler's writes and only -race reports it. Give the goroutine a recorder whose Write and reader take one mutex, and assert through that",
					target, fset.Position(pos).Line, module.ID, fn.Name.Name, name)
			}
		}
	}
	return nil
}

// importAlias resolves the local name a file uses for an import path.
func importAlias(file *ast.File, path string) (string, bool) {
	quoted := `"` + path + `"`
	for _, spec := range file.Imports {
		if spec.Path.Value != quoted {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" {
				return "", false
			}
			return spec.Name.Name, true
		}
		return path[strings.LastIndex(path, "/")+1:], true
	}
	return "", false
}

// recorderCrossingGoStatement reports the first recorder-bound name a `go`
// statement in fn references. Scope is the whole top-level function so a
// recorder built in the test body and captured by a nested subtest closure is
// still seen.
func recorderCrossingGoStatement(fn *ast.FuncDecl, alias string) (string, token.Pos, bool) {
	names := recorderNames(fn, alias)
	if len(names) == 0 {
		return "", token.NoPos, false
	}
	var (
		hit   string
		at    token.Pos
		found bool
	)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		statement, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		ast.Inspect(statement, func(inner ast.Node) bool {
			if found {
				return false
			}
			if ident, ok := inner.(*ast.Ident); ok && names[ident.Name] {
				hit, at, found = ident.Name, statement.Go, true
				return false
			}
			return true
		})
		return true
	})
	return hit, at, found
}

// recorderNames collects every local bound to a ResponseRecorder in fn, from
// either httptest.NewRecorder() or a ResponseRecorder composite literal.
func recorderNames(fn *ast.FuncDecl, alias string) map[string]bool {
	names := map[string]bool{}
	bind := func(targets []ast.Expr, value ast.Expr) {
		if !isRecorderValue(value, alias) {
			return
		}
		for _, target := range targets {
			if ident, ok := target.(*ast.Ident); ok && ident.Name != "_" {
				names[ident.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			if len(statement.Rhs) == 1 {
				bind(statement.Lhs, statement.Rhs[0])
			}
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				return true
			}
			for _, spec := range declaration.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Values) != 1 {
					continue
				}
				targets := make([]ast.Expr, 0, len(value.Names))
				for _, ident := range value.Names {
					targets = append(targets, ident)
				}
				bind(targets, value.Values[0])
			}
		}
		return true
	})
	return names
}

// isRecorderValue reports whether an expression evaluates to a
// ResponseRecorder: the constructor call or a composite literal of the type,
// addressed or not.
func isRecorderValue(value ast.Expr, alias string) bool {
	switch expression := value.(type) {
	case *ast.CallExpr:
		return isQualifiedIdent(expression.Fun, alias, "NewRecorder")
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		return isRecorderValue(expression.X, alias)
	case *ast.CompositeLit:
		return isQualifiedIdent(expression.Type, alias, "ResponseRecorder")
	}
	return false
}

func isQualifiedIdent(expression ast.Expr, pkg, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
