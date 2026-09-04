// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The repository publishes its own catalog, so the catalog it ships must load
// and be internally consistent: every profile member has to name a real module,
// or `ggg sync` would resolve a profile to a missing dependency.
func TestPublishedRegistryIsConsistent(t *testing.T) {
	repo := os.DirFS("../..")
	catalog, err := LoadCatalog(repo)
	if err != nil {
		t.Fatalf("LoadCatalog(repository): %v", err)
	}
	if len(catalog.Modules) == 0 {
		t.Fatal("published catalog has no modules")
	}

	known := make(map[string]struct{}, len(catalog.Modules))
	for _, module := range catalog.Modules {
		known[module.ID] = struct{}{}
	}
	for _, module := range catalog.Modules {
		for _, dependency := range module.Requires {
			if _, ok := known[dependency.ID]; !ok {
				t.Fatalf("module %s requires %s, which the catalog does not publish", module.ID, dependency.ID)
			}
		}
	}
	for _, profile := range catalog.Profiles {
		if len(profile.Members) == 0 {
			t.Fatalf("profile %s has no members", profile.ID)
		}
		for _, member := range profile.Members {
			if _, ok := known[member]; !ok {
				t.Fatalf("profile %s names %s, which the catalog does not publish", profile.ID, member)
			}
		}
	}

	rootData, err := fs.ReadFile(repo, "registry.json")
	if err != nil {
		t.Fatalf("read registry.json: %v", err)
	}
	var root RegistryRoot
	if err := decodeStrict(rootData, &root); err != nil {
		t.Fatalf("decode registry.json: %v", err)
	}
	if !slices.Equal(root.Includes, publishedRegistryIncludes) {
		t.Fatalf("registry includes = %v, want %v", root.Includes, publishedRegistryIncludes)
	}

	for _, name := range []string{
		"registry/schema/registry.schema.json",
		"registry/schema/module.schema.json",
		"registry/schema/project.schema.json",
		"registry/schema/lock.schema.json",
		"registry/schema/snapshot.schema.json",
	} {
		data, err := fs.ReadFile(repo, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !json.Valid(data) {
			t.Fatalf("%s is not valid JSON", name)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if got, want := document["$schema"], "https://json-schema.org/draft/2020-12/schema"; got != want {
			t.Fatalf("%s $schema = %v, want %v", name, got, want)
		}
	}
}

func TestPublishedSchemaPatternsArePortableAndStrict(t *testing.T) {
	repo := os.DirFS("../..")
	data, err := fs.ReadFile(repo, "registry/schema/module.schema.json")
	if err != nil {
		t.Fatalf("read module schema: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode module schema: %v", err)
	}

	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if pattern, ok := value["pattern"].(string); ok {
				if _, err := regexp.Compile(pattern); err != nil {
					t.Errorf("schema pattern %q is not RE2 portable: %v", pattern, err)
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(document)

	defs := document["$defs"].(map[string]any)
	manifestFile := defs["ManifestFile"].(map[string]any)["properties"].(map[string]any)
	pathPattern := manifestFile["source"].(map[string]any)["pattern"].(string)
	pathRE, err := regexp.Compile(pathPattern)
	if err != nil {
		t.Fatalf("safe path pattern is not RE2 portable: %v", err)
	}
	for _, invalid := range []string{"../secret", "a/../secret", "a\n../../secret", "/absolute", `a\b`, "a//b"} {
		if pathRE.MatchString(invalid) {
			t.Errorf("safe path pattern accepts %q", invalid)
		}
	}
	for _, valid := range []string{".env", "..config", "dir/file.go", "dir/.hidden"} {
		if !pathRE.MatchString(valid) {
			t.Errorf("safe path pattern rejects %q", valid)
		}
	}

	profile := defs["Profile"].(map[string]any)["properties"].(map[string]any)
	nameRE, err := regexp.Compile(profile["name"].(map[string]any)["pattern"].(string))
	if err != nil {
		t.Fatalf("kebab pattern is not RE2 portable: %v", err)
	}
	if nameRE.MatchString("2fa") || !nameRE.MatchString("two-fa") {
		t.Errorf("kebab pattern does not match Go validator")
	}

	route := defs["RouteContribution"].(map[string]any)["properties"].(map[string]any)
	routeRE, err := regexp.Compile(route["pattern"].(map[string]any)["pattern"].(string))
	if err != nil {
		t.Fatalf("route pattern is not RE2 portable: %v", err)
	}
	if routeRE.MatchString("/a/../b") || routeRE.MatchString("/a//b") || !routeRE.MatchString("/a/b") {
		t.Errorf("route pattern does not match Go validator")
	}

	lockData, err := fs.ReadFile(repo, "registry/schema/lock.schema.json")
	if err != nil {
		t.Fatalf("read lock schema: %v", err)
	}
	var lockDocument map[string]any
	if err := json.Unmarshal(lockData, &lockDocument); err != nil {
		t.Fatalf("decode lock schema: %v", err)
	}
	walk(lockDocument)
	lockDefs := lockDocument["$defs"].(map[string]any)
	pendingConflict := lockDefs["PendingConflict"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"candidate_path", "diff_path"} {
		pattern := pendingConflict[field].(map[string]any)["pattern"].(string)
		fieldRE, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatalf("%s pattern is not RE2 portable: %v", field, err)
		}
		if !strings.HasPrefix(pattern, "^"+conflictArtifactPrefix) {
			t.Errorf("%s pattern does not enforce the %s prefix", field, conflictArtifactPrefix)
		}
		if fieldRE.MatchString("internal/modules/button.go") || !fieldRE.MatchString("tmp/ggg/conflicts/run1/element-button/abc-button.go.candidate") {
			t.Errorf("%s pattern does not match the Go validator prefix rule", field)
		}
	}
}

func TestPublishedSchemaInstancesValidate(t *testing.T) {
	repo := os.DirFS("../..")
	validate := func(schemaPath, instancePath string) {
		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			t.Fatalf("resolve repository root: %v", err)
		}
		compiler := jsonschema.NewCompiler()
		for _, name := range []string{"registry.schema.json", "module.schema.json", "project.schema.json", "lock.schema.json", "snapshot.schema.json"} {
			data, err := fs.ReadFile(repo, "registry/schema/"+name)
			if err != nil {
				t.Fatalf("read schema %s: %v", name, err)
			}
			var document any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatalf("decode schema %s: %v", name, err)
			}
			if err := compiler.AddResource(filepath.Join(repoRoot, "registry/schema", name), document); err != nil {
				t.Fatalf("add schema %s: %v", name, err)
			}
		}
		schemaFile := filepath.Join(repoRoot, schemaPath)
		compileURL := schemaFile
		if strings.HasSuffix(schemaPath, "module.schema.json") {
			compileURL += "#/$defs/ModuleDocument"
			if strings.HasPrefix(instancePath, "registry/profiles/") {
				compileURL = schemaFile + "#/$defs/ProfileDocument"
			}
		}
		compiled, err := compiler.Compile(compileURL)
		if err != nil {
			t.Fatalf("compile schema %s: %v", schemaPath, err)
		}
		instanceData, err := fs.ReadFile(repo, instancePath)
		if err != nil {
			t.Fatalf("read instance %s: %v", instancePath, err)
		}
		var instance any
		if err := json.Unmarshal(instanceData, &instance); err != nil {
			t.Fatalf("decode instance %s: %v", instancePath, err)
		}
		if err := compiled.Validate(instance); err != nil {
			t.Errorf("%s does not validate against %s: %v", instancePath, schemaPath, err)
		}
	}

	validate("registry/schema/registry.schema.json", "registry.json")
	for _, include := range publishedRegistryIncludes {
		validate("registry/schema/registry.schema.json", include)
	}
	validate("registry/schema/project.schema.json", "gogogadget.json")
	validate("registry/schema/lock.schema.json", "gogogadget.lock.json")
	validate("registry/schema/snapshot.schema.json", RegistrySnapshotPath)

	var instances []string
	if err := fs.WalkDir(repo, "registry/modules", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && path.Base(name) == "module.json" {
			instances = append(instances, name)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk module instances: %v", err)
	}
	if err := fs.WalkDir(repo, "registry/profiles", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".json") {
			instances = append(instances, name)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk profile instances: %v", err)
	}
	sort.Strings(instances)
	if len(instances) == 0 {
		t.Fatal("published registry has no module/profile instances")
	}
	for _, instance := range instances {
		validate("registry/schema/module.schema.json", instance)
	}
}

func TestPublishedSchemasMatchModels(t *testing.T) {
	repo := os.DirFS("../..")
	definitions := map[string]map[string]any{}
	for _, name := range []string{
		"registry/schema/registry.schema.json",
		"registry/schema/module.schema.json",
		"registry/schema/project.schema.json",
		"registry/schema/lock.schema.json",
	} {
		data, err := fs.ReadFile(repo, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		defs, ok := document["$defs"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no $defs object", name)
		}
		for definitionName, raw := range defs {
			definition, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s $defs.%s is not an object", name, definitionName)
			}
			if _, exists := definitions[definitionName]; exists {
				t.Fatalf("schema definition %s is duplicated across documents", definitionName)
			}
			definitions[definitionName] = definition
		}
	}

	modelTypes := []reflect.Type{
		reflect.TypeOf(RegistryRoot{}), reflect.TypeOf(CatalogIndex{}),
		reflect.TypeOf(ModuleDocument{}), reflect.TypeOf(ProfileDocument{}), reflect.TypeOf(Profile{}),
		reflect.TypeOf(Project{}), reflect.TypeOf(ProjectRegistry{}), reflect.TypeOf(PortOverrides{}),
		reflect.TypeOf(Manifest{}), reflect.TypeOf(ManifestFile{}), reflect.TypeOf(NamespaceClaims{}),
		reflect.TypeOf(RuntimeContributions{}), reflect.TypeOf(ProviderSlotContribution{}),
		reflect.TypeOf(CapabilityContribution{}), reflect.TypeOf(SystemContribution{}),
		reflect.TypeOf(AdapterContribution{}), reflect.TypeOf(ServiceTarget{}),
		reflect.TypeOf(RuntimeNeed{}), reflect.TypeOf(RuntimeProvide{}),
		reflect.TypeOf(RouteContribution{}), reflect.TypeOf(RoutePolicy{}),
		reflect.TypeOf(JobContribution{}), reflect.TypeOf(ContentTypeContribution{}),
		reflect.TypeOf(NavigationContribution{}), reflect.TypeOf(SlotContribution{}),
		reflect.TypeOf(UIContribution{}), reflect.TypeOf(AssetContribution{}),
		reflect.TypeOf(CLIContribution{}),
		reflect.TypeOf(ManifestMigration{}), reflect.TypeOf(EnvironmentVariable{}),
		reflect.TypeOf(EnvironmentDerivation{}),
		reflect.TypeOf(DocumentationRef{}), reflect.TypeOf(TestMetadata{}), reflect.TypeOf(DataDeclaration{}),
		reflect.TypeOf(Lock{}), reflect.TypeOf(LockedModule{}), reflect.TypeOf(LockedFile{}),
		reflect.TypeOf(LockedMigration{}), reflect.TypeOf(PendingUpdate{}), reflect.TypeOf(PendingConflict{}),
	}
	for _, modelType := range modelTypes {
		assertSchemaDefinition(t, definitions, modelType)
	}
}
