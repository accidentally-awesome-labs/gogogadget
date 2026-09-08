// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
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
	schemaRefuses := func(body []byte) bool {
		var instance any
		if err := json.Unmarshal(body, &instance); err != nil {
			t.Fatalf("decode mutated instance: %v", err)
		}
		return schema.Validate(instance) != nil
	}
	validatorRefuses := func(body []byte) bool {
		var document ModuleDocument
		if err := decodeStrict(body, &document); err != nil {
			return true
		}
		if err := requireJSONValue(body, reflect.TypeOf(&document), base); err != nil {
			return true
		}
		return validateManifest(document.Module, true) != nil
	}

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
// A rescue means the requirement is CONDITIONAL: JSON Schema can only state it
// with `if`/`then`/`dependentRequired`/`oneOf`, and the published contract
// deliberately does not model those (assertSchemaDefinition pins `required` to
// exactly the non-omitempty field set). No rescue means the field is required
// outright, and an `omitempty` tag on it is the defect.
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

// The gate whose absence let C1 ship. Presence parity was already held from two
// sides — requireJSONValue refuses a missing key for every field without
// `omitempty`, and assertSchemaDefinition pins the schema's `required` list to
// exactly that same set — so the published contract can only diverge from the
// tool where the TAG is wrong: a field the validator refuses outright at its
// zero value, declared `omitempty`, is optional to the decoder and optional in
// the published schema while being required in fact. That is precisely what
// runtime.ui[].signature was.
//
// Both halves are derived from the published catalog, not written down: the
// candidate set is every `omitempty` field whose zero value some real manifest
// is refused for, and conditionality is decided by searchForRescue rather than
// by an exemption list, so a new conditional rule needs no edit here and a new
// unconditional one cannot be waved through.
//
// Measured over the 297 published manifests: 15 candidates, all 15 rescued,
// zero findings — and reverting signature's tag to `omitempty` puts it back on
// the list, which is the false-negative this test exists to remove.
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

	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
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
		}
	}
}
