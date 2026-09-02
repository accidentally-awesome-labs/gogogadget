package gggcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

type createPlan struct {
	registryRoot string
	files        map[string][]byte
}

type providerDefinition struct {
	Targets      []modkit.ServiceTarget          `json:"targets"`
	Environment  []modkit.EnvironmentVariable    `json:"environment"`
	Dependencies modkit.Dependencies             `json:"dependencies"`
	Files        []modkit.ManifestFile           `json:"files"`
	Start        bool                            `json:"start"`
	Stop         bool                            `json:"stop"`
	Health       bool                            `json:"health"`
	Provisioner  *modkit.ProvisionerContribution `json:"provisioner,omitempty"`
}

func (c *Controller) previewCreate(ctx context.Context, mutation CreateMutation) (Plan, error) {
	project, err := c.loadProject()
	if err != nil {
		return Plan{}, err
	}
	registry, ok := c.mutableProjectRegistry(project)
	if !ok {
		return Plan{}, refusalError(fmt.Errorf("project has no mutable directory registry"))
	}
	registryRoot := filepath.Join(c.rootDir(), filepath.FromSlash(registry.Path))
	files, manifest, err := c.buildCreateFiles(ctx, registry, registryRoot, mutation)
	if err != nil {
		return Plan{}, err
	}
	if manifest != nil {
		if err := modkit.ValidateManifest(*manifest); err != nil {
			return Plan{}, usageError(fmt.Sprintf("created manifest is invalid: %v", err))
		}
	}
	changes := make([]modkit.Change, 0, len(files))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		kind := modkit.ChangeCreate
		if _, statErr := os.Stat(filepath.Join(c.rootDir(), filepath.FromSlash(name))); statErr == nil {
			kind = modkit.ChangeUpdate
		}
		sum := sha256.Sum256(files[name])
		changes = append(changes, modkit.Change{Path: name, Module: registry.Namespace + "/" + mutation.Kind + "/" + mutation.Name, Kind: kind, Class: modkit.DestinationAuthored, SHA256: hex.EncodeToString(sum[:]), Content: files[name]})
	}
	local := &modkit.Plan{Root: c.rootDir(), Project: project, RegistryCommit: "local", Changes: changes}
	plan := c.planFor("create", local, true)
	plan.mutation = mutation
	plan.create = &createPlan{registryRoot: registryRoot, files: files}
	return plan, nil
}

func (c *Controller) mutableProjectRegistry(project modkit.Project) (modkit.ProjectRegistry, bool) {
	for _, registry := range project.Registries {
		if registry.Source == "directory" && registry.Namespace != "ggg" {
			return registry, true
		}
	}
	// A project-local registry is a self-contained directory: it carries its
	// own registry.json. The self-hosting root (registry.json beside the
	// project intent) is the published catalog, never a `ggg create` target.
	for _, registry := range project.Registries {
		if registry.Source != "directory" || registry.Path == "" || registry.Path == "." {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.rootDir(), filepath.FromSlash(registry.Path), "registry.json")); err == nil {
			return registry, true
		}
	}
	return modkit.ProjectRegistry{}, false
}

func (c *Controller) buildCreateFiles(ctx context.Context, registry modkit.ProjectRegistry, registryRoot string, mutation CreateMutation) (map[string][]byte, *modkit.Manifest, error) {
	if mutation.Name == "" {
		return nil, nil, usageError("create name is required")
	}
	kind, name := mutation.Kind, mutation.Name
	if kind == "module" {
		var ok bool
		kind, name, ok = strings.Cut(mutation.Name, "/")
		if !ok {
			return nil, nil, usageError("create module requires KIND/NAME")
		}
	}
	moduleKind := modkit.ModuleKind(kind)
	switch kind {
	case "resource", "job":
		moduleKind = modkit.ModuleWorkflow
	case "provider":
		moduleKind = modkit.ModuleSystem
	case "migration":
		return c.buildMigrationFiles(registry, registryRoot, mutation)
	case "element", "component", "page", "workflow", "system":
	default:
		return nil, nil, usageError("create kind must be module, resource, page, workflow, job, migration, component, or provider")
	}
	slug := kebab(name)
	if slug == "" {
		return nil, nil, usageError("create name must contain letters or digits")
	}
	// Migration payloads live outside the files list; they are written beside
	// the manifest after materialization.
	migrationBodies := map[string][]byte{}
	manifest := modkit.Manifest{
		ID: registry.Namespace + "/" + string(moduleKind) + "/" + slug, Kind: moduleKind, Name: slug,
		Revision: 1, Contract: 1, Title: titleWords(name), Description: "Project-local " + kind + " " + titleWords(name) + ".",
		Requires: []modkit.Requirement{}, Dependencies: emptyDependencies(), Files: []modkit.ManifestFile{},
		Migrations: []modkit.ManifestMigration{}, Environment: []modkit.EnvironmentVariable{}, Docs: []modkit.DocumentationRef{},
		Data: []modkit.DataDeclaration{}, RemovalPolicy: modkit.RemovalFree,
	}
	payloads := map[string][]byte{}
	packageName := goIdentifier(slug)
	switch kind {
	case "page":
		if mutation.Scope != "public" && mutation.Scope != "app" && mutation.Scope != "admin" && mutation.Scope != "dev" {
			return nil, nil, usageError("create page --scope must be public, app, admin, or dev")
		}
		target := "internal/web/templates/pages/" + snake(slug) + ".templ"
		payloads[target] = []byte("package pages\n\ntempl " + exported(name) + "() {\n\t<main data-testid=\"" + slug + "-page\"><h1>" + titleWords(name) + "</h1></main>\n}\n")
		manifest.Claims.Packages = []string{"internal/web/templates/pages"}
	case "component":
		if mutation.Family == "" {
			return nil, nil, usageError("create component requires --family")
		}
		target := "internal/web/templates/ui/" + snake(slug) + ".templ"
		payloads[target] = []byte("package ui\n\ntempl " + exported(name) + "() {\n\t<div class=\"" + slug + "\"></div>\n}\n")
		manifest.Claims.Packages = []string{"internal/web/templates/ui"}
	case "resource":
		resourceManifest, resourcePayloads, resourceMigrations, resourceErr :=
			buildResourceModule(registry, moduleKind, name, mutation, manifest)
		if resourceErr != nil {
			return nil, nil, resourceErr
		}
		manifest, payloads, migrationBodies = resourceManifest, resourcePayloads, resourceMigrations
	case "job":
		if mutation.MaxAttempts == 0 {
			mutation.MaxAttempts = 10
		}
		target := "internal/jobs/" + snake(slug) + ".go"
		payloads[target] = []byte("package jobs\n\nimport \"context\"\n\nfunc Handle" + exported(name) + "(context.Context, []byte) error { return nil }\n")
		manifest.Claims.Packages = []string{"internal/jobs"}
		manifest.Claims.Jobs = []string{slug}
		manifest.Runtime.Jobs = []modkit.JobContribution{{Kind: slug, Package: "internal/jobs", Handler: "Handle" + exported(name), Schedulable: mutation.Schedulable, MaxAttempts: mutation.MaxAttempts}}
	case "provider":
		providerManifest, providerPayloads, err := c.buildProviderManifest(ctx, registry, manifest, mutation)
		if err != nil {
			return nil, nil, err
		}
		manifest, payloads = providerManifest, providerPayloads
	default:
		target := "internal/" + packageName + "/module.go"
		payloads[target] = []byte("package " + packageName + "\n\n// Module is the project-local " + titleWords(name) + " capability.\ntype Module struct{}\n")
		manifest.Claims.Packages = []string{"internal/" + packageName}
	}
	// materializeManifest is what appends the file records, so the manifest it
	// returns — not the one handed to it — is the module the plan describes.
	files, materialized, err := materializeManifest(registry.Path, manifest, payloads)
	if err != nil {
		return nil, nil, runtimeError(err)
	}
	for source, body := range migrationBodies {
		files[filepath.ToSlash(filepath.Join(registry.Path, source))] = body
	}
	return files, &materialized, nil
}

func (c *Controller) buildProviderManifest(ctx context.Context, registry modkit.ProjectRegistry, manifest modkit.Manifest, mutation CreateMutation) (modkit.Manifest, map[string][]byte, error) {
	if mutation.Slot == "" || mutation.Package == "" || mutation.Constructor == "" || mutation.Definition == "" {
		return manifest, nil, usageError("create provider requires --slot, --package, --constructor, and --definition")
	}
	data, err := os.ReadFile(mutation.Definition)
	if err != nil {
		return manifest, nil, usageError(err.Error())
	}
	var definition providerDefinition
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return manifest, nil, usageError(fmt.Sprintf("provider definition: %v", err))
	}
	if definition.Dependencies.Go == nil {
		definition.Dependencies.Go = []modkit.GoDependency{}
	}
	if definition.Dependencies.Tools == nil {
		definition.Dependencies.Tools = []modkit.ToolArtifact{}
	}
	if definition.Dependencies.Containers == nil {
		definition.Dependencies.Containers = []modkit.ContainerDependency{}
	}
	if definition.Targets == nil || len(definition.Targets) == 0 {
		return manifest, nil, usageError("provider definition must declare targets")
	}
	catalog, _, _, err := c.readCatalog(ctx, false)
	if err != nil {
		return manifest, nil, err
	}
	var slot *modkit.ProviderSlotContribution
	for i := range catalog.Modules {
		for j := range catalog.Modules[i].Runtime.ProviderSlots {
			if catalog.Modules[i].Runtime.ProviderSlots[j].ID == mutation.Slot {
				candidate := catalog.Modules[i].Runtime.ProviderSlots[j]
				slot = &candidate
			}
		}
	}
	if slot == nil {
		return manifest, nil, usageError(fmt.Sprintf("unknown provider slot %q", mutation.Slot))
	}
	provides := make([]modkit.RuntimeProvide, 0, len(slot.Capabilities))
	for _, capability := range slot.Capabilities {
		provides = append(provides, modkit.RuntimeProvide{Field: exported(strings.TrimPrefix(capability.Capability, strings.Split(capability.Capability, ".")[0]+".")), Capability: capability.Capability, Type: capability.Type})
	}
	manifest.Dependencies = definition.Dependencies
	manifest.Environment = definition.Environment
	manifest.Files = definition.Files
	manifest.Runtime.System = &modkit.SystemContribution{Package: mutation.Package, Constructor: mutation.Constructor, Needs: []modkit.RuntimeNeed{}, Provides: provides, Start: definition.Start, Stop: definition.Stop, Health: definition.Health, Adapter: &modkit.AdapterContribution{Slot: mutation.Slot, Targets: definition.Targets}}
	if definition.Provisioner != nil {
		manifest.Runtime.Provisioners = []modkit.ProvisionerContribution{*definition.Provisioner}
		manifest.Claims.Provisioners = []string{definition.Provisioner.ID}
	}
	return manifest, map[string][]byte{}, nil
}

func (c *Controller) buildMigrationFiles(registry modkit.ProjectRegistry, registryRoot string, mutation CreateMutation) (map[string][]byte, *modkit.Manifest, error) {
	if mutation.Owner == "" || (mutation.Migration != "immutable" && mutation.Migration != "neutralize" && mutation.Migration != "purge") {
		return nil, nil, usageError("create migration requires --owner MODULE --kind immutable|neutralize|purge")
	}
	parts := strings.Split(mutation.Owner, "/")
	if len(parts) != 3 || parts[0] != registry.Namespace {
		return nil, nil, refusalError(fmt.Errorf("migration owner must be a module in the project-local registry"))
	}
	manifestRelative := filepath.ToSlash(filepath.Join("registry", "modules", parts[1], parts[2], "module.json"))
	manifestPath := filepath.Join(registryRoot, filepath.FromSlash(manifestRelative))
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, runtimeError(err)
	}
	var document struct {
		Schema int             `json:"schema"`
		Module modkit.Manifest `json:"module"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, nil, runtimeError(err)
	}
	slug := snake(mutation.Name)
	source := filepath.ToSlash(filepath.Join("registry", "modules", parts[1], parts[2], "payload", slug+".sql"))
	body := []byte("-- +goose Up\n-- " + mutation.Name + "\nSELECT 1;\n\n-- +goose Down\n-- forward-only migration\n")
	sum := sha256.Sum256(body)
	document.Module.Migrations = append(document.Module.Migrations, modkit.ManifestMigration{ID: registry.Namespace + "." + slug, Kind: modkit.MigrationKind(mutation.Migration), Source: source, SHA256: hex.EncodeToString(sum[:])})
	sort.Slice(document.Module.Migrations, func(i, j int) bool { return document.Module.Migrations[i].ID < document.Module.Migrations[j].ID })
	if err := modkit.ValidateManifest(document.Module); err != nil {
		return nil, nil, usageError(err.Error())
	}
	manifestData, _ := json.MarshalIndent(document, "", "  ")
	return map[string][]byte{
		filepath.ToSlash(filepath.Join(registry.Path, source)):           body,
		filepath.ToSlash(filepath.Join(registry.Path, manifestRelative)): append(manifestData, '\n'),
	}, &document.Module, nil
}

// materializeManifest turns the payload-by-target map into the registry files
// a module is made of: one payload per source path plus the manifest that
// declares them. It returns the completed manifest, because appending the file
// records is its job and the caller's copy would otherwise describe a module
// that owns nothing.
func materializeManifest(
	registryPath string, manifest modkit.Manifest, payloads map[string][]byte,
) (map[string][]byte, modkit.Manifest, error) {
	files := map[string][]byte{}
	for target, data := range payloads {
		source := filepath.ToSlash(filepath.Join("registry", "modules", string(manifest.Kind), manifest.Name, "payload", filepath.Base(target)+".txt"))
		sum := sha256.Sum256(data)
		class := modkit.FileClassGo
		switch {
		case strings.HasSuffix(target, "_test.go"):
			class = modkit.FileClassTest
		case strings.HasSuffix(target, ".templ"):
			class = modkit.FileClassTempl
		case strings.HasSuffix(target, ".sql") && strings.Contains(target, "/migrations/"):
			class = modkit.FileClassMigration
		case strings.HasSuffix(target, ".sql"):
			class = modkit.FileClassQuery
		}
		// A Go, templ or test payload carries this repository's import paths;
		// installing it into a derivative rewrites them to that project's
		// module. A query or migration payload has none.
		rewrite := class == modkit.FileClassGo || class == modkit.FileClassTempl || class == modkit.FileClassTest
		manifest.Files = append(manifest.Files, modkit.ManifestFile{Source: source, Target: target, Class: class, SHA256: hex.EncodeToString(sum[:]), RewriteModule: rewrite, Contract: true})
		files[filepath.ToSlash(filepath.Join(registryPath, source))] = data
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Target < manifest.Files[j].Target })
	if err := modkit.ValidateManifest(manifest); err != nil {
		return nil, modkit.Manifest{}, err
	}
	document := struct {
		Schema int             `json:"schema"`
		Module modkit.Manifest `json:"module"`
	}{Schema: 2, Module: manifest}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, modkit.Manifest{}, err
	}
	manifestPath := filepath.ToSlash(filepath.Join(registryPath, "registry", "modules", string(manifest.Kind), manifest.Name, "module.json"))
	files[manifestPath] = append(data, '\n')
	return files, manifest, nil
}

func (c *Controller) applyCreate(_ context.Context, plan Plan) (Result, error) {
	state := plan.create
	if state == nil {
		return Result{}, runtimeError(fmt.Errorf("create plan is incomplete"))
	}
	type previous struct {
		data    []byte
		mode    fs.FileMode
		existed bool
	}
	before := map[string]previous{}
	remember := func(name string) error {
		if _, ok := before[name]; ok {
			return nil
		}
		info, err := os.Stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			before[name] = previous{}
			return nil
		}
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		before[name] = previous{data: data, mode: info.Mode(), existed: true}
		return nil
	}
	for relative := range state.files {
		if err := remember(filepath.Join(c.rootDir(), filepath.FromSlash(relative))); err != nil {
			return Result{}, runtimeError(err)
		}
	}
	for _, index := range []string{"elements.json", "components.json", "pages.json", "workflows.json", "systems.json", "profiles.json"} {
		if err := remember(filepath.Join(state.registryRoot, "registry", index)); err != nil {
			return Result{}, runtimeError(err)
		}
	}
	rollback := func() {
		for name, old := range before {
			if old.existed {
				_ = os.MkdirAll(filepath.Dir(name), 0o755)
				_ = os.WriteFile(name, old.data, old.mode)
			} else {
				_ = os.Remove(name)
			}
		}
	}
	names := make([]string, 0, len(state.files))
	for name := range state.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, relative := range names {
		name := filepath.Join(c.rootDir(), filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			rollback()
			return Result{}, rollbackError(err)
		}
		if err := c.Write(name, state.files[relative], 0o644); err != nil {
			rollback()
			return Result{}, rollbackError(err)
		}
	}
	if _, err := modkit.RefreshManifestDigests(state.registryRoot); err != nil {
		rollback()
		return Result{}, rollbackError(err)
	}
	if _, _, err := modkit.BuildRegistryIndexes(state.registryRoot); err != nil {
		rollback()
		return Result{}, rollbackError(err)
	}
	env := normalizeEnvelope(modkit.Envelope{OK: true, Command: "create", Exit: exitOK})
	for _, relative := range names {
		env.Changes = append(env.Changes, modkit.Change{Path: relative, Kind: modkit.ChangeCreate, Class: modkit.DestinationAuthored})
	}
	return Result{Envelope: env}, nil
}

func emptyDependencies() modkit.Dependencies {
	return modkit.Dependencies{Go: []modkit.GoDependency{}, Tools: []modkit.ToolArtifact{}, Containers: []modkit.ContainerDependency{}}
}
func kebab(value string) string { return separated(value, '-') }
func snake(value string) string { return separated(value, '_') }
func separated(value string, separator byte) string {
	var b strings.Builder
	last := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && !last {
				b.WriteByte(separator)
			}
			b.WriteRune(unicode.ToLower(r))
			last = false
		} else if !last {
			b.WriteByte(separator)
			last = true
		}
	}
	return strings.Trim(b.String(), string(separator))
}
func goIdentifier(value string) string { return strings.ReplaceAll(snake(value), "-", "_") }
func exported(value string) string {
	words := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for _, word := range words {
		if word == "" {
			continue
		}
		runes := []rune(word)
		b.WriteRune(unicode.ToUpper(runes[0]))
		b.WriteString(string(runes[1:]))
	}
	if b.Len() == 0 {
		return "Generated"
	}
	return b.String()
}
func titleWords(value string) string {
	return strings.TrimSpace(strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' || r == '/' }), " "))
}
