package modkit

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// templatesPackageSuffix is the import path of the package templ renders into.
// A typed option-struct field carrying a request target only exists where
// markup is produced, and matching on the suffix rather than a full path is
// what keeps the rule working in a derivative project under its own module
// path — the same reason uiPackageSuffix is a suffix.
const templatesPackageSuffix = "internal/web/templates"

// ValidateRouteReferences refuses a control whose route no selected module
// declares.
//
// This is the third instance of one shape, and the two before it were the two
// reference scans this engine already runs. ValidateAssetReferences asks
// whether an asset REFERENCE has a declared FILE, after moving
// `static/analytics.js` between manifests dropped its entry and left a
// `<script src>` at a 404 through two releases. ValidateUIComponentRequires
// asks whether a `ui.X` reference has a declared EDGE to X's owner, after 35
// page modules referenced components 1296 times while declaring nothing. This
// asks whether a route a template TARGETS is declared by something installed.
//
// Both sides already existed and nothing compared them. Routes are declared
// (`runtime.routes` with id, method and pattern, plus the index and detail
// patterns a content type expands to) and the mux is built from
// internal/web/routes_registry_gen.go; templates and shipped scripts named
// those patterns as string literals. Measured over this tree at 1850f581: 275
// route references, of which 20 dangled in ggg/profile/minimal and
// ggg/profile/web — twenty live controls that 404 in the two smallest shipped
// profiles — plus the five controls the appearance and impersonation workflows
// own, which dangle in any project that deselects those workflows.
//
// # What a reference has to do about it
//
// Not declare an edge. `requires` is the mechanism for "I am meaningless
// without this", and for a route target it is usually not even available: a
// workflow's e2e spec drives the page that renders its controls, so
// ggg/workflow/appearance requires ggg/page/settings-account and
// ggg/system/server, and ggg/workflow/impersonation requires
// ggg/page/admin-overview. An edge from any of those to the workflow whose
// route it targets closes a cycle, which is what resolveSelectedGraph refuses.
// Eleven of the fourteen dangling reference pairs in this tree are cyclic in
// that way; of the three that are not, two would drag billing and a second
// admin page into ggg/profile/minimal, which is the closure those profiles
// exist to keep small, and the third is ggg/system/static — the shell floor,
// which must not depend on a product workflow at all.
//
// So the remedy is that the control is GATED, the way shell slots already are:
// templates.RouteAvailable(id) reports whether anything declares the route and
// templates.RoutePath(id, args...) resolves the target from the same records
// the mux is generated from. That also removes the literal, which is why this
// check has two halves — a path literal must resolve against the INSTALLED set,
// and a route id must exist in the CATALOG, because a route being absent from
// this project is the whole point of asking.
//
// # Scope, and the exclusions
//
// The alphabet is a closed list of positions where a value becomes the target
// of a request the framework issues: the markup attributes href, action and the
// five htmx method attributes; the typed option-struct spelling of those same
// attributes, which is how this codebase actually writes them; the third
// argument of Navigate and Redirect; and fetch and window.location in shipped
// browser sources. Nothing else. Measured exhaustively, EVERY path-shaped
// literal in every authored payload is 715 strings and 72 of them resolve to no
// route — outbound HTTP client paths for Polar, Typesense, Neon, Ably and OTLP,
// prefixes handed to strings.HasPrefix, subtree registrations, and the CLI
// source that emits route patterns as strings. A rule over "a string that looks
// like a path" is that measurement; a rule over declared positions is 275
// references with one finding.
//
// Excluded, enumerated: comments (stripped before scanning, which is what
// keeps a doc comment quoting `action="/upload"` from being a promise);
// absolute URLs with a scheme, including mailto: and tel:; protocol-relative
// `//host`; in-page anchors; relative values with no leading slash; the empty
// string; and any expression whose first term is not a string literal, because
// a target computed entirely at runtime has no pattern to compare. Generated
// payloads are skipped — they are rendered FROM the installed set and cannot
// disagree with it — and so are test payloads and vendored bundles.
//
// Method awareness was measured and REFUSED: keying each position to a verb
// costs 13 further refusals on this tree, all of them false. The gallery's
// inert `hx-delete="/dev/gallery"` demonstrations point back at the page they
// are on, and ui.FormOpts carries an explicit Method field that a
// position-to-verb table cannot see. So resolution is at pattern level, and
// "the method disagrees" is a named residual rather than a gate.
func ValidateRouteReferences(modules, catalog []Manifest, files map[string][]byte) error {
	declared := declaredRoutePatterns(modules)
	catalogRoutes := declaredRoutePatternsByID(catalog)
	if len(declared) == 0 && len(catalogRoutes) == 0 {
		return nil
	}

	ordered := append([]Manifest(nil), modules...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, module := range ordered {
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			if !isRouteReferencingPayload(file) {
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
			body := stripCommentsKeepingStrings(content)
			for _, ref := range routeReferences(target, body) {
				if resolvesToDeclaredRoute(declared, ref.path) {
					continue
				}
				return fmt.Errorf(
					"%s targets route %s at %s:%d, which no installed module declares; "+
						"either the route lost its owner (the control renders and 404s) or the path is wrong. "+
						"resolve the target through templates.RoutePath(\"<route id>\") and render it only when "+
						"templates.RouteAvailable(\"<route id>\") reports the route installed, or — when this module "+
						"is meaningless without it and the edge is acyclic — add the owning module to %s's requires",
					module.ID, ref.raw, target, ref.line, module.ID)
			}
			// The id half is arity only, and the omission is deliberate. A
			// gate exists precisely so a route that is NOT installed yields no
			// control, so "not installed" must never refuse — and a
			// derivative's catalog is not the publisher's either: `ggg new`
			// copies the core registry pruned to what the chosen profile can
			// reach, so ggg/profile/minimal's copy has no
			// ggg/page/admin-flag-detail and an existence check against it
			// would refuse the gate that page's absence is the reason for.
			// Measured doing exactly that on `ggg new --profile
			// ggg/profile/minimal`. Existence is asserted over the PUBLISHED
			// catalog instead, in route_scan_selfhost_test.go, which is where a
			// claim about this repository's own manifests belongs.
			for _, ref := range routeIDReferences(body) {
				pattern, known := catalogRoutes[ref.id]
				if !known || ref.args < 0 {
					continue
				}
				if ref.args != routePatternWildcards(pattern) {
					return fmt.Errorf(
						"%s resolves route id %q at %s:%d with %d path argument(s), but its declared pattern %s takes %d; "+
							"the surplus segments would be left unsubstituted in the URL",
						module.ID, ref.id, target, ref.line, ref.args, pattern, routePatternWildcards(pattern))
				}
			}
		}
	}
	return nil
}

// declaredRoutePatterns is every pattern an installed module declares, mapped
// to the module that declares it. Content types are expanded through
// moduleRoutes, the same projection the router is generated from.
func declaredRoutePatterns(modules []Manifest) map[string]string {
	declared := make(map[string]string)
	for _, module := range modules {
		for _, r := range moduleRoutes(module) {
			declared[r.contrib.Pattern] = module.ID
		}
	}
	return declared
}

// declaredRoutePatternsByID is the same expansion keyed by route id. The
// alphabet for the id half comes from the CATALOG rather than the installed
// graph, and that is the difference between this catching a typo and reporting
// a deselection: a gate exists precisely so an uninstalled route yields no
// control, so "not installed" must not refuse, while "no such route anywhere"
// must.
func declaredRoutePatternsByID(modules []Manifest) map[string]string {
	declared := make(map[string]string)
	for _, module := range modules {
		for _, r := range moduleRoutes(module) {
			declared[r.contrib.ID] = r.contrib.Pattern
		}
	}
	return declared
}

func routePatternWildcards(pattern string) int {
	count := 0
	for _, segment := range strings.Split(pattern, "/") {
		if isRoutePatternWildcard(segment) {
			count++
		}
	}
	return count
}

func isRoutePatternWildcard(segment string) bool {
	return len(segment) > 2 && segment[0] == '{' && segment[len(segment)-1] == '}' && segment != "{$}"
}

// resolvesToDeclaredRoute matches a referenced path against the declared
// patterns, with net/http's ServeMux semantics: a `{name}` segment matches one
// segment, a trailing `{$}` anchors the pattern to an exact path, and a pattern
// ending in `/` matches its whole subtree. A `*` in the reference is a segment
// the template computes, and matches any declared segment.
func resolvesToDeclaredRoute(declared map[string]string, reference string) bool {
	for pattern := range declared {
		if routePatternMatches(pattern, reference) {
			return true
		}
	}
	return false
}

func routePatternMatches(pattern, reference string) bool {
	exact := true
	switch {
	case strings.HasSuffix(pattern, "{$}"):
		pattern = strings.TrimSuffix(pattern, "{$}")
	case strings.HasSuffix(pattern, "/"):
		exact = false
		pattern = strings.TrimSuffix(pattern, "/")
	}
	declared := strings.Split(pattern, "/")
	referenced := strings.Split(reference, "/")
	if exact && len(declared) != len(referenced) {
		return false
	}
	if !exact && len(referenced) < len(declared) {
		return false
	}
	for i, segment := range declared {
		switch {
		case segment == referenced[i]:
		case isRoutePatternWildcard(segment):
		case referenced[i] == routeWildcardMark:
		default:
			return false
		}
	}
	return true
}

// routeWildcardMark stands for a segment the template computes. It is not a
// path character, so it can never collide with a declared segment.
const routeWildcardMark = "*"

type routeReference struct {
	// path is the reference with computed segments replaced by
	// routeWildcardMark and any query or fragment removed.
	path string
	// raw is what to print, which is the expression the payload actually
	// spells.
	raw  string
	line int
}

type routeIDReference struct {
	id string
	// args is the number of path arguments the call site supplies, or -1 when
	// the call is not a RoutePath resolution.
	args int
	line int
}

// routeAttributeReference is the markup form: one of the closed set of
// attributes whose value is a request target, followed by a quoted path or a
// templ expression.
var routeAttributeReference = regexp.MustCompile(`\b(?:href|action|hx-get|hx-post|hx-put|hx-patch|hx-delete)=`)

// routeFieldReference is the typed spelling of those same attributes: the
// option-struct fields this codebase's ui components take a request target in.
// It is an enumerated list rather than a suffix rule, because a name like
// BaseURL means "where to send this request" on a ui component and "which
// vendor host to call" on a provider client, and only the first is a route.
var routeFieldReference = regexp.MustCompile(
	`(?:^|[^\w.])(Href|Action|Get|Post|Put|Patch|Delete|BaseURL|ClearURL|GetURL|SearchURL|SubmitURL|MoveURL|PrevURL|NextURL|RetryURL|PreviewURL|FallbackURL|ActionHref)\s*:\s*`)

// routeRedirectReference is the server-side form: both of internal/web's
// redirect helpers take the target as their third argument.
var routeRedirectReference = regexp.MustCompile(`(?:^|[^\w.])(?:Navigate|Redirect)\(`)

// routeScriptReference is the browser form in shipped JavaScript.
var routeScriptReference = regexp.MustCompile(`\b(?:fetch|window\.location\.assign|window\.location\.replace)\(`)

// routeIDCall matches a resolution through the generated table. Both helpers
// are matched so a gate on a mistyped id is refused as loudly as a mistyped
// path would be.
var routeIDCall = regexp.MustCompile(`\b(RoutePath|RouteAvailable)\(`)

// routeReferences finds the route targets one payload names.
//
// Comments are already gone from body. String literals cannot be: a route
// reference IS a string literal, which is what makes this scan different from
// the ui one — there the payload's imports name the alphabet and a comment is
// the only text that can lie, here the position is the alphabet and a comment
// is still the only text that can lie.
func routeReferences(target string, body []byte) []routeReference {
	text := string(body)
	type position struct {
		at    int
		value string
	}
	positions := make([]position, 0, 8)
	extension := path.Ext(target)
	if extension == ".templ" || extension == ".html" {
		for _, match := range routeAttributeReference.FindAllStringIndex(text, -1) {
			positions = append(positions, position{match[1], captureAttributeValue(text, match[1])})
		}
	}
	if extension == ".templ" || (extension == ".go" && inTemplatesPackage(target)) {
		for _, match := range routeFieldReference.FindAllStringIndex(text, -1) {
			positions = append(positions, position{match[1], captureExpression(text, match[1])})
		}
	}
	if extension == ".go" {
		for _, match := range routeRedirectReference.FindAllStringIndex(text, -1) {
			if value, ok := callArgument(text, match[1], 2); ok {
				positions = append(positions, position{match[1], value})
			}
		}
	}
	if extension == ".js" {
		for _, match := range routeScriptReference.FindAllStringIndex(text, -1) {
			positions = append(positions, position{match[1], captureExpression(text, match[1])})
		}
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].at < positions[j].at })

	references := make([]routeReference, 0, len(positions))
	for _, found := range positions {
		resolved, ok := referencedRoutePath(found.value)
		if !ok {
			continue
		}
		references = append(references, routeReference{
			path: resolved,
			raw:  strings.TrimSpace(found.value),
			line: 1 + strings.Count(text[:found.at], "\n"),
		})
	}
	return references
}

// routeIDReferences finds every resolution through the generated table, with
// the number of path arguments the call site supplies.
func routeIDReferences(body []byte) []routeIDReference {
	text := string(body)
	references := make([]routeIDReference, 0)
	for _, match := range routeIDCall.FindAllStringSubmatchIndex(text, -1) {
		arguments := callArguments(text, match[1])
		if len(arguments) == 0 {
			continue
		}
		id, ok := goStringLiteralValue(strings.TrimSpace(arguments[0]))
		if !ok {
			// An id computed at runtime has no name to check. Nothing in this
			// tree does that, and a rule cannot be enforced over a value it
			// cannot see.
			continue
		}
		args := -1
		if text[match[2]:match[3]] == "RoutePath" {
			args = len(arguments) - 1
		}
		references = append(references, routeIDReference{
			id:   id,
			args: args,
			line: 1 + strings.Count(text[:match[1]], "\n"),
		})
	}
	return references
}

// referencedRoutePath turns one value expression into the path it addresses, or
// reports that it addresses none.
//
// Concatenation is the interesting case, and it is why a scan over bare string
// literals cannot work: `"/admin/users/" + u.UserID + "/impersonate"` is three
// literals, of which two resolve to no route on their own and the third is not
// even a prefix. Splitting on top-level `+` and standing a mark in for every
// computed term reconstructs `/admin/users/*/impersonate`, which is exactly the
// declared pattern with its wildcard filled.
func referencedRoutePath(expression string) (string, bool) {
	terms := splitTopLevelConcatenation(strings.TrimSpace(expression))
	resolved := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if value, ok := goStringLiteralValue(term); ok {
			resolved = append(resolved, value)
			continue
		}
		// A call whose first literal is the path: fmt.Sprintf("/a/%s", id) and
		// i18n.T(ctx, "settings.title") are the same syntax, and only the
		// leading-slash test below tells them apart.
		if len(resolved) == 0 {
			if literal, ok := firstGoStringLiteral(term); ok {
				resolved = append(resolved, printfVerb.ReplaceAllString(literal, routeWildcardMark))
				continue
			}
		}
		resolved = append(resolved, routeWildcardMark)
	}
	if len(resolved) == 0 || !strings.HasPrefix(resolved[0], "/") {
		return "", false
	}
	// A protocol-relative URL addresses another origin, and an empty first
	// segment is what distinguishes it from a path.
	if strings.HasPrefix(resolved[0], "//") {
		return "", false
	}
	joined := printfVerb.ReplaceAllString(strings.Join(resolved, ""), routeWildcardMark)
	joined = collapsedWildcards.ReplaceAllString(joined, routeWildcardMark)
	if index := strings.IndexAny(joined, "?#"); index >= 0 {
		joined = joined[:index]
	}
	if joined == "" {
		return "", false
	}
	return joined, true
}

var printfVerb = regexp.MustCompile(`%[-+ #0-9.*]*[a-zA-Z]`)

var collapsedWildcards = regexp.MustCompile(`\*+`)

// splitTopLevelConcatenation splits an expression on the `+` operators that are
// not inside brackets or string literals.
func splitTopLevelConcatenation(expression string) []string {
	terms := make([]string, 0, 4)
	depth := 0
	term := strings.Builder{}
	for index := 0; index < len(expression); {
		character := expression[index]
		if character == '"' || character == '`' {
			literal, width := scanStringLiteral(expression, index)
			term.WriteString(literal)
			index += width
			continue
		}
		switch character {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '+':
			if depth == 0 {
				terms = append(terms, term.String())
				term.Reset()
				index++
				continue
			}
		}
		term.WriteByte(character)
		index++
	}
	return append(terms, term.String())
}

// captureAttributeValue reads a markup attribute's value: either a quoted
// string or a templ `{ expression }`.
func captureAttributeValue(text string, at int) string {
	for at < len(text) && (text[at] == ' ' || text[at] == '\t') {
		at++
	}
	if at < len(text) && text[at] == '{' {
		depth := 0
		for index := at; index < len(text); {
			if text[index] == '"' || text[index] == '`' {
				_, width := scanStringLiteral(text, index)
				index += width
				continue
			}
			switch text[index] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[at+1 : index]
				}
			}
			index++
		}
		return text[at+1:]
	}
	return captureExpression(text, at)
}

// captureExpression reads one value expression: everything up to the comma,
// closing bracket or newline that ends it at bracket depth zero.
func captureExpression(text string, at int) string {
	depth := 0
	for index := at; index < len(text); {
		character := text[index]
		if character == '"' || character == '`' {
			_, width := scanStringLiteral(text, index)
			index += width
			continue
		}
		switch character {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return text[at:index]
			}
			depth--
		case ',':
			if depth == 0 {
				return text[at:index]
			}
		case '\n':
			if depth == 0 {
				return text[at:index]
			}
		}
		index++
	}
	return text[at:]
}

// callArguments splits a call's argument list, given the offset just after its
// opening parenthesis.
func callArguments(text string, at int) []string {
	arguments := make([]string, 0, 3)
	depth := 0
	argument := strings.Builder{}
	for index := at; index < len(text); {
		character := text[index]
		if character == '"' || character == '`' {
			literal, width := scanStringLiteral(text, index)
			argument.WriteString(literal)
			index += width
			continue
		}
		switch character {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return append(arguments, argument.String())
			}
			depth--
		case ',':
			if depth == 0 {
				arguments = append(arguments, argument.String())
				argument.Reset()
				index++
				continue
			}
		}
		argument.WriteByte(character)
		index++
	}
	return append(arguments, argument.String())
}

func callArgument(text string, at, index int) (string, bool) {
	arguments := callArguments(text, at)
	if index >= len(arguments) {
		return "", false
	}
	return arguments[index], true
}

// scanStringLiteral returns the literal starting at index and its width,
// including the quotes. An unterminated literal yields the single quote
// character, so a malformed payload cannot make the scanner run off the end.
func scanStringLiteral(text string, at int) (string, int) {
	quote := text[at]
	if quote == '`' {
		if end := strings.IndexByte(text[at+1:], '`'); end >= 0 {
			return text[at : at+2+end], end + 2
		}
		return text[at : at+1], 1
	}
	for index := at + 1; index < len(text); index++ {
		switch text[index] {
		case '\\':
			index++
		case '"':
			return text[at : index+1], index + 1 - at
		case '\n':
			return text[at : at+1], 1
		}
	}
	return text[at : at+1], 1
}

func goStringLiteralValue(term string) (string, bool) {
	if len(term) < 2 {
		return "", false
	}
	literal, width := scanStringLiteral(term, 0)
	if width != len(term) || len(literal) < 2 {
		return "", false
	}
	if literal[0] == '`' {
		return literal[1 : len(literal)-1], true
	}
	value, err := strconv.Unquote(literal)
	if err != nil {
		return "", false
	}
	return value, true
}

func firstGoStringLiteral(term string) (string, bool) {
	for index := range len(term) {
		if term[index] != '"' && term[index] != '`' {
			continue
		}
		literal, width := scanStringLiteral(term, index)
		if width < 2 {
			continue
		}
		return goStringLiteralValue(literal)
	}
	return "", false
}

// isRouteReferencingPayload reports whether a payload is authored source that
// can name a route target. Generated output is excluded because it is rendered
// FROM the installed set and can never disagree with it; test payloads because
// a test names the paths its fixture invents; vendored bundles because their
// bytes are a third party's and are pinned by digest, not authored here.
func isRouteReferencingPayload(file ManifestFile) bool {
	if file.Class == FileClassGenerated || file.Class == FileClassTest {
		return false
	}
	if strings.HasSuffix(file.Target, "_test.go") {
		return false
	}
	if strings.HasPrefix(file.Target, "static/vendor/") {
		return false
	}
	switch path.Ext(file.Target) {
	case ".go", ".templ", ".js", ".html":
		return true
	default:
		return false
	}
}

func inTemplatesPackage(target string) bool {
	directory := path.Dir(target)
	return directory == templatesPackageSuffix || strings.HasSuffix(directory, "/"+templatesPackageSuffix)
}

// stripCommentsKeepingStrings blanks the comment spans of a payload while
// keeping every string literal, byte offset and newline, so a refusal still
// names the line the reference is on.
//
// ui_scan.go's stripGoCommentsAndStrings blanks both, which is right there: a
// ui reference is an identifier, so a string can only lie. Here the reference
// IS a string, so only the comments can — and they do: markdown-editor.templ
// documents its upload contract by quoting `action="/upload"` in a doc comment,
// and that was one of two references this scan invented before comments were
// removed.
func stripCommentsKeepingStrings(content []byte) []byte {
	out := make([]byte, 0, len(content))
	for index := 0; index < len(content); {
		character := content[index]
		if character == '"' || character == '`' {
			literal, width := scanStringLiteral(string(content), index)
			out = append(out, literal...)
			index += width
			continue
		}
		if character == '/' && index+1 < len(content) && content[index+1] == '/' {
			for index < len(content) && content[index] != '\n' {
				out = append(out, ' ')
				index++
			}
			continue
		}
		if character == '/' && index+1 < len(content) && content[index+1] == '*' {
			end := strings.Index(string(content[index+2:]), "*/")
			stop := len(content)
			if end >= 0 {
				stop = index + 2 + end + 2
			}
			for ; index < stop; index++ {
				if content[index] == '\n' {
					out = append(out, '\n')
					continue
				}
				out = append(out, ' ')
			}
			continue
		}
		out = append(out, character)
		index++
	}
	return out
}
