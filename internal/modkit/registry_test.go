package modkit

import (
	"encoding/json"
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

func assertSchemaDefinition(t *testing.T, definitions map[string]map[string]any, modelType reflect.Type) {
	t.Helper()
	definition, ok := definitions[modelType.Name()]
	if !ok {
		t.Fatalf("schema definition %s is missing", modelType.Name())
	}
	if got := definition["type"]; got != "object" {
		t.Fatalf("schema definition %s type = %v, want object", modelType.Name(), got)
	}
	if got := definition["additionalProperties"]; got != false {
		t.Fatalf("schema definition %s additionalProperties = %v, want false", modelType.Name(), got)
	}
	properties, ok := definition["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema definition %s properties is not an object", modelType.Name())
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
			t.Fatalf("schema definition %s property %s is not an object", modelType.Name(), parts[0])
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		switch fieldType.Kind() {
		case reflect.Bool:
			if got := property["type"]; got != "boolean" {
				t.Fatalf("schema definition %s property %s type = %v, want boolean", modelType.Name(), parts[0], got)
			}
		case reflect.Int, reflect.Int64:
			if got := property["type"]; got != "integer" {
				t.Fatalf("schema definition %s property %s type = %v, want integer", modelType.Name(), parts[0], got)
			}
		case reflect.String:
			if got := property["type"]; got != "string" {
				t.Fatalf("schema definition %s property %s type = %v, want string", modelType.Name(), parts[0], got)
			}
		case reflect.Slice:
			if got := property["type"]; got != "array" {
				t.Fatalf("schema definition %s property %s type = %v, want array", modelType.Name(), parts[0], got)
			}
			if _, hasItems := property["items"].(map[string]any); !hasItems {
				if _, hasPrefixItems := property["prefixItems"].([]any); !hasPrefixItems {
					t.Fatalf("schema definition %s property %s has neither items nor prefixItems", modelType.Name(), parts[0])
				}
			}
		case reflect.Struct:
			ref, ok := property["$ref"].(string)
			if !ok || !strings.HasSuffix(ref, "#/$defs/"+fieldType.Name()) {
				t.Fatalf("schema definition %s property %s ref = %v, want %s", modelType.Name(), parts[0], property["$ref"], fieldType.Name())
			}
			if _, ok := definitions[fieldType.Name()]; !ok {
				t.Fatalf("schema definition %s property %s references missing %s", modelType.Name(), parts[0], fieldType.Name())
			}
		}
	}
	gotProperties := make([]string, 0, len(properties))
	for name := range properties {
		gotProperties = append(gotProperties, name)
	}
	requiredValue, ok := definition["required"].([]any)
	if !ok {
		t.Fatalf("schema definition %s required is not an array", modelType.Name())
	}
	var gotRequired []string
	for _, raw := range requiredValue {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("schema definition %s required contains non-string %T", modelType.Name(), raw)
		}
		gotRequired = append(gotRequired, value)
	}
	slices.Sort(wantProperties)
	slices.Sort(wantRequired)
	slices.Sort(gotProperties)
	slices.Sort(gotRequired)
	if !slices.Equal(gotProperties, wantProperties) {
		t.Fatalf("schema definition %s properties = %v, want %v", modelType.Name(), gotProperties, wantProperties)
	}
	if !slices.Equal(gotRequired, wantRequired) {
		t.Fatalf("schema definition %s required = %v, want %v", modelType.Name(), gotRequired, wantRequired)
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
