package modkit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// ValidateConfigFieldOwnership refuses a read of a generated Config field from
// a module that cannot guarantee the field exists.
//
// The typed struct is generated from the union of installed environment
// declarations, so a field belongs to exactly one module and disappears when
// that module does. A reader that neither declares the key nor requires the
// module that declares it therefore stops compiling on a provider swap — which
// is precisely what made a project that does not select ggg/system/identity-clerk
// impossible to build, in three modules that had never chosen Clerk. The
// sanctioned read for everyone else is by key: Config.Value / Config.BoolValue /
// Config.IntValue resolve to the empty value instead of vanishing.
//
// Scope is deliberately narrowed to fields declared by provider adapters. A
// field declared by a seam every profile installs cannot disappear, and its
// name is often an ordinary word (Env, Port) that a selector-name match would
// flag everywhere. Adapters are the removable case, so they are the case with
// a rule.
func ValidateConfigFieldOwnership(modules []Manifest, files map[string][]byte) error {
	owner := map[string]string{}
	for _, module := range modules {
		if module.Runtime.System == nil || module.Runtime.System.Adapter == nil {
			continue
		}
		for _, declaration := range module.Environment {
			owner[declaration.Field] = module.ID
		}
	}
	if len(owner) == 0 {
		return nil
	}

	byID := make(map[string]Manifest, len(modules))
	for _, module := range modules {
		byID[module.ID] = module
	}
	// A module that requires the declarer, directly or through the graph, is
	// installed with it and removed before it, so the field is guaranteed for
	// exactly as long as its reader exists.
	reach := map[string]map[string]struct{}{}
	var visit func(string, map[string]struct{})
	visit = func(id string, into map[string]struct{}) {
		for _, requirement := range byID[id].Requires {
			if _, seen := into[requirement.ID]; seen {
				continue
			}
			into[requirement.ID] = struct{}{}
			visit(requirement.ID, into)
		}
	}
	fileOwner := map[string]string{}
	for _, module := range modules {
		closure := map[string]struct{}{}
		visit(module.ID, closure)
		reach[module.ID] = closure
		for _, file := range module.Files {
			fileOwner[file.Target] = module.ID
		}
	}

	targets := make([]string, 0, len(files))
	for target := range files {
		if strings.HasSuffix(target, ".go") && !IsGeneratedOutputPath(target) {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	for _, target := range targets {
		reader, known := fileOwner[target]
		if !known {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), target, files[target], parser.SkipObjectResolution)
		if err != nil {
			// Payload bytes that do not parse are another gate's problem; a
			// scan must not turn a syntax error into an ownership error.
			continue
		}
		var refusal error
		refuse := func(field, declarer string) {
			refusal = fmt.Errorf(
				"%s (owned by %s) reads config field %s, which %s declares; %s neither declares the key nor requires %s, so deselecting that adapter deletes the field out from under this file. Read it by key instead: cfg.Value/cfg.BoolValue/cfg.IntValue",
				target, reader, field, declarer, reader, declarer)
		}
		check := func(field string) bool {
			declarer, declared := owner[field]
			if !declared || declarer == reader {
				return true
			}
			if _, required := reach[reader][declarer]; required {
				return true
			}
			refuse(field, declarer)
			return false
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if refusal != nil {
				return false
			}
			switch node := node.(type) {
			case *ast.SelectorExpr:
				// Only selectors off the Config value itself. The field names
				// are ordinary identifiers that other structs reuse — the
				// rendered page carries a ClerkPublishableKey of its own — so
				// the receiver is what makes this a config read. Reaching
				// Config through an identifier named cfg/config or a field
				// named Config/cfg is the convention everywhere in this tree,
				// and this scan is what makes it one.
				if !isConfigReceiver(node.X) {
					return true
				}
				return check(node.Sel.Name)
			case *ast.CompositeLit:
				// A struct literal naming the field breaks on removal exactly
				// as a read does, so it is the same refusal — but only when
				// the literal is syntactically a config.Config.
				if !isConfigType(node.Type) {
					return true
				}
				for _, element := range node.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := pair.Key.(*ast.Ident)
					if !ok {
						continue
					}
					if !check(key.Name) {
						return false
					}
				}
			}
			return true
		})
		if refusal != nil {
			return refusal
		}
	}
	return nil
}

// isConfigReceiver reports whether an expression names the parsed configuration:
// a bare cfg/config identifier, or a field selector ending in Config/cfg.
func isConfigReceiver(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name == "cfg" || expr.Name == "config" || expr.Name == "conf"
	case *ast.SelectorExpr:
		return expr.Sel.Name == "Config" || expr.Sel.Name == "cfg" || expr.Sel.Name == "config"
	case *ast.StarExpr:
		return isConfigReceiver(expr.X)
	case *ast.ParenExpr:
		return isConfigReceiver(expr.X)
	}
	return false
}

// isConfigType reports whether a composite-literal type is the config struct.
func isConfigType(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name == "Config"
	case *ast.SelectorExpr:
		return expr.Sel.Name == "Config"
	case *ast.StarExpr:
		return isConfigType(expr.X)
	}
	return false
}

// configPackage is the package the generated loader lives in. A derivation
// function is called from inside it, so its package may not reach back.
const configPackage = "internal/config"

// ValidateDerivationPackages refuses a declared derivation whose package can
// reach internal/config, directly or through the project's own packages.
//
// EnvironmentDerivation documents this as a property of the leaf; without a
// check it is only a comment, and breaking it surfaces as an import cycle in a
// generated file — a Go error about code nobody wrote, pointing at neither the
// manifest that declared it nor the package that broke it. The scan reads the
// planned payload bytes, so it sees exactly what sync would write.
func ValidateDerivationPackages(modules []Manifest, files map[string][]byte, modulePath string) error {
	declared := map[string]string{}
	for _, module := range modules {
		for _, item := range module.Environment {
			if item.Derivation != nil {
				declared[strings.Trim(item.Derivation.Package, "/")] = module.ID + " " + item.Key
			}
		}
	}
	if len(declared) == 0 {
		return nil
	}

	// Project-internal import edges, package to package, from the payloads.
	imports := map[string]map[string]struct{}{}
	targets := make([]string, 0, len(files))
	for target := range files {
		if strings.HasSuffix(target, ".go") && !strings.HasSuffix(target, "_test.go") {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	for _, target := range targets {
		parsed, err := parser.ParseFile(token.NewFileSet(), target, files[target], parser.ImportsOnly)
		if err != nil {
			continue
		}
		pkg := path.Dir(target)
		for _, spec := range parsed.Imports {
			raw := strings.Trim(spec.Path.Value, "\"`")
			local := strings.TrimPrefix(raw, modulePath+"/")
			if local == raw && !strings.HasPrefix(raw, "internal/") {
				continue
			}
			if imports[pkg] == nil {
				imports[pkg] = map[string]struct{}{}
			}
			imports[pkg][local] = struct{}{}
		}
	}

	for _, pkg := range sortedKeys(declared) {
		seen := map[string]bool{}
		var reaches func(string) []string
		reaches = func(from string) []string {
			if seen[from] {
				return nil
			}
			seen[from] = true
			for _, next := range sortedKeys(imports[from]) {
				if next == configPackage {
					return []string{from, next}
				}
				if rest := reaches(next); rest != nil {
					return append([]string{from}, rest...)
				}
			}
			return nil
		}
		if chain := reaches(pkg); chain != nil {
			return fmt.Errorf(
				"derivation for %s names package %s, which reaches %s (%s); the generated config loader calls it, so its package must import nothing that imports config",
				declared[pkg], pkg, configPackage, strings.Join(chain, " -> "))
		}
	}
	return nil
}
