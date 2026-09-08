// Self-host assertions. Declared self_host by ggg/system/modkit: the
// repository that publishes the registry installs and runs it, and no
// derivative ever receives it.
//
// Spec: AGENTS.md lines 79-93, the "Declarations live in …" paragraph.
//
// The audit that produced this file found the declaration list missing eight
// manifest keys and eight `runtime.*` keys, four of them schema-REQUIRED —
// provider slots, provisioners, database ops, deploy targets and CLI
// contributions were all invisible to an agent reading the document. The list
// had simply stopped being maintained alongside the model.
//
// So all three statements of the contract are held equal here: the document,
// `Manifest`/`RuntimeContributions`/`NamespaceClaims` in model.go, and
// `$defs` in registry/schema/module.schema.json. Two-way against the Go model
// alone would not do: the previous gate validated INSTANCES against the
// schema, which only ever proves the schema is permissive enough, never that
// it is strict enough.

package modkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// declarationParagraph is the collapsed "Declarations live in …" paragraph.
func declarationParagraph(t *testing.T) string {
	t.Helper()
	section := agentsSection(t, "Source vs generated — read this first")
	const anchor = "Declarations live in"
	start := strings.Index(section, anchor)
	if start < 0 {
		t.Fatalf("AGENTS.md no longer carries the %q paragraph this check compares against the model", anchor)
	}
	rest := section[start:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return collapse(rest)
}

// cutParenthetical removes the `(...)` that follows one code span, so the
// nested sub-lists inside the paragraph do not leak into the top-level key
// list. `data` is both a claims family and a manifest key, so the regions have
// to be excised rather than the members subtracted.
func cutParenthetical(t *testing.T, text, span string) string {
	t.Helper()
	at := strings.Index(text, span+" (")
	if at < 0 {
		t.Fatalf("AGENTS.md declaration paragraph no longer spells %s with its sub-list in parentheses", span)
	}
	open := at + len(span) + 1
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[:open] + text[i+1:]
			}
		}
	}
	t.Fatalf("AGENTS.md declaration paragraph has an unbalanced parenthesis after %s", span)
	return ""
}

var bareKey = regexp.MustCompile(`^[a-z0-9_]+$`)

// documentedKeys returns the bare lower-case keys one stretch of the paragraph
// names. A span carrying a dot, a slash or a capital is a symbol or a path,
// not a declaration key.
func documentedKeys(text string) []string {
	var keys []string
	for _, span := range backticked(text) {
		if bareKey.MatchString(span) {
			keys = append(keys, span)
		}
	}
	return keys
}

// jsonTags returns the JSON object keys one struct declares.
func jsonTags(t *testing.T, sample any) []string {
	t.Helper()
	typ := reflect.TypeOf(sample)
	var keys []string
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s carries no json tag, so it has no declaration key", typ.Name(), typ.Field(i).Name)
		}
		keys = append(keys, strings.Split(tag, ",")[0])
	}
	return keys
}

// schemaProperties returns the property names one published `$defs` entry
// declares.
func schemaProperties(t *testing.T, def string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "registry", "schema", "module.schema.json"))
	if err != nil {
		t.Fatalf("read module schema: %v", err)
	}
	var document struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode module schema: %v", err)
	}
	entry, ok := document.Defs[def]
	if !ok {
		t.Fatalf("registry/schema/module.schema.json declares no $defs.%s", def)
	}
	var keys []string
	for key := range entry.Properties {
		keys = append(keys, key)
	}
	return keys
}

// requireSameKeys asserts three statements of one key set agree, naming the
// members that are missing from each side. "Add X to Y" is the only useful
// failure here: a bare "mismatch" leaves the reader to diff 23 names by eye.
func requireSameKeys(t *testing.T, subject string, documented, model, schema []string) {
	t.Helper()
	diff := func(a, b []string) []string {
		have := map[string]bool{}
		for _, k := range b {
			have[k] = true
		}
		var out []string
		for _, k := range a {
			if !have[k] {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}

	if missing := diff(model, documented); len(missing) > 0 {
		t.Errorf("%s: the Go model declares %v, and AGENTS.md names none of them.\n"+
			"Add each key to the %s list in the AGENTS.md declaration paragraph — a field nobody documents is a declaration an agent never writes.",
			subject, missing, subject)
	}
	if stale := diff(documented, model); len(stale) > 0 {
		t.Errorf("%s: AGENTS.md names %v and the Go model has no such field.\n"+
			"Remove each one from the AGENTS.md declaration paragraph, or restore the field.", subject, stale)
	}
	if missing := diff(model, schema); len(missing) > 0 {
		t.Errorf("%s: the Go model declares %v and registry/schema/module.schema.json does not.\n"+
			"Add each property to the published schema; the schema IS the external extension contract.", subject, missing)
	}
	if extra := diff(schema, model); len(extra) > 0 {
		t.Errorf("%s: registry/schema/module.schema.json declares %v and the Go model has no such field.\n"+
			"Remove each property from the schema, or add the field.", subject, extra)
	}
	if len(documented) != len(model) {
		t.Errorf("%s: AGENTS.md names %d keys, the Go model declares %d", subject, len(documented), len(model))
	}
}

// docNumber reads a figure out of the declaration paragraph.
func docNumber(t *testing.T, text, pattern string) int {
	t.Helper()
	return docCount(t, text, pattern)
}

// The 23 manifest keys, three ways. A field added to Manifest without
// touching the document fails here naming the field.
func TestAgentsManifestKeysEqualTheModelAndTheSchema(t *testing.T) {
	paragraph := declarationParagraph(t)

	// The nested sub-lists are excised, not subtracted: `data`, `environment`,
	// `jobs`, `queries`, `openapi`, `ui` and `assets` are all both a claims
	// family and a manifest key.
	topLevel := cutParenthetical(t, paragraph, "`claims`")
	topLevel = cutParenthetical(t, topLevel, "`dependencies`")
	topLevel = regexp.MustCompile("`runtime\\.\\{[^`]*`").ReplaceAllString(topLevel, "")

	documented := append(documentedKeys(topLevel), "runtime")
	stated := docNumber(t, paragraph, "([0-9]+) manifest keys")
	if stated != len(documented) {
		t.Errorf("AGENTS.md says %d manifest keys and its own list names %d (%v).\nMake the figure and the list agree.",
			stated, len(documented), documented)
	}
	requireSameKeys(t, "manifest keys", documented, jsonTags(t, Manifest{}), schemaProperties(t, "Manifest"))
}

// The 18 `runtime.*` keys, three ways. The document writes them as one brace
// span, which is the abbreviation the check expands.
func TestAgentsRuntimeKeysEqualTheModelAndTheSchema(t *testing.T) {
	paragraph := declarationParagraph(t)
	span := regexp.MustCompile("`runtime\\.\\{([^`]*)\\}`").FindStringSubmatch(paragraph)
	if span == nil {
		t.Fatal("AGENTS.md no longer carries the `runtime.{…}` span this check expands; restore it or delete the check")
	}
	var documented []string
	for _, key := range strings.Split(span[1], ",") {
		if key = strings.TrimSpace(key); key != "" {
			documented = append(documented, key)
		}
	}
	if stated := docNumber(t, paragraph, "\\}` \\(([0-9]+)\\)"); stated != len(documented) {
		t.Errorf("AGENTS.md says %d runtime keys and its own span names %d (%v)", stated, len(documented), documented)
	}
	requireSameKeys(t, "runtime contribution keys", documented,
		jsonTags(t, RuntimeContributions{}), schemaProperties(t, "RuntimeContributions"))
}

// The 16 claims families, three ways. This is the collision plane: an
// undocumented family is a namespace nobody knows to claim.
func TestAgentsClaimFamiliesEqualTheModelAndTheSchema(t *testing.T) {
	paragraph := declarationParagraph(t)
	families := region(t, paragraph, "families:", ")")
	documented := documentedKeys(families)
	if stated := docNumber(t, paragraph, "([0-9]+) families"); stated != len(documented) {
		t.Errorf("AGENTS.md says %d claims families and its own list names %d (%v)", stated, len(documented), documented)
	}
	requireSameKeys(t, "claims families", documented,
		jsonTags(t, NamespaceClaims{}), schemaProperties(t, "NamespaceClaims"))
}

// The dependencies block, three ways.
func TestAgentsDependencyKeysEqualTheModelAndTheSchema(t *testing.T) {
	paragraph := declarationParagraph(t)
	block := region(t, paragraph, "`dependencies` (", ")")
	requireSameKeys(t, "dependency keys", documentedKeys(block),
		jsonTags(t, Dependencies{}), schemaProperties(t, "Dependencies"))
}
