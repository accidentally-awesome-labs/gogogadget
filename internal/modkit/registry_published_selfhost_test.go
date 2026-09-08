// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// publishedSchemaCompiler loads all five published contracts into one compiler,
// because a $ref across documents has to resolve the way a third-party
// validator's would rather than the way one document alone allows.
func publishedSchemaCompiler(t *testing.T) (*jsonschema.Compiler, string) {
	t.Helper()
	repo := os.DirFS("../..")
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
	return compiler, repoRoot
}

// compilePublishedSchema compiles one definition out of the published contracts.
// fragment is empty for a whole document or "#/$defs/Name" for one definition.
func compilePublishedSchema(t *testing.T, schemaPath, fragment string) *jsonschema.Schema {
	t.Helper()
	compiler, repoRoot := publishedSchemaCompiler(t)
	compiled, err := compiler.Compile(filepath.Join(repoRoot, schemaPath) + fragment)
	if err != nil {
		t.Fatalf("compile schema %s%s: %v", schemaPath, fragment, err)
	}
	return compiled
}

func TestPublishedSchemaInstancesValidate(t *testing.T) {
	repo := os.DirFS("../..")
	compiled := map[string]*jsonschema.Schema{}
	validate := func(schemaPath, instancePath string) {
		fragment := ""
		if strings.HasSuffix(schemaPath, "module.schema.json") {
			fragment = "#/$defs/ModuleDocument"
			if strings.HasPrefix(instancePath, "registry/profiles/") {
				fragment = "#/$defs/ProfileDocument"
			}
		}
		schema, ok := compiled[schemaPath+fragment]
		if !ok {
			schema = compilePublishedSchema(t, schemaPath, fragment)
			compiled[schemaPath+fragment] = schema
		}
		instanceData, err := fs.ReadFile(repo, instancePath)
		if err != nil {
			t.Fatalf("read instance %s: %v", instancePath, err)
		}
		var instance any
		if err := json.Unmarshal(instanceData, &instance); err != nil {
			t.Fatalf("decode instance %s: %v", instancePath, err)
		}
		if err := schema.Validate(instance); err != nil {
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
		reflect.TypeOf(CLIContribution{}), reflect.TypeOf(TargetInput{}),
		reflect.TypeOf(LocalService{}), reflect.TypeOf(LocalServicePort{}),
		reflect.TypeOf(LocalServiceEnv{}), reflect.TypeOf(LocalServiceVolume{}),
		reflect.TypeOf(LocalServiceHealth{}),
		reflect.TypeOf(ManifestMigration{}), reflect.TypeOf(EnvironmentVariable{}),
		reflect.TypeOf(EnvironmentDerivation{}),
		reflect.TypeOf(DocumentationRef{}), reflect.TypeOf(TestMetadata{}), reflect.TypeOf(DataDeclaration{}),
		reflect.TypeOf(Lock{}), reflect.TypeOf(LockedModule{}), reflect.TypeOf(LockedFile{}),
		reflect.TypeOf(LockedMigration{}), reflect.TypeOf(PendingUpdate{}), reflect.TypeOf(PendingConflict{}),
	}
	conditional := schemaConditionalRequirements(t, publishedModuleSchemaDocument(t, repo))
	walked := map[string]bool{}
	for _, modelType := range modelTypes {
		walked[modelType.Name()] = true
		assertSchemaDefinition(t, definitions, modelType, conditional, publishedConditionalRules)
	}
	// A recorded rule about a definition nothing above walks would be checked
	// by no one, which is how a typo'd key passes as a clean sweep.
	for key := range publishedConditionalRules {
		owner, _, _ := strings.Cut(key, ".")
		if !walked[owner] {
			t.Errorf("publishedConditionalRules names %s, but no model type above walks %s, "+
				"so its schema keyword is asserted by nothing", key, owner)
		}
	}
}

// publishedModuleInstances lists every module.json the repository publishes.
func publishedModuleInstances(t *testing.T, repo fs.FS) []string {
	t.Helper()
	var out []string
	if err := fs.WalkDir(repo, "registry/modules", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && path.Base(name) == "module.json" {
			out = append(out, name)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk module instances: %v", err)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("published registry has no module instances")
	}
	return out
}

// 1850f581 made runtime.ui[].signature required and EXACT in validateUI and
// left module.schema.json declaring it optional with a "^templ " prefix. The
// published extension contract then disagreed with the tool in both
// directions: a third-party manifest omitting signature validated clean and
// was refused by ggg, and one declaring `templ Badge(o BadgeOptions)` matched
// the published pattern and was refused by ggg. Nothing caught it, because
// TestPublishedSchemaInstancesValidate only validates instances AGAINST the
// schema — one-directional — and every core manifest satisfied both.
//
// This is the two-directional assertion, run through both engines over a real
// published manifest with only the signature mutated. The verdicts must agree
// on every shape but ONE: JSON Schema would need a backreference to say "the
// two captured names are equal", and every pattern in this document must also
// compile under Go's RE2 engine, which has none
// (TestPublishedSchemaPatternsArePortableAndStrict). That single residual is
// named here and in the schema's own $comment, so a SECOND divergence fails
// instead of hiding behind the first.
func TestPublishedSchemaAndValidatorAgreeOnRendererSignatures(t *testing.T) {
	const base = "registry/modules/component/badge/module.json"
	repo := os.DirFS("../..")
	data, err := fs.ReadFile(repo, base)
	if err != nil {
		t.Fatalf("read %s: %v", base, err)
	}
	schema := compilePublishedSchema(t, "registry/schema/module.schema.json", "#/$defs/ModuleDocument")

	// mutate rewrites the one runtime.ui record's signature, or drops the key.
	mutate := func(signature string, present bool) []byte {
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode %s: %v", base, err)
		}
		records, ok := document["module"].(map[string]any)["runtime"].(map[string]any)["ui"].([]any)
		if !ok || len(records) != 1 {
			t.Fatalf("%s no longer carries exactly one runtime.ui record", base)
		}
		record := records[0].(map[string]any)
		if present {
			record["signature"] = signature
		} else {
			delete(record, "signature")
		}
		out, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode mutated %s: %v", base, err)
		}
		return out
	}
	schemaRefuses := func(body []byte) bool { return schemaRefusesModuleDocument(t, schema, body) }
	validatorRefuses := func(body []byte) bool { return validatorRefusesModuleDocument(t, body, base) }

	// The valid case first. Without it every row below would pass even if the
	// mutation under test were harmless, because a broken base refuses too.
	if schemaRefuses(data) || validatorRefuses(data) {
		t.Fatalf("%s is refused unmutated: schema=%v validator=%v",
			base, schemaRefuses(data), validatorRefuses(data))
	}

	residuals := 0
	for _, tc := range []struct {
		name      string
		signature string
		present   bool
		refused   bool
		residual  bool
	}{
		{name: "the exact declared shape", signature: "templ Badge(o BadgeOpts)", present: true},
		{name: "no signature at all", refused: true},
		{name: "an empty signature", signature: "", present: true, refused: true},
		{name: "the bare templ prefix", signature: "templ ", present: true, refused: true},
		{name: "Options instead of Opts", signature: "templ Badge(o BadgeOptions)", present: true, refused: true},
		{name: "a trailing space", signature: "templ Badge(o BadgeOpts) ", present: true, refused: true},
		{name: "a parameter not named o", signature: "templ Badge(x BadgeOpts)", present: true, refused: true},
		{name: "a func rather than a templ", signature: "func Badge(o BadgeOpts)", present: true, refused: true},
		{name: "two options arguments", signature: "templ Badge(o BadgeOpts, x int)", present: true, refused: true},
		// The one accepted divergence: RE2 cannot require the two names to agree.
		{name: "the two names disagreeing", signature: "templ Badge(o CardOpts)", present: true, refused: true, residual: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := mutate(tc.signature, tc.present)
			if got := validatorRefuses(body); got != tc.refused {
				t.Fatalf("validator refuses = %v, want %v", got, tc.refused)
			}
			if tc.residual {
				if !schemaRefuses(body) {
					return
				}
				t.Fatalf("the published schema now refuses %q too, so the recorded residual "+
					"in UIContribution.signature's $comment is stale and must be deleted", tc.signature)
			}
			if got := schemaRefuses(body); got != tc.refused {
				t.Fatalf("published schema refuses = %v, want %v — the external extension "+
					"contract disagrees with the tool, which is C1 in the review of 1850f581", got, tc.refused)
			}
		})
		if tc.residual {
			residuals++
		}
	}
	if residuals != 1 {
		t.Fatalf("%d residual divergences are declared; exactly one is accepted and documented", residuals)
	}
}

// schemaRefusesModuleDocument and validatorRefusesModuleDocument are the two
// verdicts every published-contract agreement test compares. Both take the
// document BYTES rather than a decoded Manifest, because two of the
// validator's refusals — strict decoding and requireJSONValue's missing-key
// check — do not survive a round trip through the model.
func schemaRefusesModuleDocument(t *testing.T, schema *jsonschema.Schema, body []byte) bool {
	t.Helper()
	var instance any
	if err := json.Unmarshal(body, &instance); err != nil {
		t.Fatalf("decode module document: %v", err)
	}
	return schema.Validate(instance) != nil
}

func validatorRefusesModuleDocument(t *testing.T, body []byte, origin string) bool {
	t.Helper()
	var document ModuleDocument
	if err := decodeStrict(body, &document); err != nil {
		return true
	}
	if err := requireJSONValue(body, reflect.TypeOf(&document), origin); err != nil {
		return true
	}
	return validateManifest(document.Module, true) != nil
}

// The three mutators below are deliberately fussy about what is already there.
// A row that deletes an absent key or overwrites a key it meant to add would
// still produce a verdict, and the verdict would be about the base manifest
// rather than about the rule under test — which is exactly how a conditional
// requirement test rots into a tautology when a base manifest moves.
func schemaDropKeys(t *testing.T, node map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := node[key]; !ok {
			t.Fatalf("base record does not carry %q, so dropping it asserts nothing", key)
		}
		delete(node, key)
	}
}

func schemaAddKey(t *testing.T, node map[string]any, key string, value any) {
	t.Helper()
	if _, ok := node[key]; ok {
		t.Fatalf("base record already carries %q, so adding it asserts nothing", key)
	}
	node[key] = value
}

func schemaReplaceKey(t *testing.T, node map[string]any, key string, value any) {
	t.Helper()
	if _, ok := node[key]; !ok {
		t.Fatalf("base record does not carry %q, so replacing it asserts nothing", key)
	}
	node[key] = value
}

// schemaObjectAt walks named object keys, and schemaRecordAt indexes one
// record out of a named array. Both fail rather than return zero values: a
// base manifest that no longer carries the shape under test must fail loudly.
func schemaObjectAt(t *testing.T, node map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, key := range keys {
		child, ok := node[key].(map[string]any)
		if !ok {
			t.Fatalf("base document has no object at %q", key)
		}
		node = child
	}
	return node
}

func schemaRecordAt(t *testing.T, node map[string]any, key string, index int) map[string]any {
	t.Helper()
	records, ok := node[key].([]any)
	if !ok {
		t.Fatalf("base document has no array at %q", key)
	}
	if index >= len(records) {
		t.Fatalf("base document %q has %d records, wanted index %d", key, len(records), index)
	}
	record, ok := records[index].(map[string]any)
	if !ok {
		t.Fatalf("base document %q[%d] is not an object", key, index)
	}
	return record
}

// The conditional half of the published extension contract.
//
// TestPublishedSchemaAndValidatorAgreeOnRendererSignatures closed one
// UNCONDITIONAL divergence. The gate that landed with it then measured fifteen
// more of a different shape: requirements the validator applies only when a
// sibling field says so, which `required` alone cannot state, and which the
// published schema therefore stated not at all. A third party writing an
// adapter manifest got no warning from the contract and a refusal from `ggg`.
//
// Every row is a real published manifest with one mutation, run through both
// engines. Each rule appears twice at least: a shape the tool refuses, which
// the schema must now refuse too, and a shape where the DISCRIMINATOR is
// absent and both engines accept the very same missing field — without which a
// conditional keyword would be indistinguishable from an unconditional
// requirement bolted onto `required`.
func TestPublishedSchemaAndValidatorAgreeOnConditionalRequirements(t *testing.T) {
	const (
		chart    = "registry/modules/component/chart/module.json"
		postgres = "registry/modules/system/database-postgres/module.json"
		storage  = "registry/modules/system/storage-s3/module.json"
		home     = "registry/modules/page/home/module.json"
		mail     = "registry/modules/system/mail/module.json"
		cli      = "registry/modules/system/cli-ui/module.json"
		clerk    = "registry/modules/system/identity-clerk/module.json"
	)
	repo := os.DirFS("../..")
	schema := compilePublishedSchema(t, "registry/schema/module.schema.json", "#/$defs/ModuleDocument")

	read := func(t *testing.T, name string) []byte {
		t.Helper()
		data, err := fs.ReadFile(repo, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return data
	}
	// mutate applies one change to the manifest inside a real module document
	// and refuses to return bytes that are identical to the ones it read.
	mutate := func(t *testing.T, name string, apply func(*testing.T, map[string]any)) []byte {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(read(t, name), &document); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		before, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		apply(t, schemaObjectAt(t, document, "module"))
		after, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode mutated %s: %v", name, err)
		}
		if bytes.Equal(before, after) {
			t.Fatalf("the mutation left %s unchanged", name)
		}
		return after
	}

	// The valid case first, once per base. Without it every row below would
	// pass even if its mutation were harmless, because a base refused for its
	// own reasons is refused by both engines too.
	for _, name := range []string{chart, postgres, storage, home, mail, cli, clerk} {
		body := read(t, name)
		if schemaRefusesModuleDocument(t, schema, body) || validatorRefusesModuleDocument(t, body, name) {
			t.Fatalf("%s is refused unmutated: schema=%v validator=%v", name,
				schemaRefusesModuleDocument(t, schema, body), validatorRefusesModuleDocument(t, body, name))
		}
	}

	adapterTarget := func(t *testing.T, module map[string]any, index int) map[string]any {
		t.Helper()
		return schemaRecordAt(t, schemaObjectAt(t, module, "runtime", "system", "adapter"), "targets", index)
	}
	for _, tc := range []struct {
		rule    string
		name    string
		base    string
		apply   func(*testing.T, map[string]any)
		refused bool
	}{
		{
			rule: "AssetContribution.engine/integrity", name: "an engine asset with no integrity",
			base: chart, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "assets", 0), "integrity")
			},
		},
		{
			rule: "AssetContribution.engine/integrity", name: "an integrity value with no engine",
			base: chart, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "assets", 0), "engine")
			},
		},
		{
			rule: "AssetContribution.engine/integrity", name: "an ordinary asset with neither",
			base: chart,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "assets", 0), "engine", "integrity")
			},
		},
		{
			rule: "LocalServiceEnv.value", name: "a container env entry with neither value nor from_key",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, adapterTarget(t, m, 0), "local_service"), "environment", 0), "value")
			},
		},
		{
			rule: "LocalServiceEnv.value", name: "a container env entry with both",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaAddKey(t, schemaRecordAt(t, schemaObjectAt(t, adapterTarget(t, m, 0), "local_service"), "environment", 0), "from_key", "POSTGRES_PASSWORD")
			},
		},
		{
			rule: "LocalServiceEnv.value", name: "a container env entry with from_key instead",
			base: postgres,
			apply: func(t *testing.T, m map[string]any) {
				record := schemaRecordAt(t, schemaObjectAt(t, adapterTarget(t, m, 0), "local_service"), "environment", 0)
				schemaDropKeys(t, record, "value")
				schemaAddKey(t, record, "from_key", "POSTGRES_PASSWORD")
			},
		},
		{
			rule: "LocalServiceHealth.path", name: "an http probe with no path",
			base: storage, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, adapterTarget(t, m, 0), "local_service", "health"), "path")
			},
		},
		{
			rule: "LocalServiceHealth.path", name: "a tcp probe with no path",
			base: storage,
			apply: func(t *testing.T, m map[string]any) {
				health := schemaObjectAt(t, adapterTarget(t, m, 0), "local_service", "health")
				schemaReplaceKey(t, health, "kind", "tcp")
				schemaDropKeys(t, health, "path")
			},
		},
		{
			rule: "NavigationContribution.href/route_id", name: "a nav entry with neither target",
			base: home, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 2), "href")
			},
		},
		{
			rule: "NavigationContribution.href/route_id", name: "a nav entry with both targets",
			base: home, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaAddKey(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 2), "route_id", "home.show")
			},
		},
		{
			rule: "NavigationContribution.href/route_id", name: "a nav entry with a route id instead",
			base: home,
			apply: func(t *testing.T, m map[string]any) {
				record := schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 2)
				schemaDropKeys(t, record, "href")
				schemaAddKey(t, record, "route_id", "home.show")
			},
		},
		{
			rule: "NavigationContribution.group", name: "a footer entry with no group",
			base: home, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 0), "group")
			},
		},
		{
			rule: "NavigationContribution.group", name: "a non-footer entry with a group",
			base: home, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaAddKey(t, schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 2), "group", "footer.company")
			},
		},
		{
			rule: "NavigationContribution.group", name: "the same entry outside the footer with no group",
			base: home,
			apply: func(t *testing.T, m map[string]any) {
				record := schemaRecordAt(t, schemaObjectAt(t, m, "runtime"), "navigation", 0)
				schemaReplaceKey(t, record, "area", "public")
				schemaDropKeys(t, record, "group")
			},
		},
		{
			rule: "NamespaceClaims.jobs/RuntimeContributions.jobs", name: "a declared job kind with no claim",
			base: mail, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "jobs")
			},
		},
		{
			rule: "NamespaceClaims.jobs/RuntimeContributions.jobs", name: "a claimed job kind with no declaration",
			base: mail, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime"), "jobs")
			},
		},
		{
			rule: "NamespaceClaims.jobs/RuntimeContributions.jobs", name: "neither half",
			base: mail,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "jobs")
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime"), "jobs")
			},
		},
		{
			rule: "NamespaceClaims.cli", name: "a contributed command with no claim",
			base: cli, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "cli")
			},
		},
		{
			rule: "NamespaceClaims.cli", name: "no contributed command and no claim",
			base: cli,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "cli")
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime"), "cli")
			},
		},
		{
			rule: "NamespaceClaims.packages", name: "a derivation with no claimed package",
			base: clerk, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "packages")
			},
		},
		{
			rule: "NamespaceClaims.packages", name: "no derivation and no claimed package",
			base: clerk,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaRecordAt(t, m, "environment", 0), "derivation")
				schemaDropKeys(t, schemaObjectAt(t, m, "claims"), "packages")
			},
		},
		{
			rule: "RuntimeContributions.system/SystemContribution.adapter", name: "a target-narrowed env key with no adapter",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime", "system"), "adapter")
			},
		},
		{
			rule: "RuntimeContributions.system/SystemContribution.adapter", name: "a target-narrowed env key with no system contribution",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime"), "system")
			},
		},
		{
			rule: "RuntimeContributions.system/SystemContribution.adapter", name: "no adapter and no target-narrowed env key",
			base: postgres,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, schemaObjectAt(t, m, "runtime"), "system")
				schemaDropKeys(t, schemaRecordAt(t, m, "environment", 0), "targets")
				schemaDropKeys(t, schemaRecordAt(t, m, "environment", 1), "targets")
			},
		},
		{
			rule: "ServiceTarget.provisioner", name: "a provision target with no provisioner",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				schemaDropKeys(t, adapterTarget(t, m, 1), "provisioner")
			},
		},
		{
			rule: "ServiceTarget.provisioner", name: "a configure target with no provisioner",
			base: postgres, refused: true,
			apply: func(t *testing.T, m map[string]any) {
				target := adapterTarget(t, m, 1)
				schemaReplaceKey(t, target, "automation", "configure")
				schemaDropKeys(t, target, "provisioner")
			},
		},
		{
			rule: "ServiceTarget.provisioner", name: "a manual target with no provisioner",
			base: postgres,
			apply: func(t *testing.T, m map[string]any) {
				target := adapterTarget(t, m, 1)
				schemaReplaceKey(t, target, "automation", "manual")
				schemaDropKeys(t, target, "provisioner")
			},
		},
	} {
		t.Run(tc.rule+"/"+tc.name, func(t *testing.T) {
			body := mutate(t, tc.base, tc.apply)
			if got := validatorRefusesModuleDocument(t, body, tc.base); got != tc.refused {
				t.Fatalf("validator refuses = %v, want %v — the row no longer describes the "+
					"rule it names, so the agreement below would assert nothing", got, tc.refused)
			}
			if got := schemaRefusesModuleDocument(t, schema, body); got != tc.refused {
				t.Fatalf("published schema refuses = %v, want %v — the external extension "+
					"contract disagrees with the tool about %s", got, tc.refused, tc.rule)
			}
		})
	}

	// The recorded residual, asserted rather than described. The validator
	// requires an env declaration's `secret` flag to EQUAL the flag on the
	// adapter target input whose `env_key` names it, which is a join between
	// two sibling arrays on a matched value; JSON Schema 2020-12 has no keyword
	// for it, the same way RE2 has no backreference for the renderer signature.
	// Asserting the schema still ACCEPTS it keeps the record honest: a future
	// keyword that expressed the rule would fail here and the $comment would
	// have to go.
	residual := mutate(t, postgres, func(t *testing.T, m map[string]any) {
		schemaDropKeys(t, schemaRecordAt(t, m, "environment", 0), "secret")
	})
	if !validatorRefusesModuleDocument(t, residual, postgres) {
		t.Fatal("the validator now accepts an env declaration whose secret flag " +
			"disagrees with its adapter input, so the recorded residual is stale")
	}
	if schemaRefusesModuleDocument(t, schema, residual) {
		t.Fatal("the published schema now refuses a secret-flag mismatch too, so the " +
			"recorded residual in EnvironmentVariable.secret's $comment must be deleted")
	}
}

// manifestFieldSite is one settable manifest field, reached by walking a real
// decoded manifest rather than by naming paths: a new contribution type joins
// the sweep by existing, not by being added to a list.
type manifestFieldSite struct {
	owner     string
	tag       string
	path      string
	value     reflect.Value
	omitempty bool
}

func collectManifestFieldSites(prefix string, v reflect.Value, out *[]manifestFieldSite) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		collectManifestFieldSites(prefix, v.Elem(), out)
	case reflect.Struct:
		modelType := v.Type()
		for i := range modelType.NumField() {
			tag := strings.Split(modelType.Field(i).Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				continue
			}
			field := v.Field(i)
			here := prefix + "." + tag[0]
			*out = append(*out, manifestFieldSite{
				owner: modelType.Name(), tag: tag[0], path: here, value: field,
				omitempty: slices.Contains(tag[1:], "omitempty"),
			})
			collectManifestFieldSites(here, field, out)
		}
	case reflect.Slice:
		// A json.RawMessage is a byte slice carrying a fragment whose shape the
		// schema, not this walker, defines. Maps are left whole for the same
		// reason: the schema declares them free-form on purpose.
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range v.Len() {
			collectManifestFieldSites(fmt.Sprintf("%s[%d]", prefix, i), v.Index(i), out)
		}
	}
}

// fieldPathsRelated reports whether one path contains the other. An ancestor is
// excluded from the rescue search because zeroing a whole container removes the
// record under test, which rescues every field inside it and would make the
// discriminator below say "conditional" about everything.
func fieldPathsRelated(a, b string) bool {
	return a == b ||
		strings.HasPrefix(a, b+".") || strings.HasPrefix(a, b+"[") ||
		strings.HasPrefix(b, a+".") || strings.HasPrefix(b, a+"[")
}

// searchForRescue asks whether the validator's refusal of a zeroed field
// DEPENDS on another field. It zeroes the named field, then tries one
// single-field change at a time — to the zero value, to every value the
// published catalog uses for that field, and to a synthetic non-zero scalar for
// the values the catalog never exercises — and returns the first change that
// makes the manifest valid again.
//
// A rescue means the requirement is CONDITIONAL, and the published contract
// states those with `if`/`then`, `dependentRequired`, `oneOf` and `not` rather
// than with `required` — so a rescue is no longer an excuse for the schema to
// say nothing, only for it to say something other than "always required". No
// rescue means the field is required outright, and an `omitempty` tag on it is
// the defect.
func searchForRescue(
	t *testing.T, manifest *Manifest, observed map[string][]any, owner, tag string,
) (string, int) {
	t.Helper()
	var sites []manifestFieldSite
	collectManifestFieldSites("", reflect.ValueOf(manifest).Elem(), &sites)
	var target *manifestFieldSite
	for i := range sites {
		if sites[i].owner == owner && sites[i].tag == tag && !sites[i].value.IsZero() {
			target = &sites[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("manifest %s carries no non-zero %s.%s to zero", manifest.ID, owner, tag)
	}
	priorTarget := target.value.Interface()
	target.value.Set(reflect.Zero(target.value.Type()))
	defer target.value.Set(reflect.ValueOf(priorTarget))

	attempts := 0
	for i := range sites {
		other := &sites[i]
		if fieldPathsRelated(other.path, target.path) {
			continue
		}
		candidates := append([]any{reflect.Zero(other.value.Type()).Interface()},
			observed[other.owner+"."+other.tag]...)
		switch other.value.Kind() {
		case reflect.String:
			candidates = append(candidates, reflect.ValueOf("x").Convert(other.value.Type()).Interface())
		case reflect.Bool:
			candidates = append(candidates, reflect.ValueOf(true).Convert(other.value.Type()).Interface())
		case reflect.Int, reflect.Int64:
			candidates = append(candidates, reflect.ValueOf(1).Convert(other.value.Type()).Interface())
		}
		priorOther := other.value.Interface()
		for _, candidate := range candidates {
			value := reflect.ValueOf(candidate)
			if !value.IsValid() || value.Type() != other.value.Type() {
				continue
			}
			attempts++
			other.value.Set(value)
			rescued := validateManifest(*manifest, true) == nil
			other.value.Set(reflect.ValueOf(priorOther))
			if rescued {
				return other.path, attempts
			}
		}
	}
	return "", attempts
}

// The gate whose absence let C1 ship, upgraded from presence parity to
// CONDITIONAL parity.
//
// Presence parity is held from two sides — requireJSONValue refuses a missing
// key for every field without `omitempty`, and assertSchemaDefinition pins the
// schema's `required` list to exactly that same set — so the contract could
// diverge from the tool in two ways. The first is a wrong TAG: a field the
// validator refuses outright at its zero value, declared `omitempty`, is
// optional to the decoder and optional in the published schema while being
// required in fact. That is what runtime.ui[].signature was, and it is what
// the rescue search below still catches.
//
// The second is a CONDITIONAL requirement the schema states not at all. When
// this gate first landed it excused those: a rescue proved the rule could not
// live in `required`, and the measurement stopped there, recording fifteen
// real gaps in the published contract. The schema now expresses them with
// `if`/`then`, `dependentRequired`, `oneOf` and `not`, so a rescue must now be
// answered by a conditional keyword that names the same field. A field with a
// rescue and no conditional is a shape a third party's manifest passes and
// `ggg` refuses — and it fails here naming the field and its discriminator.
//
// Both halves stay derived from the published catalog rather than written down:
// the candidate set is every `omitempty` field whose zero value some real
// manifest is refused for, conditionality is decided by searchForRescue, and
// the schema's side is read out of the document by
// schemaConditionalRequirements. A new conditional rule in the validator needs
// no edit here — only a keyword in the schema, or an entry in
// publishedSchemaResiduals saying which keyword JSON Schema lacks.
//
// Measured over the 297 published manifests: 15 candidates, all 15 rescued, 14
// answered by a conditional keyword and one recorded residual.
func TestValidatorRequiredFieldsAreNotOptionalInTheContract(t *testing.T) {
	repo := os.DirFS("../..")
	instances := publishedModuleInstances(t, repo)

	load := func(name string) Manifest {
		data, err := fs.ReadFile(repo, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var document ModuleDocument
		if err := decodeStrict(data, &document); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return document.Module
	}

	observed := map[string][]any{}
	distinct := map[string]map[string]bool{}
	candidates := map[string]string{}
	sites := 0
	for _, name := range instances {
		manifest := load(name)
		if err := validateManifest(manifest, true); err != nil {
			t.Fatalf("%s does not validate unmutated: %v", name, err)
		}
		var fields []manifestFieldSite
		collectManifestFieldSites("", reflect.ValueOf(&manifest).Elem(), &fields)
		sites += len(fields)
		for _, field := range fields {
			key := field.owner + "." + field.tag
			// Eight distinct values per field is enough vocabulary for the
			// rescue search and keeps it linear in the catalog.
			if blob, err := json.Marshal(field.value.Interface()); err == nil {
				if distinct[key] == nil {
					distinct[key] = map[string]bool{}
				}
				if !distinct[key][string(blob)] && len(observed[key]) < 8 {
					distinct[key][string(blob)] = true
					observed[key] = append(observed[key], field.value.Interface())
				}
			}
			if !field.omitempty || field.value.IsZero() || !field.value.CanSet() {
				continue
			}
			if _, known := candidates[key]; known {
				continue
			}
			prior := field.value.Interface()
			field.value.Set(reflect.Zero(field.value.Type()))
			refused := validateManifest(manifest, true) != nil
			field.value.Set(reflect.ValueOf(prior))
			if refused {
				candidates[key] = name
			}
		}
	}
	if sites < 200 || len(candidates) < 8 {
		t.Fatalf("only %d field sites and %d candidates were derived from %d manifests; "+
			"the walk has collapsed, not the catalog", sites, len(candidates), len(instances))
	}

	// Positive control. The discriminator must be able to SAY unconditional, or
	// a search that rescues everything would report a clean sweep. name is
	// unconditionally required and correctly carries no omitempty.
	control := load("registry/modules/component/badge/module.json")
	if rescue, attempts := searchForRescue(t, &control, observed, "UIContribution", "name"); rescue != "" {
		t.Fatalf("UIContribution.name was rescued by %s after %d attempts; the rescue search "+
			"cannot distinguish a conditional requirement from an outright one", rescue, attempts)
	}

	document := publishedModuleSchemaDocument(t, repo)
	conditional := schemaConditionalRequirements(t, document)
	if len(conditional) < len(publishedConditionalRules) {
		t.Fatalf("only %d conditional requirements were read out of the published schema, "+
			"fewer than the %d recorded rules; the walk has collapsed, not the schema",
			len(conditional), len(publishedConditionalRules))
	}

	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	residualsReached := map[string]bool{}
	for _, key := range keys {
		owner, tag, _ := strings.Cut(key, ".")
		manifest := load(candidates[key])
		rescue, attempts := searchForRescue(t, &manifest, observed, owner, tag)
		if rescue == "" {
			t.Errorf("%s is refused at its zero value in %s and no single-field change anywhere "+
				"else rescues it (%d tried), so the validator requires it outright — but its tag "+
				"says omitempty, which makes it optional to requireJSONValue and absent from the "+
				"published schema's required list. Drop the omitempty.",
				key, candidates[key], attempts)
			continue
		}
		if _, recorded := publishedSchemaResiduals[key]; recorded {
			residualsReached[key] = true
			continue
		}
		rule, recorded := publishedConditionalRules[key]
		if !recorded {
			t.Errorf("%s is CONDITIONALLY required — the validator refuses its zero value in %s "+
				"and %s rescues it — and the inventory records nothing about it. State the rule "+
				"in registry/schema/module.schema.json with if/then, dependentRequired, oneOf or "+
				"not and add it to publishedConditionalRules; if JSON Schema cannot express it, "+
				"record it in publishedSchemaResiduals with the keyword it lacks rather than "+
				"approximating it.", key, candidates[key], rescue)
			continue
		}
		if !conditional[key] {
			t.Errorf("%s is CONDITIONALLY required — the validator refuses its zero value in %s "+
				"and %s rescues it — and the published schema states no conditional requirement "+
				"about it (recorded rule: %s), so a third party's manifest validates clean and "+
				"ggg refuses it.", key, candidates[key], rescue, rule)
		}
	}
	for key, reason := range publishedSchemaResiduals {
		if !residualsReached[key] {
			t.Errorf("%s is recorded as a residual (%s) but the sweep no longer reaches it as a "+
				"conditional requirement; the record is stale", key, reason)
		}
		if conditional[key] {
			t.Errorf("%s is recorded as inexpressible (%s) and the published schema now states "+
				"a conditional about it; delete the record and its $comment", key, reason)
		}
	}
	for key, rule := range publishedConditionalRules {
		if !conditional[key] {
			t.Errorf("the published schema no longer states the %s rule about %s; `required` "+
				"cannot carry it, because the field is optional whenever the condition is absent",
				rule, key)
		}
		if _, known := candidates[key]; !known {
			t.Errorf("%s is recorded as conditionally required but no published manifest is "+
				"refused for its zero value any more; the record is stale", key)
		}
	}
}

// publishedConditionalRules is the inventory of conditional requirements the
// Go validator enforces and the keyword registry/schema/module.schema.json
// states each with. It is a record, not the check: the check reads the schema
// (schemaConditionalRequirements) and the validator (searchForRescue) and
// compares them. The record exists so dropping a keyword from the published
// contract fails by name in two places — here and in assertSchemaDefinition —
// rather than only when someone re-measures the catalog.
var publishedConditionalRules = map[string]string{
	"AssetContribution.engine":        "dependentRequired co-presence",
	"AssetContribution.integrity":     "dependentRequired co-presence",
	"LocalServiceEnv.value":           "oneOf exclusive-or with from_key",
	"LocalServiceHealth.path":         "if kind is http/then required",
	"NavigationContribution.href":     "oneOf exclusive-or with route_id",
	"NavigationContribution.route_id": "oneOf exclusive-or with href",
	"NavigationContribution.group":    "if area is footer/then required/else not required",
	"NamespaceClaims.jobs":            "if runtime.jobs/then required",
	"RuntimeContributions.jobs":       "if claims.jobs/then required",
	"NamespaceClaims.cli":             "if runtime.cli/then required",
	"NamespaceClaims.packages":        "if any environment derivation/then required",
	"RuntimeContributions.system":     "if any target-narrowed environment record/then required",
	"SystemContribution.adapter":      "if any target-narrowed environment record/then required",
	"ServiceTarget.provisioner":       "if automation is provision or configure/then required",
}

// publishedSchemaResiduals are the requirements JSON Schema 2020-12 cannot
// state, with the keyword it would need. Both are value joins the tool
// performs and no applicator expresses; approximating either with a weaker
// keyword would be worse than silence, because a keyword reads as a guarantee.
//
// UIContribution.signature is the other recorded divergence and is not listed
// here: its pattern IS published, and only the equality of the two captured
// names is missing, which is a pattern-engine limit rather than a missing
// conditional. TestPublishedSchemaAndValidatorAgreeOnRendererSignatures owns
// that one.
var publishedSchemaResiduals = map[string]string{
	"EnvironmentVariable.secret": "the flag must EQUAL the secret flag of the adapter " +
		"target input whose env_key names this record's key — a join between two sibling " +
		"arrays on a matched value, which JSON Schema 2020-12 has no keyword for",
}

func publishedModuleSchemaDocument(t *testing.T, repo fs.FS) map[string]any {
	t.Helper()
	data, err := fs.ReadFile(repo, "registry/schema/module.schema.json")
	if err != nil {
		t.Fatalf("read module schema: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode module schema: %v", err)
	}
	return document
}

// schemaConditionalRequirements reads out of the published module schema the
// set of `Definition.field` pairs some conditional keyword constrains: a field
// named by a `required` list inside `if`/`then`/`else`/`not`/`oneOf`/`anyOf`/
// `allOf`/`contains`, or by either side of a `dependentRequired` entry.
//
// It is a walk rather than a list because the rules do not all live on the
// definition that owns the field: the two-halves rules span `claims` and
// `runtime`, which are sibling properties of Manifest. So the walk carries the
// definition each instance location belongs to and switches it when it steps
// through a property whose declared schema is a `$ref` — or an array of them,
// where `contains` and `items` then constrain one element.
func schemaConditionalRequirements(t *testing.T, document map[string]any) map[string]bool {
	t.Helper()
	defs, ok := document["$defs"].(map[string]any)
	if !ok {
		t.Fatal("published module schema has no $defs object")
	}
	definitionName := func(node map[string]any) string {
		ref, ok := node["$ref"].(string)
		if !ok {
			return ""
		}
		if _, name, found := strings.Cut(ref, "#/$defs/"); found {
			return name
		}
		return ""
	}
	// resolve names the definition a property of def carries, following either
	// a direct $ref or the $ref of an array's items.
	resolve := func(def, property string) string {
		owner, ok := defs[def].(map[string]any)
		if !ok {
			return ""
		}
		properties, ok := owner["properties"].(map[string]any)
		if !ok {
			return ""
		}
		declared, ok := properties[property].(map[string]any)
		if !ok {
			return ""
		}
		if name := definitionName(declared); name != "" {
			return name
		}
		if items, ok := declared["items"].(map[string]any); ok {
			return definitionName(items)
		}
		return ""
	}

	found := map[string]bool{}
	var walk func(node map[string]any, def string, inConditional bool)
	walkAny := func(value any, def string, inConditional bool) {
		switch value := value.(type) {
		case map[string]any:
			walk(value, def, inConditional)
		case []any:
			for _, child := range value {
				if child, ok := child.(map[string]any); ok {
					walk(child, def, inConditional)
				}
			}
		}
	}
	walk = func(node map[string]any, def string, inConditional bool) {
		if def == "" {
			return
		}
		if inConditional {
			if required, ok := node["required"].([]any); ok {
				for _, raw := range required {
					if name, ok := raw.(string); ok {
						found[def+"."+name] = true
					}
				}
			}
		}
		// dependentRequired is conditional wherever it appears, and both sides
		// of each entry are constrained: the key by the dependency it drags in,
		// the dependency by the key that requires it.
		if dependent, ok := node["dependentRequired"].(map[string]any); ok {
			for name, raw := range dependent {
				found[def+"."+name] = true
				if list, ok := raw.([]any); ok {
					for _, entry := range list {
						if dependency, ok := entry.(string); ok {
							found[def+"."+dependency] = true
						}
					}
				}
			}
		}
		for _, keyword := range []string{"if", "then", "else", "not", "oneOf", "anyOf", "allOf", "contains", "dependentSchemas"} {
			if child, ok := node[keyword]; ok {
				if keyword == "dependentSchemas" {
					if schemas, ok := child.(map[string]any); ok {
						for _, schema := range schemas {
							walkAny(schema, def, true)
						}
					}
					continue
				}
				walkAny(child, def, true)
			}
		}
		// items and prefixItems keep the definition: an array property already
		// resolved to its element definition above.
		for _, keyword := range []string{"items", "prefixItems"} {
			if child, ok := node[keyword]; ok {
				walkAny(child, def, inConditional)
			}
		}
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, child := range properties {
				walkAny(child, resolve(def, name), inConditional)
			}
		}
	}
	for name := range defs {
		if definition, ok := defs[name].(map[string]any); ok {
			walk(definition, name, false)
		}
	}
	return found
}
