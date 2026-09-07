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

// uiPackageSuffix is the import path of the one Go package every component and
// element module contributes a file to. Matching on the suffix rather than on
// a full path is what makes the scan work in a derivative project, whose module
// path is its own.
const uiPackageSuffix = "internal/web/templates/ui"

// uiRendererSignature reads the renderer's name out of a declared signature.
// The manifest states `templ Badge(o BadgeOpts)`; a drift test already holds
// that string against the code, so it is the one place the package's exported
// surface is written down as data.
var uiRendererSignature = regexp.MustCompile(`^templ\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// ValidateUIComponentRequires refuses a payload that renders a ui component its
// own module has no declared path to.
//
// internal/web/templates/ui is 145 modules in ONE Go package, and until the
// edges below were derived, all 35 page modules declared zero of them. The
// member lists of the shipped profiles were a hand-maintained superset arrived
// at by over-including — `minimal` demanded 57 ui modules, reached 67, and
// installed 77 — and the failure mode of getting that wrong is silent at plan
// time and fatal at compile time: `ggg/component/notice` was held in `minimal`
// only by `ggg/component/kanban`'s requires, while seven of `minimal`'s own
// pages call ui.Notice. Removing kanban from the member list, which nothing
// referenced, would have taken notice with it and broken the tree with no
// diagnostic from any command that ran before the compiler.
//
// So this is the reference-has-a-declaration direction for the ui package,
// beside ValidateAssetReferences' reference-has-a-file. It reads the SAME
// declaration the component reference and the gallery are generated from —
// `runtime.ui[].signature` — so a renderer's owner is stated once.
//
// # Scope, and why the two obvious wider rules are wrong
//
// The alphabet is declared renderers and their options structs, never "any
// exported identifier". `ui.Attrs`, `ui.KindDanger` and `ui.IconClose` belong
// to ggg/element/ui-core, which every ui module already requires and every
// closure installs, so checking them buys nothing; and a type like ui.MenuItem
// that a module happens to own is not part of any declared contract, so a rule
// over it would be enforcing where a symbol was written rather than what it is.
// That residual is real and named in the report: this refuses a missing
// renderer edge, not every missing symbol edge.
//
// Payloads inside the ui package itself are skipped, and that is not a
// convenience. Within `package ui` a reference is a BARE identifier, so there
// is no qualifier to key on, and the two derivations that read those bare names
// wrongly are exactly this repository's measured false-positive sources:
// ui-core's contract_test.go names all 145 renderers in one hand-written table,
// which collapses the package into a single strongly-connected component of
// 145, and reference_gen.go names every component by STRING LITERAL, which a
// textual rule reads as 145-way fan-out. Test payloads are skipped for the
// first of those reasons and generated payloads for the second.
//
// The alphabet comes from the CATALOG rather than from the installed graph,
// and that is the difference between this catching the incident and reporting
// it after the fact. The failure is a component that fell OUT of the closure:
// with the installed set as the alphabet, ui.Notice stops being a known
// renderer at the exact moment nothing installs notice, and the reference goes
// unseen. The catalog knows who owns Notice whether or not this project
// selected it.
func ValidateUIComponentRequires(modules, catalog []Manifest, files map[string][]byte) error {
	owner := uiRendererOwners(catalog)
	if len(owner) == 0 {
		return nil
	}
	installed := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		installed[module.ID] = struct{}{}
	}
	reach := requirementReach(modules)

	ordered := append([]Manifest(nil), modules...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, module := range ordered {
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			if !isUIReferencingPayload(file) {
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
			for _, ref := range uiRendererReferences(target, content, owner) {
				provider := owner[ref.symbol]
				if provider == module.ID {
					continue
				}
				if _, ok := reach[module.ID][provider]; ok {
					continue
				}
				_, present := installed[provider]
				return fmt.Errorf(
					"%s renders ui.%s at %s:%d, which %s owns, but declares no requires path to it%s. "+
						"a page's component edges are what let a profile install the ui modules it actually uses: "+
						"add %s to %s's requires",
					module.ID, ref.symbol, target, ref.line, provider,
					map[bool]string{true: "", false: " and nothing installs it"}[present],
					provider, module.ID)
			}
		}
	}
	return nil
}

// uiRendererOwners maps every declared renderer and its options struct onto the
// module that declares it. A duplicate is a refusal elsewhere — the component
// registry generator rejects two owners for one name — so the last writer here
// cannot mask one.
func uiRendererOwners(modules []Manifest) map[string]string {
	owner := map[string]string{}
	for _, module := range modules {
		for _, contribution := range module.Runtime.UI {
			match := uiRendererSignature.FindStringSubmatch(contribution.Signature)
			if match == nil {
				continue
			}
			owner[match[1]] = module.ID
			owner[match[1]+"Opts"] = module.ID
		}
	}
	return owner
}

// requirementReach is the transitive requires closure of every installed
// module. Transitive is the right question: a page that requires data-table
// reaches column-header through it, and demanding a direct edge for something
// it never names would be asking the manifest to repeat the component's own
// composition.
func requirementReach(modules []Manifest) map[string]map[string]struct{} {
	direct := make(map[string][]string, len(modules))
	for _, module := range modules {
		for _, requirement := range module.Requires {
			direct[module.ID] = append(direct[module.ID], requirement.ID)
		}
	}
	reach := make(map[string]map[string]struct{}, len(modules))
	for _, module := range modules {
		seen := map[string]struct{}{}
		stack := append([]string(nil), direct[module.ID]...)
		for len(stack) > 0 {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			stack = append(stack, direct[next]...)
		}
		reach[module.ID] = seen
	}
	return reach
}

// isUIReferencingPayload reports whether a payload is authored source that can
// name a ui renderer. Generated output is excluded because it is rendered FROM
// the installed set and can never disagree with it; test payloads because the
// one cross-cutting suite in this package names every renderer there is.
func isUIReferencingPayload(file ManifestFile) bool {
	if file.Class == FileClassGenerated || file.Class == FileClassTest {
		return false
	}
	if strings.HasSuffix(file.Target, "_test.go") {
		return false
	}
	if strings.HasPrefix(file.Target, uiPackageSuffix+"/") {
		return false
	}
	switch path.Ext(file.Target) {
	case ".go", ".templ":
		return true
	default:
		return false
	}
}

type uiReference struct {
	symbol string
	line   int
}

// uiRendererReferences finds the renderers one payload names.
//
// The qualifier is resolved by PARSING the payload's own import block —
// parser.ImportsOnly stops at the end of it, which is the part of a .templ file
// that is ordinary Go — so a file that does not import the package contributes
// nothing and an aliased import is followed rather than assumed. Go payloads
// are then walked as an AST, so a comment or a string cannot be a reference. A
// .templ body is not Go and cannot be, so it is matched on bytes with comments
// and string literals removed first; measured over this whole tree that scan
// invents nothing the type checker does not also see.
func uiRendererReferences(target string, content []byte, owner map[string]string) []uiReference {
	fset := token.NewFileSet()
	header, err := parser.ParseFile(fset, target, content, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	local := ""
	for _, imported := range header.Imports {
		value := strings.Trim(imported.Path.Value, `"`)
		if !addressesUIPackage(value) {
			continue
		}
		local = path.Base(value)
		if imported.Name != nil {
			local = imported.Name.Name
		}
	}
	if local == "" {
		return nil
	}

	var out []uiReference
	if path.Ext(target) == ".go" {
		parsed, parseErr := parser.ParseFile(fset, target, content, parser.SkipObjectResolution)
		if parseErr != nil {
			// Unparseable Go is the compiler's problem to report.
			return nil
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			qualifier, ok := selector.X.(*ast.Ident)
			if !ok || qualifier.Name != local {
				return true
			}
			if _, ok := owner[selector.Sel.Name]; !ok {
				return true
			}
			out = append(out, uiReference{symbol: selector.Sel.Name, line: fset.Position(selector.Pos()).Line})
			return true
		})
		return out
	}

	stripped := stripGoCommentsAndStrings(content)
	for _, match := range qualifiedReference(local).FindAllSubmatchIndex(stripped, -1) {
		symbol := string(stripped[match[2]:match[3]])
		if _, ok := owner[symbol]; !ok {
			continue
		}
		out = append(out, uiReference{
			symbol: symbol,
			line:   1 + strings.Count(string(stripped[:match[2]]), "\n"),
		})
	}
	return out
}

// addressesUIPackage reports whether an import path names the ui package. A
// suffix match is deliberate: the same payload installs into a derivative
// under that project's own module path.
func addressesUIPackage(value string) bool {
	return value == uiPackageSuffix || strings.HasSuffix(value, "/"+uiPackageSuffix)
}

// qualifiedReference matches `<local>.Symbol` where the qualifier actually
// starts: the preceding byte may not be a word character or a dot, so neither
// `myui.Badge` nor a method chain's `o.ui.Badge` counts as the package.
func qualifiedReference(local string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w.])` + regexp.QuoteMeta(local) + `\.([A-Z][A-Za-z0-9_]*)`)
}

// stripGoCommentsAndStrings blanks out the spans of a templ payload that are
// text rather than code, keeping every byte offset and newline so a refusal
// still names the line the reference is on. templ bodies interleave Go with
// markup, so `//` is only treated as a comment when it is not inside a string
// — which is what keeps an `https://` URL in an href from blanking the rest of
// its line.
func stripGoCommentsAndStrings(content []byte) []byte {
	out := append([]byte(nil), content...)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == '/' && i+1 < len(out) && out[i+1] == '/':
			end := i
			for end < len(out) && out[end] != '\n' {
				end++
			}
			blank(i, end)
			i = end
		case out[i] == '/' && i+1 < len(out) && out[i+1] == '*':
			end := i + 2
			for end+1 < len(out) && !(out[end] == '*' && out[end+1] == '/') {
				end++
			}
			blank(i, min(end+2, len(out)))
			i = end + 1
		case out[i] == '"' || out[i] == '`':
			quote := out[i]
			end := i + 1
			for end < len(out) && out[end] != quote {
				if quote == '"' && out[end] == '\\' {
					end++
				}
				if quote == '"' && out[end] == '\n' {
					break
				}
				end++
			}
			blank(i+1, min(end, len(out)))
			i = end
		}
	}
	return out
}
