package modkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

var publishedRegistryIncludes = []string{
	"registry/elements.json",
	"registry/components.json",
	"registry/pages.json",
	"registry/workflows.json",
	"registry/systems.json",
	"registry/profiles.json",
}

func putJSON(t *testing.T, files fstest.MapFS, name string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case RegistryRoot:
		if typed.Namespace == "" {
			typed.Namespace = "ggg"
		}
		if typed.CanonicalModule == "" {
			typed.CanonicalModule = "github.com/gogogadget/gogogadget"
		}
		value = typed
	case CatalogIndex:
		value = typed
	case ModuleDocument:
		if typed.Module.Dependencies.Go == nil {
			typed.Module.Dependencies.Go = []GoDependency{}
		}
		if typed.Module.Dependencies.Tools == nil {
			typed.Module.Dependencies.Tools = []ToolArtifact{}
		}
		if typed.Module.Dependencies.Containers == nil {
			typed.Module.Dependencies.Containers = []ContainerDependency{}
		}
		value = typed
	case ProfileDocument:
		if typed.Profile.RequiredProviderSlots == nil {
			typed.Profile.RequiredProviderSlots = []string{}
		}
		if typed.Profile.ProviderDefaults == nil {
			typed.Profile.ProviderDefaults = map[string]ProviderSelections{}
		}
		value = typed
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%s): %v", name, err)
	}
	files[name] = &fstest.MapFile{Data: data}
}

func registryFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	putJSON(t, files, "registry.json", RegistryRoot{Schema: 2, Namespace: "ggg", CanonicalModule: "github.com/gogogadget/gogogadget", Includes: append([]string(nil), publishedRegistryIncludes...)})
	for _, index := range []CatalogIndex{
		{Schema: 2, Kind: CatalogElement, Items: []string{"registry/modules/element/button/module.json"}},
		{Schema: 2, Kind: CatalogComponent, Items: []string{}},
		{Schema: 2, Kind: CatalogPage, Items: []string{}},
		{Schema: 2, Kind: CatalogWorkflow, Items: []string{}},
		{Schema: 2, Kind: CatalogSystem, Items: []string{}},
		{Schema: 2, Kind: CatalogProfile, Items: []string{"registry/profiles/full.json"}},
	} {
		name := "registry/" + string(index.Kind) + "s.json"
		putJSON(t, files, name, index)
	}
	module := testLockedModule("ggg/element/button", testDigestA).Manifest
	putJSON(t, files, "registry/modules/element/button/module.json", ModuleDocument{Schema: 2, Module: module})
	// The manifest DECLARES this payload, so the tree has to carry it:
	// ValidateRegistryTreeOwnership refuses a declared path with no bytes,
	// and a fixture that lies about its own contents tests the wrong thing.
	files["registry/modules/element/button/button.go"] = &fstest.MapFile{Data: []byte("package button\n")}
	putJSON(t, files, "registry/profiles/full.json", ProfileDocument{
		Schema: 2,
		Profile: Profile{
			ID:          "ggg/profile/full",
			Kind:        CatalogProfile,
			Name:        "full",
			Revision:    1,
			Contract:    1,
			Title:       "Full",
			Description: "Every production module.",
			Members:     []string{"ggg/element/button"}, RequiredProviderSlots: []string{}, ProviderDefaults: map[string]ProviderSelections{}, DefaultDeployment: "",
		},
	})
	return files
}

func TestLoadCatalog(t *testing.T) {
	catalog, err := LoadCatalog(registryFixture(t))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if got, want := len(catalog.Modules), 1; got != want {
		t.Fatalf("module count = %d, want %d", got, want)
	}
	if got, want := catalog.Modules[0].ID, "ggg/element/button"; got != want {
		t.Fatalf("module id = %q, want %q", got, want)
	}
	if got, want := len(catalog.Profiles), 1; got != want {
		t.Fatalf("profile count = %d, want %d", got, want)
	}
	if got, want := catalog.Profiles[0].Members[0], "ggg/element/button"; got != want {
		t.Fatalf("profile member = %q, want %q", got, want)
	}
}

func TestLoadCatalogRejectsInvalidCatalogs(t *testing.T) {
	t.Run("missing required include", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry.json", RegistryRoot{Schema: 2, Namespace: "ggg", CanonicalModule: "github.com/gogogadget/gogogadget", Includes: publishedRegistryIncludes[:5]})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "includes") {
			t.Fatalf("LoadCatalog error = %v, want includes rejection", err)
		}
	})

	t.Run("index kind must match include", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry/elements.json", CatalogIndex{Schema: 2, Kind: CatalogComponent, Items: []string{}})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "kind") {
			t.Fatalf("LoadCatalog error = %v, want kind rejection", err)
		}
	})

	t.Run("duplicate module id", func(t *testing.T) {
		files := registryFixture(t)
		module := testLockedModule("ggg/element/button", testDigestA).Manifest
		putJSON(t, files, "registry/modules/element/button/duplicate.json", ModuleDocument{Schema: 2, Module: module})
		putJSON(t, files, "registry/elements.json", CatalogIndex{
			Schema: 2,
			Kind:   CatalogElement,
			Items: []string{
				"registry/modules/element/button/duplicate.json",
				"registry/modules/element/button/module.json",
			},
		})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("LoadCatalog error = %v, want duplicate id rejection", err)
		}
	})

	t.Run("profile member must exist", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry/profiles/full.json", ProfileDocument{
			Schema: 2,
			Profile: Profile{
				ID: "ggg/profile/full", Kind: CatalogProfile, Name: "full", Revision: 1, Contract: 1,
				Title: "Full", Description: "Every production module.", Members: []string{"ggg/element/missing"},
			},
		})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "member") {
			t.Fatalf("LoadCatalog error = %v, want profile member rejection", err)
		}
	})

	t.Run("test-only module cannot enter production index", func(t *testing.T) {
		files := registryFixture(t)
		module := testLockedModule("ggg/element/button", testDigestA).Manifest
		module.TestOnly = true
		putJSON(t, files, "registry/modules/element/button/module.json", ModuleDocument{Schema: 2, Module: module})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "test_only") {
			t.Fatalf("LoadCatalog error = %v, want test_only rejection", err)
		}
	})

	for _, tt := range []struct {
		name string
		item string
	}{
		{name: "parent traversal item", item: "registry/modules/element/../../secret.json"},
		{name: "absolute item", item: "/etc/passwd"},
		{name: "backslash item", item: `registry\modules\element\button.json`},
		{name: "non-json item", item: "registry/modules/element/button/module.go"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := registryFixture(t)
			putJSON(t, files, "registry/elements.json", CatalogIndex{Schema: 2, Kind: CatalogElement, Items: []string{tt.item}})
			_, err := LoadCatalog(files)
			if err == nil || !strings.Contains(err.Error(), "path") {
				t.Fatalf("LoadCatalog error = %v, want item path rejection", err)
			}
		})
	}

	t.Run("item prefix must match index kind", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry/elements.json", CatalogIndex{
			Schema: 2, Kind: CatalogElement, Items: []string{"registry/modules/system/example/module.json"},
		})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "stay under") {
			t.Fatalf("LoadCatalog error = %v, want kind-scoped prefix rejection", err)
		}
	})

	t.Run("document kind must match index kind", func(t *testing.T) {
		files := registryFixture(t)
		module := testLockedModule("ggg/component/button", testDigestA).Manifest
		putJSON(t, files, "registry/modules/element/button/module.json", ModuleDocument{Schema: 2, Module: module})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "kind") {
			t.Fatalf("LoadCatalog error = %v, want document kind rejection", err)
		}
	})

	t.Run("items must be sorted", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry/elements.json", CatalogIndex{
			Schema: 2, Kind: CatalogElement,
			Items: []string{"registry/modules/element/z/module.json", "registry/modules/element/a/module.json"},
		})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "sorted") {
			t.Fatalf("LoadCatalog error = %v, want sorted rejection", err)
		}
	})

	t.Run("items must be unique", func(t *testing.T) {
		files := registryFixture(t)
		putJSON(t, files, "registry/elements.json", CatalogIndex{
			Schema: 2, Kind: CatalogElement,
			Items: []string{"registry/modules/element/button/module.json", "registry/modules/element/button/module.json"},
		})
		_, err := LoadCatalog(files)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("LoadCatalog error = %v, want duplicate item rejection", err)
		}
	})

	t.Run("schema versions are closed", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, fstest.MapFS)
		}{
			{
				name: "root",
				mutate: func(t *testing.T, files fstest.MapFS) {
					putJSON(t, files, "registry.json", RegistryRoot{Schema: 3, Namespace: "ggg", CanonicalModule: "github.com/gogogadget/gogogadget", Includes: publishedRegistryIncludes})
				},
			},
			{
				name: "index",
				mutate: func(t *testing.T, files fstest.MapFS) {
					putJSON(t, files, "registry/elements.json", CatalogIndex{Schema: 3, Kind: CatalogElement, Items: []string{}})
				},
			},
			{
				name: "document",
				mutate: func(t *testing.T, files fstest.MapFS) {
					module := testLockedModule("ggg/element/button", testDigestA).Manifest
					putJSON(t, files, "registry/modules/element/button/module.json", ModuleDocument{Schema: 3, Module: module})
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				files := registryFixture(t)
				tt.mutate(t, files)
				_, err := LoadCatalog(files)
				if err == nil || !strings.Contains(err.Error(), "schema") {
					t.Fatalf("LoadCatalog error = %v, want schema rejection", err)
				}
			})
		}
	})

	for _, tt := range []struct {
		name string
		data string
		want string
	}{
		{name: "unknown field", data: `{"schema":2,"kind":"element","items":[],"extra":true}`, want: "unknown field"},
		{name: "duplicate key", data: `{"schema":2,"schema":2,"kind":"element","items":[]}`, want: "duplicate"},
		{name: "trailing data", data: `{"schema":2,"kind":"element","items":[]} {}`, want: "trailing"},
		{name: "null items", data: `{"schema":2,"kind":"element","items":null}`, want: "null"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := registryFixture(t)
			files["registry/elements.json"] = &fstest.MapFile{Data: []byte(tt.data)}
			_, err := LoadCatalog(files)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadCatalog error = %v, want %q rejection", err, tt.want)
			}
		})
	}

	t.Run("returned modules are sorted by id", func(t *testing.T) {
		files := registryFixture(t)
		module := testLockedModule("ggg/component/card", testDigestB).Manifest
		putJSON(t, files, "registry/modules/component/card/module.json", ModuleDocument{Schema: 2, Module: module})
		putJSON(t, files, "registry/components.json", CatalogIndex{
			Schema: 2, Kind: CatalogComponent, Items: []string{"registry/modules/component/card/module.json"},
		})
		catalog, err := LoadCatalog(files)
		if err != nil {
			t.Fatalf("LoadCatalog: %v", err)
		}
		if got, want := catalog.Modules[0].ID, "ggg/component/card"; got != want {
			t.Fatalf("first module = %q, want %q", got, want)
		}
	})
}

func TestParseLockEnforcesCatalogRequiredFields(t *testing.T) {
	withIncompleteRoute := strings.Replace(
		canonicalLockJSON,
		`"runtime": {}`,
		`"runtime": {"routes":[{"id":"route","method":"GET","pattern":"/route","scope":"public","package":"example.com/acme/web","handler":"Handle"}]}`,
		1,
	)
	_, err := ParseLock([]byte(withIncompleteRoute))
	if err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("ParseLock error = %v, want missing policy rejection", err)
	}
}

// assertSchemaDefinition proves the parity between one Go model and its
// published JSON Schema definition in three parts, because the contract has
// three kinds of requirement and one keyword cannot carry all three.
//
//   - Shape: the property set is exactly the tagged field set, every property
//     has the declared type, and every struct property $refs a real definition.
//   - Unconditional requirement: every field WITHOUT `omitempty` is in
//     `required`, which is the same set requireJSONValue refuses a missing key
//     for. This is the half the decoder and the schema must agree on exactly.
//   - Conditional requirement: a field WITH `omitempty` may never appear in
//     `required` — that would make the contract refuse a manifest the decoder
//     accepts — so a rule like "provisioner is required for provision
//     automation" has to live in `if`/`then`, `dependentRequired`, `oneOf` or
//     `not` instead. conditional is the set of `Definition.field` pairs read
//     back out of the schema's conditional keywords, and recorded names the
//     rules the validator enforces, so dropping one from the schema fails here
//     rather than only when the catalog is re-measured.
//
// The two required assertions are separate and directional on purpose: which
// way parity broke decides whether the fix is a tag, a `required` entry or a
// conditional keyword.
func assertSchemaDefinition(
	t *testing.T, definitions map[string]map[string]any, definitionName string, modelType reflect.Type,
	conditional map[string]bool, recorded map[string]string, aliases map[string]string,
) {
	t.Helper()
	definition, ok := definitions[definitionName]
	if !ok {
		t.Fatalf("schema definition %s is missing", definitionName)
	}
	assertSchemaShape(t, definitions, definitionName, definition, modelType, conditional, recorded, aliases)
}

// assertSchemaShape is the body of the above over one schema NODE. A struct
// property may state its shape by `$ref` or inline — the project document
// does both — and an inline object is not an excuse to assert nothing about
// it, so the same three comparisons recurse into it.
func assertSchemaShape(
	t *testing.T, definitions map[string]map[string]any, definitionName string,
	definition map[string]any, modelType reflect.Type,
	conditional map[string]bool, recorded map[string]string, aliases map[string]string,
) {
	t.Helper()
	if got := definition["type"]; got != "object" {
		t.Fatalf("schema definition %s type = %v, want object", definitionName, got)
	}
	if got := definition["additionalProperties"]; got != false {
		t.Fatalf("schema definition %s additionalProperties = %v, want false", definitionName, got)
	}
	properties, ok := definition["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema definition %s properties is not an object", definitionName)
	}
	// refersTo reports whether a `$ref` names the definition a Go type is
	// published as. A type may be published under more than one name — the
	// project document restates two lock definitions under its own names —
	// and aliases is the only place that is written down. It arrives as a
	// parameter because this file ships to derivative projects, which do not
	// carry the self-host inventory that defines it.
	refersTo := func(ref string, goType string) bool {
		_, name, found := strings.Cut(ref, "#/$defs/")
		if !found {
			return false
		}
		if alias, ok := aliases[name]; ok {
			name = alias
		}
		return name == goType
	}

	var wantProperties, wantRequired []string
	for i := range modelType.NumField() {
		field := modelType.Field(i)
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		wantProperties = append(wantProperties, parts[0])
		if !slices.Contains(parts[1:], "omitempty") {
			wantRequired = append(wantRequired, parts[0])
		}
		property, ok := properties[parts[0]].(map[string]any)
		if !ok {
			t.Fatalf("schema definition %s property %s is not an object", definitionName, parts[0])
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		switch fieldType.Kind() {
		case reflect.Bool:
			if got := property["type"]; got != "boolean" {
				t.Fatalf("schema definition %s property %s type = %v, want boolean", definitionName, parts[0], got)
			}
		case reflect.Int, reflect.Int64:
			if got := property["type"]; got != "integer" {
				t.Fatalf("schema definition %s property %s type = %v, want integer", definitionName, parts[0], got)
			}
		case reflect.String:
			if got := property["type"]; got != "string" {
				t.Fatalf("schema definition %s property %s type = %v, want string", definitionName, parts[0], got)
			}
		case reflect.Slice:
			// A named byte slice is a raw JSON region the decoder hands
			// through untouched (jsontext.Value): OpenAPI `info`, `servers`,
			// `responses`. Its shape is the OpenAPI document's business, not
			// this model's, so the schema states it and reflection has
			// nothing to compare against.
			if fieldType.Elem().Kind() == reflect.Uint8 {
				break
			}
			if got := property["type"]; got != "array" {
				t.Fatalf("schema definition %s property %s type = %v, want array", definitionName, parts[0], got)
			}
			if _, hasItems := property["items"].(map[string]any); !hasItems {
				if _, hasPrefixItems := property["prefixItems"].([]any); !hasPrefixItems {
					t.Fatalf("schema definition %s property %s has neither items nor prefixItems", definitionName, parts[0])
				}
			}
		case reflect.Map:
			// A Go map is a JSON object with open keys. The schema may state
			// the value shape inline or by reference; where it references,
			// the reference must name the element type — a `$ref` at the
			// wrong definition is the drift a map property can carry, and
			// there was no arm here to catch it.
			if got := property["type"]; got != "object" {
				t.Fatalf("schema definition %s property %s type = %v, want object", definitionName, parts[0], got)
			}
			values, ok := property["additionalProperties"].(map[string]any)
			if !ok {
				break
			}
			ref, ok := values["$ref"].(string)
			if !ok {
				break
			}
			element := fieldType.Elem()
			for element.Kind() == reflect.Pointer {
				element = element.Elem()
			}
			if !refersTo(ref, element.Name()) {
				t.Fatalf("schema definition %s property %s values ref %s, want the definition published for %s",
					definitionName, parts[0], ref, element.Name())
			}
		case reflect.Struct:
			ref, hasRef := property["$ref"].(string)
			if !hasRef {
				// Stated inline. Assert it rather than skipping it: the
				// project document writes ProviderSelection's two fields out
				// at each of its three environment properties, and an inline
				// shape drifting from the model is the same defect a wrong
				// `$ref` would be.
				if _, hasProperties := property["properties"].(map[string]any); !hasProperties {
					t.Fatalf("schema definition %s property %s neither $refs %s nor states its properties inline",
						definitionName, parts[0], fieldType.Name())
				}
				assertSchemaShape(t, definitions, definitionName+"."+parts[0], property, fieldType, conditional, recorded, aliases)
				break
			}
			if !refersTo(ref, fieldType.Name()) {
				t.Fatalf("schema definition %s property %s ref = %v, want %s", definitionName, parts[0], ref, fieldType.Name())
			}
			if _, name, _ := strings.Cut(ref, "#/$defs/"); definitions[name] == nil {
				t.Fatalf("schema definition %s property %s references missing %s", definitionName, parts[0], name)
			}
		}
	}
	gotProperties := make([]string, 0, len(properties))
	for name := range properties {
		gotProperties = append(gotProperties, name)
	}
	// A definition with no `required` list requires nothing, which is a legal
	// and used shape: OpenAPIContribution's every field carries omitempty.
	// Absent and empty must read the same here, or the parity comparison
	// below would fatal before it ever ran.
	requiredValue, _ := definition["required"].([]any)
	var gotRequired []string
	for _, raw := range requiredValue {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("schema definition %s required contains non-string %T", definitionName, raw)
		}
		gotRequired = append(gotRequired, value)
	}
	slices.Sort(wantProperties)
	slices.Sort(wantRequired)
	slices.Sort(gotProperties)
	slices.Sort(gotRequired)
	if !slices.Equal(gotProperties, wantProperties) {
		t.Fatalf("schema definition %s properties = %v, want %v", definitionName, gotProperties, wantProperties)
	}
	for _, name := range wantRequired {
		if !slices.Contains(gotRequired, name) {
			t.Fatalf("schema definition %s does not require %s, which carries no omitempty and "+
				"is therefore refused when absent by requireJSONValue: the published contract "+
				"accepts a manifest ggg refuses", definitionName, name)
		}
	}
	for _, name := range gotRequired {
		if !slices.Contains(wantRequired, name) {
			t.Fatalf("schema definition %s requires %s, which carries omitempty and is therefore "+
				"optional to the decoder: the published contract refuses a manifest ggg accepts. "+
				"A requirement that only holds sometimes belongs in if/then, dependentRequired, "+
				"oneOf or not, never in required", definitionName, name)
		}
	}
	for _, name := range wantProperties {
		key := modelType.Name() + "." + name
		rule, isRecorded := recorded[key]
		if !isRecorded {
			continue
		}
		if !conditional[key] && !conditional[definitionName+"."+name] {
			t.Fatalf("schema definition %s states no conditional requirement about %s, and the "+
				"validator enforces one (%s). `required` cannot carry it: the field is optional "+
				"whenever the condition is absent, so a third party's manifest would validate "+
				"clean and be refused by ggg", definitionName, name, rule)
		}
	}
}

// A content-type contribution names generated routes and a handler, so a bad
// declaration must be refused by registry validation — before generation emits a
// route table and before any runtime boots against it. Catching it at boot would
// already be too late: the generated aggregate is committed.
//
// The valid case is asserted first. Without that control every subtable would
// pass even if the mutation under test were harmless, because a broken base
// fixture fails for its own reasons.
func TestInvalidContentTypeManifestRejected(t *testing.T) {
	validContribution := func() map[string]any {
		return map[string]any{
			"id": "blog", "mode": "pages", "paths": []string{"/blog"},
			"package": "internal/web", "handler": "handleBlog",
		}
	}
	catalogWith := func(t *testing.T, contribution map[string]any) (Catalog, error) {
		t.Helper()
		module := map[string]any{
			"id": "ggg/system/broken", "kind": "system", "name": "broken",
			"revision": 1, "contract": 1, "title": "Broken",
			"description": "A module with a content type declaration.",
			"requires":    []any{}, "files": []any{}, "claims": map[string]any{},
			"runtime":    map[string]any{"content_types": []any{contribution}},
			"migrations": []any{}, "environment": []any{}, "docs": []any{},
			"tests": map[string]any{}, "data": []any{},
			"dependencies":   map[string]any{"go": []any{}, "tools": []any{}, "containers": []any{}},
			"removal_policy": "free",
		}
		document, err := json.Marshal(map[string]any{"schema": 2, "module": module})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		files := registryFixture(t)
		files["registry/systems.json"] = &fstest.MapFile{
			Data: []byte(`{"schema":2,"kind":"system","items":["registry/modules/system/broken/module.json"]}`),
		}
		files["registry/modules/system/broken/module.json"] = &fstest.MapFile{Data: document}
		return LoadCatalog(files)
	}

	if _, err := catalogWith(t, validContribution()); err != nil {
		t.Fatalf("control: a valid content type declaration was rejected: %v", err)
	}

	cases := map[string]func(map[string]any){
		"empty id":        func(m map[string]any) { m["id"] = "" },
		"unknown mode":    func(m map[string]any) { m["mode"] = "carousel" },
		"relative path":   func(m map[string]any) { m["paths"] = []string{"blog"} },
		"traversal path":  func(m map[string]any) { m["paths"] = []string{"/../etc/passwd"} },
		"missing paths":   func(m map[string]any) { delete(m, "paths") },
		"bad package":     func(m map[string]any) { m["package"] = "../internal/web" },
		"bad handler":     func(m map[string]any) { m["handler"] = "1handler" },
		"empty handler":   func(m map[string]any) { m["handler"] = "" },
		"duplicate paths": func(m map[string]any) { m["paths"] = []string{"/blog", "/blog"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			contribution := validContribution()
			mutate(contribution)
			if _, err := catalogWith(t, contribution); err == nil {
				t.Fatalf("LoadCatalog accepted a %s content type declaration", name)
			}
		})
	}
}

// envManifest is a manifest whose only interesting content is one environment
// declaration, so a validation failure names the declaration rather than some
// unrelated missing array.
func envManifest(item EnvironmentVariable, packages ...string) Manifest {
	return Manifest{
		ID: "ggg/system/hatch", Kind: ModuleSystem, Name: "hatch",
		Revision: 1, Contract: 1, Title: "Hatch", Description: "Hatch system.",
		Requires: []Requirement{}, Files: []ManifestFile{}, Migrations: []ManifestMigration{},
		Docs: []DocumentationRef{}, Data: []DataDeclaration{}, RemovalPolicy: RemovalFree,
		Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		Claims:       NamespaceClaims{Packages: packages},
		Environment:  []EnvironmentVariable{item},
	}
}

// The two declarations that let an adapter own its configuration behaviour are
// only safe if their semantics are enforced: a refusal reads a bool field, and
// a derivation emits a call into a package the declaring module owns. Anything
// else generates code that does not compile, or code that outlives the module.
func TestValidateManifestEnforcesRefusalAndDerivationSemantics(t *testing.T) {
	derivation := func(mutate func(*EnvironmentDerivation)) *EnvironmentDerivation {
		d := EnvironmentDerivation{Package: "internal/hatch/origin", Function: "Origin", Inputs: []string{"APP_URL"}}
		if mutate != nil {
			mutate(&d)
		}
		return &d
	}
	good := EnvironmentVariable{Key: "HATCH_ORIGIN", Field: "HatchOrigin", Type: EnvString,
		Description: "browser origin", Derivation: derivation(nil)}
	if err := ValidateManifest(envManifest(good, "internal/hatch/origin")); err != nil {
		t.Fatalf("a well-formed derivation was refused: %v", err)
	}
	bypass := EnvironmentVariable{Key: "HATCH_BYPASS", Field: "HatchBypass", Type: EnvBool,
		Description: "synthetic sessions", RefusedInProduction: true}
	if err := ValidateManifest(envManifest(bypass)); err != nil {
		t.Fatalf("a well-formed refusal was refused: %v", err)
	}

	for name, manifest := range map[string]Manifest{
		"refusal on a non-bool": envManifest(EnvironmentVariable{Key: "HATCH_BYPASS", Field: "HatchBypass",
			Type: EnvString, Description: "synthetic sessions", RefusedInProduction: true}),
		"derivation on a non-string": envManifest(EnvironmentVariable{Key: "HATCH_ORIGIN", Field: "HatchOrigin",
			Type: EnvBool, Description: "browser origin", Derivation: derivation(nil)}, "internal/hatch/origin"),
		"derivation into an unclaimed package": envManifest(good),
		"derivation with no inputs": envManifest(EnvironmentVariable{Key: "HATCH_ORIGIN", Field: "HatchOrigin",
			Type: EnvString, Description: "browser origin",
			Derivation: derivation(func(d *EnvironmentDerivation) { d.Inputs = nil })}, "internal/hatch/origin"),
		"derivation calling an unexported function": envManifest(EnvironmentVariable{Key: "HATCH_ORIGIN",
			Field: "HatchOrigin", Type: EnvString, Description: "browser origin",
			Derivation: derivation(func(d *EnvironmentDerivation) { d.Function = "origin" })}, "internal/hatch/origin"),
		"derivation reading itself": envManifest(EnvironmentVariable{Key: "HATCH_ORIGIN", Field: "HatchOrigin",
			Type: EnvString, Description: "browser origin",
			Derivation: derivation(func(d *EnvironmentDerivation) { d.Inputs = []string{"HATCH_ORIGIN"} })}, "internal/hatch/origin"),
		"derivation escaping the project": envManifest(EnvironmentVariable{Key: "HATCH_ORIGIN", Field: "HatchOrigin",
			Type: EnvString, Description: "browser origin",
			Derivation: derivation(func(d *EnvironmentDerivation) { d.Package = "../elsewhere" })}, "../elsewhere"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("ValidateManifest accepted a declaration the generator cannot emit safely")
			}
		})
	}
}

// routeManifest is a manifest whose only interesting content is one route, so a
// refusal names the route policy rather than some unrelated missing array.
func routeManifest(route RouteContribution) Manifest {
	return Manifest{
		ID: "ggg/system/hatch", Kind: ModuleSystem, Name: "hatch",
		Revision: 1, Contract: 1, Title: "Hatch", Description: "Hatch system.",
		Requires: []Requirement{}, Files: []ManifestFile{}, Migrations: []ManifestMigration{},
		Docs: []DocumentationRef{}, Data: []DataDeclaration{}, RemovalPolicy: RemovalFree,
		Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		Claims:       NamespaceClaims{Routes: []string{route.ID}},
		Runtime:      RuntimeContributions{Routes: []RouteContribution{route}},
	}
}

// RoutePolicy.Idempotent reaches exactly two consumers, and both of them are
// the API transport: scopeTargets.target wraps the handler in the
// idempotency-key middleware under `case ScopeAPIRead, ScopeAPIWrite`, and the
// generated OpenAPI document derives its Idempotency-Key parameter from the
// same field. Declared on any other scope it documents a retry contract that
// reaches no middleware — nine routes carried it while two were wrapped, and
// two of the nine were GETs, where a retry key has nothing to deduplicate.
//
// A route that IS retry-safe some other way makes a different claim: the local
// billing POSTs and both hosted webhook receivers dedupe on a server-derived id
// in the webhook_events ledger, which needs no client header, and each states
// that where it lives rather than borrowing this field's name for it.
func TestValidateManifestRefusesAnUnenforceableIdempotencyClaim(t *testing.T) {
	route := func(id string, method string, scope RouteScope, idempotent bool) RouteContribution {
		return RouteContribution{
			ID: id, Method: method, Pattern: "/api/v1/things", Scope: scope,
			Package: "internal/web", Handler: "handleThings",
			Policy: RoutePolicy{Idempotent: idempotent},
		}
	}

	// The two controls: the shape the transport really enforces, and the same
	// route without the flag. Without them every row below would pass even if
	// the mutation were harmless.
	if err := ValidateManifest(routeManifest(route("api.things.create", "POST", RouteAPIWrite, true))); err != nil {
		t.Fatalf("control: an enforced idempotency declaration was refused: %v", err)
	}
	if err := ValidateManifest(routeManifest(route("app.things.create", "POST", RouteApp, false))); err != nil {
		t.Fatalf("control: an app POST with no declaration was refused: %v", err)
	}

	for name, contribution := range map[string]RouteContribution{
		"an app POST":        route("app.things.create", "POST", RouteApp, true),
		"an app GET":         route("app.things.show", "GET", RouteApp, true),
		"an admin POST":      route("admin.things.create", "POST", RouteAdmin, true),
		"a webhook receiver": route("webhook.things.receive", "POST", RouteWebhook, true),
		"a public POST":      route("public.things.create", "POST", RoutePublic, true),
		"a dev POST":         route("dev.things.create", "POST", RouteDev, true),
		"an api-write GET":   route("api.things.list", "GET", RouteAPIWrite, true),
		"an api-read GET":    route("api.things.read", "GET", RouteAPIRead, true),
		"an api-read HEAD":   route("api.things.head", "HEAD", RouteAPIRead, true),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateManifest(routeManifest(contribution)); err == nil {
				t.Fatal("ValidateManifest accepted an idempotency claim no middleware applies")
			}
		})
	}
}

// scanManifest is an adapter or reader with exactly the fields the two config
// scans look at.
func scanManifest(id string, requires []string, adapter bool, env []EnvironmentVariable, targets ...string) Manifest {
	m := Manifest{
		ID: id, Kind: ModuleSystem, Environment: env,
		Requires: make([]Requirement, 0, len(requires)),
		Files:    make([]ManifestFile, 0, len(targets)),
	}
	for _, r := range requires {
		m.Requires = append(m.Requires, Requirement{ID: r, Contract: ContractBounds{Min: 1, Max: 1}})
	}
	for _, t := range targets {
		m.Files = append(m.Files, ManifestFile{Source: t, Target: t, Class: FileClassGo})
	}
	if adapter {
		m.Runtime.System = &SystemContribution{Package: "internal/adapter", Adapter: &AdapterContribution{Slot: "ggg/slot"}}
	}
	return m
}

// The typed Config field belongs to the module that declares the key and
// vanishes with it, so a reader that cannot guarantee the module is installed
// must read by key. Without this scan the rule is prose: it was stated in the
// docs and violated in the same range that stated it.
func TestValidateConfigFieldOwnership(t *testing.T) {
	adapter := scanManifest("ggg/system/hatch", nil, true,
		[]EnvironmentVariable{{Key: "HATCH_BYPASS", Field: "HatchBypass", Type: EnvBool, Description: "d"}},
		"internal/hatch/adapter.go")
	read := []byte("package x\n\nfunc f(cfg C) bool { return cfg.HatchBypass }\n")
	literal := []byte("package x\n\nimport \"c\"\n\nvar v = c.Config{HatchBypass: true}\n")
	byKey := []byte("package x\n\nfunc f(cfg C) bool { return cfg.BoolValue(\"HATCH_BYPASS\") }\n")
	other := []byte("package x\n\nfunc f(page P) bool { return page.HatchBypass }\n")

	for name, tc := range map[string]struct {
		reader  Manifest
		content []byte
		refuse  bool
	}{
		"a stranger reading the field":      {scanManifest("ggg/page/other", nil, false, nil, "internal/other/x.go"), read, true},
		"a stranger naming it in a literal": {scanManifest("ggg/page/other", nil, false, nil, "internal/other/x.go"), literal, true},
		"the same stranger reading by key":  {scanManifest("ggg/page/other", nil, false, nil, "internal/other/x.go"), byKey, false},
		"a non-config receiver":             {scanManifest("ggg/page/other", nil, false, nil, "internal/other/x.go"), other, false},
		"a module that requires the declarer": {
			scanManifest("ggg/page/other", []string{"ggg/system/hatch"}, false, nil, "internal/other/x.go"), read, false},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string][]byte{"internal/other/x.go": tc.content}
			err := ValidateConfigFieldOwnership([]Manifest{adapter, tc.reader}, files)
			if tc.refuse && err == nil {
				t.Fatal("ValidateConfigFieldOwnership accepted a read that removal would break")
			}
			if !tc.refuse && err != nil {
				t.Fatalf("ValidateConfigFieldOwnership refused a sound read: %v", err)
			}
		})
	}

	// The declaring module reads its own field freely; that is the whole point
	// of owning it.
	own := map[string][]byte{"internal/hatch/adapter.go": read}
	if err := ValidateConfigFieldOwnership([]Manifest{adapter}, own); err != nil {
		t.Fatalf("the declaring module was refused its own field: %v", err)
	}
}

// A derivation runs inside the generated config loader, so its package must
// not reach back. EnvironmentDerivation documents that; this is what makes it
// true, and turns an import cycle in generated code into a manifest error.
func TestValidateDerivationPackagesRefusesAReachBackToConfig(t *testing.T) {
	declarer := scanManifest("ggg/system/hatch", nil, true,
		[]EnvironmentVariable{{Key: "HATCH_ORIGIN", Field: "HatchOrigin", Type: EnvString, Description: "d",
			Derivation: &EnvironmentDerivation{Package: "internal/hatch/origin", Function: "Origin", Inputs: []string{"APP_URL"}}}},
		"internal/hatch/origin/origin.go")
	modules := []Manifest{declarer}

	leaf := map[string][]byte{"internal/hatch/origin/origin.go": []byte("package origin\n\nimport \"strings\"\n\nvar _ = strings.TrimSpace\n")}
	if err := ValidateDerivationPackages(modules, leaf, "example.com/app"); err != nil {
		t.Fatalf("a genuine leaf was refused: %v", err)
	}

	direct := map[string][]byte{"internal/hatch/origin/origin.go": []byte("package origin\n\nimport \"example.com/app/internal/config\"\n\nvar _ = config.Config{}\n")}
	if err := ValidateDerivationPackages(modules, direct, "example.com/app"); err == nil {
		t.Fatal("a derivation package importing internal/config was accepted")
	}

	// Transitive too: one hop is the shape a leaf actually acquires, by
	// reaching for a helper that happens to read configuration.
	indirect := map[string][]byte{
		"internal/hatch/origin/origin.go": []byte("package origin\n\nimport \"example.com/app/internal/hatch/util\"\n\nvar _ = util.X\n"),
		"internal/hatch/util/util.go":     []byte("package util\n\nimport \"example.com/app/internal/config\"\n\nvar X = config.Config{}\n"),
	}
	if err := ValidateDerivationPackages(modules, indirect, "example.com/app"); err == nil {
		t.Fatal("a derivation package reaching internal/config through one hop was accepted")
	}
}

// revisionFixture writes a one-module registry plus the lock that records what
// the project last consumed, so the gate has a previous published state to
// compare against.
func revisionFixture(t *testing.T, revision int, payload string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "registry", "modules", "system", "hatch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"schema":2,"module":{"id":"ggg/system/hatch","kind":"system","name":"hatch",
"revision":%d,"contract":1,"title":"Hatch","description":"Hatch system.","requires":[],
"dependencies":{"go":[],"tools":[],"containers":[]},
"files":[{"source":"internal/hatch/hatch.go","target":"internal/hatch/hatch.go","class":"go","sha256":%q,"rewrite_module":true,"contract":true}],
"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},"data":[],"removal_policy":"free"}}`,
		revision, payload)
	if err := os.WriteFile(filepath.Join(dir, "module.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{"schema":2,"engine_contract":2,"registry_commit":"` + testCommitA + `","order":["ggg/system/hatch"],
"modules":[{"id":"ggg/system/hatch","revision":1,"contract":1,"source_commit":"` + testCommitA + `","reason":"explicit","required_by":[],
"files":[{"path":"internal/hatch/hatch.go","source":"internal/hatch/hatch.go","base_sha256":"` + strings.Repeat("a", 64) + `","local_sha256":"` + strings.Repeat("a", 64) + `","state":"clean"}],
"migrations":[]}]}`
	if err := os.WriteFile(filepath.Join(root, LockFileName), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// revision is the module's version of record: it feeds indexSHA and `ggg info`,
// so changed payloads under an unchanged revision publish a lie. The convention
// held only as habit until this gate, and habit failed seventeen times in one
// range — including, on the gate's very first run, an eighteenth module nobody
// had noticed.
func TestValidateManifestRevisionsRefusesChangedPayloadsAtTheSameRevision(t *testing.T) {
	unchanged := strings.Repeat("a", 64)
	changed := strings.Repeat("b", 64)

	if err := ValidateManifestRevisions(revisionFixture(t, 1, unchanged)); err != nil {
		t.Fatalf("an untouched module was refused: %v", err)
	}
	err := ValidateManifestRevisions(revisionFixture(t, 1, changed))
	if err == nil {
		t.Fatal("changed payload digests at the locked revision were accepted")
	}
	if !strings.Contains(err.Error(), "ggg/system/hatch") || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("the refusal must name the module and the remedy: %v", err)
	}
	// The bump is the remedy, and it must be enough on its own: comparing a
	// manifest against its own previous bytes would demand one bump per edit.
	if err := ValidateManifestRevisions(revisionFixture(t, 2, changed)); err != nil {
		t.Fatalf("a bumped revision was still refused: %v", err)
	}

	// Scope: no lock beside the registry means no previous published state, so
	// there is nothing to compare and genesis must not be blocked.
	bare := revisionFixture(t, 1, changed)
	if err := os.Remove(filepath.Join(bare, LockFileName)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestRevisions(bare); err != nil {
		t.Fatalf("a registry with no lock was refused: %v", err)
	}
}

// releaseBaselineModule writes one module into a registry tree: the payload on
// disk and the manifest that declares its digest, the way `registry build`
// leaves them after a refresh. It returns the manifest bytes, which is what a
// release publishes and what the baseline is loaded from.
func releaseBaselineModule(t *testing.T, root, name string, revision int, payload []byte) []byte {
	t.Helper()
	source := "internal/" + name + "/" + name + ".go"
	if err := os.MkdirAll(filepath.Join(root, "internal", name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(source)), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "registry", "modules", "system", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(fmt.Sprintf(`{"schema":2,"module":{"id":"ggg/system/%s","kind":"system","name":"%s",
"revision":%d,"contract":1,"title":"Module","description":"One module.","requires":[],
"dependencies":{"go":[],"tools":[],"containers":[]},
"files":[{"source":%q,"target":%q,"class":"go","sha256":%q,"rewrite_module":true,"contract":true}],
"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},"data":[],"removal_policy":"free"}}`,
		name, name, revision, source, source, digestBytes(payload)))
	if err := os.WriteFile(filepath.Join(dir, "module.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// publishReleaseBaseline signs nothing and pins everything: the snapshot a
// release writes is an index of paths to digests, and the baseline loader
// authenticates each manifest against it.
func publishReleaseBaseline(t *testing.T, ref string, manifests map[string][]byte) ReleaseBaseline {
	t.Helper()
	snapshot := RegistrySnapshot{Schema: 1}
	for path, data := range manifests {
		snapshot.Files = append(snapshot.Files, SnapshotFile{Path: path, SHA256: digestBytes(data)})
	}
	slices.SortFunc(snapshot.Files, func(a, b SnapshotFile) int { return strings.Compare(a.Path, b.Path) })
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadReleaseBaseline(ref, data, func(path string) ([]byte, error) {
		published, ok := manifests[path]
		if !ok {
			return nil, fmt.Errorf("%s is not in the released tree", path)
		}
		return published, nil
	})
	if err != nil {
		t.Fatalf("load the %s baseline: %v", ref, err)
	}
	return baseline
}

// The hole the lock and snapshot halves cannot see: one edit that moves a
// payload AND refreshes the digest recorded for it leaves manifest and disk
// agreeing at the old revision. The released snapshot is the third point, and
// it is immutable — no working-tree command rewrites a tag.
func TestValidateRevisionsAgainstReleaseBaselineRefusesBytesThatMovedWithoutABump(t *testing.T) {
	const manifestPath = "registry/modules/system/hatch/module.json"

	// The release: one module at revision 3, payload and manifest agreeing.
	published := t.TempDir()
	baseline := publishReleaseBaseline(t, "v1.2.0", map[string][]byte{
		manifestPath: releaseBaselineModule(t, published, "hatch", 3, []byte("package hatch\n\nconst Version = 1\n")),
	})

	// Untouched since the release: passes, and needs no bump. This is the
	// false-positive half — most modules in any cycle are this one.
	if err := ValidateRevisionsAgainstReleaseBaseline(published, baseline); err != nil {
		t.Fatalf("a module untouched since the release was refused: %v", err)
	}

	// The incident shape: the payload moves and the manifest absorbs its new
	// digest in the same edit, at the same revision. Both existing gates see
	// two artifacts in agreement; this one sees the release.
	tree := t.TempDir()
	releaseBaselineModule(t, tree, "hatch", 3, []byte("package hatch\n\nconst Version = 2\n"))
	err := ValidateRevisionsAgainstReleaseBaseline(tree, baseline)
	if err == nil {
		t.Fatal("a module that republished new bytes under its published revision was accepted")
	}
	for _, want := range []string{"ggg/system/hatch", "published revision 3", "tree revision 3", "v1.2.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name the module, both revisions and the baseline; %q is missing from: %v", want, err)
		}
	}

	// The remedy, and its scope: one bump clears the module for the whole
	// release cycle, however many further edits it takes. Per release, not
	// per commit.
	bumped := t.TempDir()
	releaseBaselineModule(t, bumped, "hatch", 4, []byte("package hatch\n\nconst Version = 2\n"))
	if err := ValidateRevisionsAgainstReleaseBaseline(bumped, baseline); err != nil {
		t.Fatalf("a bumped module was refused: %v", err)
	}
	releaseBaselineModule(t, bumped, "hatch", 4, []byte("package hatch\n\nconst Version = 3\n"))
	if err := ValidateRevisionsAgainstReleaseBaseline(bumped, baseline); err != nil {
		t.Fatalf("a second edit at the same bumped revision was refused, so the gate demands a bump per commit: %v", err)
	}

	// A revision that moved BACKWARD is not a bump: the published revision is
	// a floor, not a difference.
	lowered := t.TempDir()
	releaseBaselineModule(t, lowered, "hatch", 2, []byte("package hatch\n\nconst Version = 2\n"))
	if err := ValidateRevisionsAgainstReleaseBaseline(lowered, baseline); err == nil {
		t.Fatal("a module whose revision moved backward while its bytes moved was accepted")
	}

	// A module absent from the release has nothing to compare: new modules
	// arrive at revision 1 and must not be refused for it.
	fresh := t.TempDir()
	releaseBaselineModule(t, fresh, "hatch", 3, []byte("package hatch\n\nconst Version = 1\n"))
	releaseBaselineModule(t, fresh, "newcomer", 1, []byte("package newcomer\n"))
	if err := ValidateRevisionsAgainstReleaseBaseline(fresh, baseline); err != nil {
		t.Fatalf("a module absent from the baseline was refused: %v", err)
	}

	// Published then deleted: passes. The refusal's remedy is an edit to a
	// manifest that no longer exists, and removal is the catalog's business,
	// not the version of record's.
	removed := t.TempDir()
	if err := ValidateRevisionsAgainstReleaseBaseline(removed, baseline); err != nil {
		t.Fatalf("a module deleted since the release was refused: %v", err)
	}
}

// The baseline is only worth comparing against if it is the release. A
// manifest whose bytes disagree with the digest the signed snapshot pins for
// it is a corrupted tree, and an empty index would pass everything while
// reporting a clean line.
func TestLoadReleaseBaselineRefusesATreeThatDisagreesWithItsSnapshot(t *testing.T) {
	const manifestPath = "registry/modules/system/hatch/module.json"
	published := t.TempDir()
	manifest := releaseBaselineModule(t, published, "hatch", 3, []byte("package hatch\n"))
	snapshot, err := json.Marshal(RegistrySnapshot{Schema: 1, Files: []SnapshotFile{
		{Path: manifestPath, SHA256: digestBytes(manifest)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadReleaseBaseline("v1.2.0", snapshot, func(string) ([]byte, error) {
		return []byte(`{"schema":2,"module":{"id":"ggg/system/hatch","revision":9}}`), nil
	})
	if err == nil {
		t.Fatal("a manifest whose bytes disagree with the signed snapshot was accepted as a baseline")
	}
	if !strings.Contains(err.Error(), manifestPath) {
		t.Fatalf("the refusal must name the file that disagrees: %v", err)
	}

	if _, err := LoadReleaseBaseline("v1.2.0", []byte(`{"files":[]}`), func(string) ([]byte, error) {
		return nil, errors.New("nothing published")
	}); err == nil {
		t.Fatal("a snapshot indexing no file was accepted as a baseline, so every comparison against it would pass vacuously")
	}

	if _, err := LoadReleaseBaseline("", []byte(`{"files":[{"path":"registry.json","sha256":"x"}]}`), func(string) ([]byte, error) {
		return nil, errors.New("nothing published")
	}); err == nil {
		t.Fatal("a baseline with no ref was accepted, so its refusals could not say what the tree changed since")
	}
}
