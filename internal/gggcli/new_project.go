package gggcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"golang.org/x/mod/module"
)

// coreRegistryPublicKey verifies the core catalog fetched from GitHub. It is
// compiled in on purpose: `ggg new` runs before a project exists, so there is
// no gogogadget.json to pin it in, and a key supplied by the same fetch it
// authenticates would authenticate nothing.
//
// Its private half is held by the release owner and never lives in the tree.
// `registry.snapshot.sig` IS a published artifact — the tree is consumed over
// GitHub by every `ggg new --registry github:…` — so it is committed and
// TestCommittedSnapshotVerifiesUnderThePinnedCoreKey keeps it in step with
// registry.snapshot.json. Changing this constant is a key rotation: publish
// registry-key-rotation.json rather than editing it in place once consumers
// exist.
const coreRegistryPublicKey = "bNmIybNneUcqwf1ZxSPHpWNEEm4DLbCcuNGvGwljEww="

// NewProjectAnswers is the non-secret, serializable answer contract consumed by
// --answers. Provider values are explicit adapter/target selections for every
// environment; credentials are deliberately impossible to represent here.
type NewProjectAnswers struct {
	Name       string                               `json:"Name"`
	Module     string                               `json:"Module"`
	Profile    string                               `json:"Profile"`
	Providers  map[string]modkit.ProviderSelections `json:"Providers"`
	Deployment string                               `json:"Deployment"`
	Registry   string                               `json:"Registry"`
	Ref        string                               `json:"Ref"`
}

type newProjectPlan struct {
	target      string
	projectData []byte
	baseFiles   map[string][]byte
	rootCreated bool
}

func parseProviderAnswer(value string) (string, string, modkit.ProviderSelection, error) {
	left, right, ok := strings.Cut(value, "=")
	if !ok {
		return "", "", modkit.ProviderSelection{}, fmt.Errorf("provider %q must be SLOT:ENV=ADAPTER@TARGET", value)
	}
	slot, environment, ok := strings.Cut(left, ":")
	if !ok || slot == "" || environment == "" {
		return "", "", modkit.ProviderSelection{}, fmt.Errorf("provider %q must be SLOT:ENV=ADAPTER@TARGET", value)
	}
	adapter, target, ok := strings.Cut(right, "@")
	if !ok || adapter == "" || target == "" {
		return "", "", modkit.ProviderSelection{}, fmt.Errorf("provider %q must be SLOT:ENV=ADAPTER@TARGET", value)
	}
	if environment != "development" && environment != "test" && environment != "production" {
		return "", "", modkit.ProviderSelection{}, fmt.Errorf("provider %q has invalid environment %q", value, environment)
	}
	return slot, environment, modkit.ProviderSelection{Adapter: adapter, Target: target}, nil
}

func parseNewRegistry(value, ref string) (modkit.ProjectRegistry, error) {
	kind, location, ok := strings.Cut(value, ":")
	if !ok || location == "" {
		return modkit.ProjectRegistry{}, fmt.Errorf("registry must be github:OWNER/REPO or directory:PATH")
	}
	switch kind {
	case "directory":
		return modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: filepath.ToSlash(location)}, nil
	case "github":
		if ref == "" {
			return modkit.ProjectRegistry{}, fmt.Errorf("GitHub registry ref is required for development builds")
		}
		return modkit.ProjectRegistry{Namespace: "ggg", Source: "github", Repository: location, Ref: ref, PublicKey: coreRegistryPublicKey}, nil
	default:
		return modkit.ProjectRegistry{}, fmt.Errorf("registry must be github:OWNER/REPO or directory:PATH")
	}
}

func (c *Controller) previewNew(ctx context.Context, mutation NewMutation) (Plan, error) {
	if mutation.Dir == "" || mutation.Name == "" || mutation.ModulePath == "" || mutation.Profile == "" || mutation.Registry == "" {
		return Plan{}, usageError("new requires directory, name, module, profile, and registry answers")
	}
	if err := module.CheckPath(mutation.ModulePath); err != nil {
		return Plan{}, usageError(fmt.Sprintf("module path: %v", err))
	}
	target := mutation.Dir
	if !filepath.IsAbs(target) {
		target = filepath.Join(c.rootDir(), target)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return Plan{}, runtimeError(err)
	}
	rootCreated := false
	if entries, readErr := os.ReadDir(target); readErr == nil {
		if len(entries) != 0 && !mutation.InPlace {
			return Plan{}, refusalError(fmt.Errorf("new target %s is not empty", target))
		}
		if mutation.InPlace {
			for _, reserved := range []string{"go.mod", modkit.ProjectFileName, modkit.LockFileName} {
				if _, statErr := os.Stat(filepath.Join(target, reserved)); statErr == nil {
					return Plan{}, refusalError(fmt.Errorf("init refuses existing %s", reserved))
				}
			}
		}
	} else if errors.Is(readErr, fs.ErrNotExist) {
		rootCreated = true
	} else {
		return Plan{}, runtimeError(readErr)
	}

	registry, err := parseNewRegistry(mutation.Registry, mutation.Ref)
	if err != nil {
		return Plan{}, usageError(err.Error())
	}
	if c.injected != nil {
		return Plan{}, refusalError(fmt.Errorf("new requires a concrete directory or GitHub registry source"))
	}
	resolver, snapshot, catalog, err := c.resolveNewCatalog(ctx, registry)
	if err != nil {
		return Plan{}, plannerFailure{refusalError(err)}
	}
	profile, ok := findProfile(catalog, mutation.Profile)
	if !ok {
		return Plan{}, usageError(fmt.Sprintf("unknown profile %q", mutation.Profile))
	}
	providers := cloneProviderAnswers(profile.ProviderDefaults)
	for slot, selections := range mutation.Providers {
		providers[slot] = selections
	}
	if err := validateNewProfileSelections(profile, providers, catalog.Modules); err != nil {
		return Plan{}, usageError(err.Error())
	}
	deployment := mutation.Deployment
	if deployment == "" {
		deployment = profile.DefaultDeployment
	}
	if deployment == "" {
		return Plan{}, usageError(fmt.Sprintf("profile %s requires an explicit deployment", profile.ID))
	}
	if err := validateNewDeployment(deployment, catalog.Modules); err != nil {
		return Plan{}, usageError(err.Error())
	}
	// A profile may publish several deploy scaffolds, but a project installs
	// exactly one. Every other deploy module in the member closure is excluded,
	// so the resolver sees one deployment module and the choice stays explicit
	// in gogogadget.json.
	byID := make(map[string]modkit.Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		byID[module.ID] = module
	}
	exclude := []string{}
	for _, member := range profile.Members {
		module, ok := byID[member]
		if ok && member != deployment && len(module.Runtime.Deploy) > 0 {
			exclude = append(exclude, member)
		}
	}
	sort.Strings(exclude)

	project := modkit.Project{
		Schema: 2, Registries: []modkit.ProjectRegistry{registry}, Modules: []string{profile.ID},
		Exclude: exclude, Providers: providers, Deployment: deployment,
	}
	temporary, err := os.MkdirTemp("", "ggg-new-preview-")
	if err != nil {
		return Plan{}, runtimeError(err)
	}
	defer os.RemoveAll(temporary)
	if err := os.WriteFile(filepath.Join(temporary, "go.mod"), []byte("module "+mutation.ModulePath+"\n\ngo 1.26.6\n"), 0o644); err != nil {
		return Plan{}, runtimeError(err)
	}
	projectData, err := modkit.MarshalProject(project)
	if err != nil {
		return Plan{}, runtimeError(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, modkit.ProjectFileName), projectData, 0o644); err != nil {
		return Plan{}, runtimeError(err)
	}
	engine := modkit.New(modkit.Options{Source: resolver, Generator: modkit.RegistryGenerator{}, ToolRunner: modkit.OSCommandRunner{}})
	local, err := engine.Plan(ctx, temporary, modkit.Operation{Kind: modkit.OpSync, Offline: registry.Source == "directory"})
	if err != nil {
		return Plan{}, plannerFailure{refusalError(err)}
	}
	local.Root = target

	localRegistry, err := modkit.ProjectLocalRegistry(target, projectSlug(mutation.Name))
	if err != nil {
		return Plan{}, usageError(err.Error())
	}
	baseFiles, err := localRegistryFiles(localRegistry.Namespace, mutation.ModulePath+"/registry")
	if err != nil {
		return Plan{}, runtimeError(err)
	}
	finalCore := registry
	if registry.Source == "directory" {
		finalCore.Path = "_registry-core"
		vendored, vendorErr := vendorResolvedRegistry(snapshot, catalog, profile, local.Lock, project)
		if vendorErr != nil {
			return Plan{}, runtimeError(vendorErr)
		}
		for name, data := range vendored {
			baseFiles[filepath.ToSlash(filepath.Join("_registry-core", name))] = data
		}
	}
	project.Registries = []modkit.ProjectRegistry{finalCore, localRegistry}
	projectData, err = modkit.MarshalProject(project)
	if err != nil {
		return Plan{}, runtimeError(err)
	}
	state := &newProjectPlan{target: target, projectData: projectData, baseFiles: baseFiles, rootCreated: rootCreated}
	plan := c.planFor("new", &local, registry.Source == "directory")
	plan.mutation = mutation
	plan.newProject = state
	return plan, nil
}

func (c *Controller) resolveNewCatalog(ctx context.Context, registry modkit.ProjectRegistry) (modkit.SourceResolver, modkit.Snapshot, modkit.Catalog, error) {
	var resolver modkit.SourceResolver
	if registry.Source == "directory" {
		resolver = modkit.DirectorySource{Root: c.rootDir()}
	} else {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, modkit.Snapshot{}, modkit.Catalog{}, err
		}
		resolver = modkit.GitHubSource{CacheDir: filepath.Join(cache, "ggg", "registry"), Token: os.Getenv("GITHUB_TOKEN")}
	}
	snapshot, err := resolver.Resolve(ctx, registry)
	if err != nil {
		return nil, modkit.Snapshot{}, modkit.Catalog{}, err
	}
	catalog, err := modkit.LoadCatalog(snapshot.FS)
	if err != nil {
		return nil, modkit.Snapshot{}, modkit.Catalog{}, err
	}
	return resolver, snapshot, catalog, nil
}

func findProfile(catalog modkit.Catalog, id string) (modkit.Profile, bool) {
	for _, profile := range catalog.Profiles {
		if profile.ID == id || profile.Name == id {
			return profile, true
		}
	}
	return modkit.Profile{}, false
}

func cloneProviderAnswers(source map[string]modkit.ProviderSelections) map[string]modkit.ProviderSelections {
	out := make(map[string]modkit.ProviderSelections, len(source))
	for slot, selections := range source {
		out[slot] = selections
	}
	return out
}

func validateNewProfileSelections(profile modkit.Profile, providers map[string]modkit.ProviderSelections, modules []modkit.Manifest) error {
	required := make(map[string]struct{}, len(profile.RequiredProviderSlots))
	for _, slot := range profile.RequiredProviderSlots {
		required[slot] = struct{}{}
		if _, ok := providers[slot]; !ok {
			return fmt.Errorf("profile %s is missing provider selections for %s", profile.ID, slot)
		}
	}
	for slot := range providers {
		if _, ok := required[slot]; !ok {
			return fmt.Errorf("profile %s has an extra provider selection for %s", profile.ID, slot)
		}
	}
	byID := make(map[string]modkit.Manifest, len(modules))
	for _, module := range modules {
		byID[module.ID] = module
	}
	for slot, selections := range providers {
		for _, entry := range []struct {
			environment string
			selection   modkit.ProviderSelection
		}{{"development", selections.Development}, {"test", selections.Test}, {"production", selections.Production}} {
			module, ok := byID[entry.selection.Adapter]
			if !ok || module.Runtime.System == nil || module.Runtime.System.Adapter == nil || module.Runtime.System.Adapter.Slot != slot {
				return fmt.Errorf("%s selection for %s uses adapter %q from the wrong slot", entry.environment, slot, entry.selection.Adapter)
			}
			validTarget := false
			for _, target := range module.Runtime.System.Adapter.Targets {
				if target.ID != entry.selection.Target {
					continue
				}
				for _, allowed := range target.Environments {
					if allowed == entry.environment {
						validTarget = true
					}
				}
				if entry.environment == "production" && target.Mode == "development" {
					return fmt.Errorf("development target %s@%s is not allowed in production", entry.selection.Adapter, target.ID)
				}
			}
			if !validTarget {
				return fmt.Errorf("target %s@%s is not allowed in %s", entry.selection.Adapter, entry.selection.Target, entry.environment)
			}
		}
	}
	return nil
}

func validateNewDeployment(id string, modules []modkit.Manifest) error {
	for _, module := range modules {
		if module.ID == id && len(module.Runtime.Deploy) == 1 {
			return nil
		}
	}
	return fmt.Errorf("deployment %q must name a system module with exactly one deploy target", id)
}

func projectSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func localRegistryFiles(namespace, canonical string) (map[string][]byte, error) {
	root := modkit.RegistryRoot{Schema: 2, Namespace: namespace, CanonicalModule: canonical, Includes: []string{
		"registry/elements.json", "registry/components.json", "registry/pages.json", "registry/workflows.json", "registry/systems.json", "registry/profiles.json",
	}}
	files := map[string][]byte{}
	put := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		files[filepath.ToSlash(filepath.Join("registry", name))] = append(data, '\n')
		return nil
	}
	if err := put("registry.json", root); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		name string
		kind modkit.CatalogKind
	}{
		{"registry/elements.json", modkit.CatalogElement}, {"registry/components.json", modkit.CatalogComponent},
		{"registry/pages.json", modkit.CatalogPage}, {"registry/workflows.json", modkit.CatalogWorkflow},
		{"registry/systems.json", modkit.CatalogSystem}, {"registry/profiles.json", modkit.CatalogProfile},
	} {
		if err := put(item.name, modkit.CatalogIndex{Schema: 2, Kind: item.kind, Items: []string{}}); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func vendorResolvedRegistry(snapshot modkit.Snapshot, catalog modkit.Catalog, profile modkit.Profile, lock modkit.Lock, project modkit.Project) (map[string][]byte, error) {
	// The vendored profile must describe the closure the project actually
	// installs: excluded modules (the unselected deploy scaffold) are removed
	// from members, and the default deployment is pinned to the explicit
	// selection so a vendored default can never name a module that is absent.
	installed := make(map[string]struct{}, len(lock.Modules))
	for _, locked := range lock.Modules {
		installed[locked.Manifest.ID] = struct{}{}
	}
	members := make([]string, 0, len(profile.Members))
	for _, member := range profile.Members {
		if _, keep := installed[member]; keep {
			members = append(members, member)
		}
	}
	profile.Members = members
	profile.DefaultDeployment = project.Deployment
	root := snapshot.Registry
	root.Includes = []string{"registry/elements.json", "registry/components.json", "registry/pages.json", "registry/workflows.json", "registry/systems.json", "registry/profiles.json"}
	files := map[string][]byte{}
	marshal := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		files[name] = append(data, '\n')
		return nil
	}
	if err := marshal("registry.json", root); err != nil {
		return nil, err
	}
	indexes := map[modkit.CatalogKind][]string{
		modkit.CatalogElement: {}, modkit.CatalogComponent: {}, modkit.CatalogPage: {},
		modkit.CatalogWorkflow: {}, modkit.CatalogSystem: {}, modkit.CatalogProfile: {"registry/profiles/" + profile.Name + ".json"},
	}
	for _, locked := range lock.Modules {
		manifestPath := "registry/modules/" + string(locked.Manifest.Kind) + "/" + locked.Manifest.Name + "/module.json"
		kind := modkit.CatalogKind(locked.Manifest.Kind)
		indexes[kind] = append(indexes[kind], manifestPath)
		if err := marshal(manifestPath, struct {
			Schema int             `json:"schema"`
			Module modkit.Manifest `json:"module"`
		}{Schema: 2, Module: locked.Manifest}); err != nil {
			return nil, err
		}
		for _, payload := range locked.Manifest.Files {
			if payload.Class == modkit.FileClassGenerated {
				continue
			}
			data, err := fs.ReadFile(snapshot.FS, payload.Source)
			if err != nil {
				return nil, fmt.Errorf("vendor %s: %w", payload.Source, err)
			}
			files[payload.Source] = data
		}
		// Migrations are declared beside files, not inside them; a vendored
		// registry without them cannot reinstall or migrate the module.
		for _, migration := range locked.Manifest.Migrations {
			data, err := fs.ReadFile(snapshot.FS, migration.Source)
			if err != nil {
				return nil, fmt.Errorf("vendor %s: %w", migration.Source, err)
			}
			files[migration.Source] = data
		}
	}
	// Excluded modules stay resolvable: `project exclude` names catalog ids,
	// and a later `ggg add` must find the module it is adding.
	byID := make(map[string]modkit.Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		byID[module.ID] = module
	}
	for _, id := range project.Exclude {
		module, ok := byID[id]
		if ok {
			if _, already := installed[module.ID]; already {
				continue
			}
			manifestPath := "registry/modules/" + string(module.Kind) + "/" + module.Name + "/module.json"
			if err := marshal(manifestPath, struct {
				Schema int             `json:"schema"`
				Module modkit.Manifest `json:"module"`
			}{Schema: 2, Module: module}); err != nil {
				return nil, err
			}
			indexes[modkit.CatalogKind(module.Kind)] = append(indexes[modkit.CatalogKind(module.Kind)], manifestPath)
			for _, payload := range module.Files {
				if payload.Class == modkit.FileClassGenerated {
					continue
				}
				data, err := fs.ReadFile(snapshot.FS, payload.Source)
				if err != nil {
					return nil, fmt.Errorf("vendor %s: %w", payload.Source, err)
				}
				files[payload.Source] = data
			}
			for _, migration := range module.Migrations {
				data, err := fs.ReadFile(snapshot.FS, migration.Source)
				if err != nil {
					return nil, fmt.Errorf("vendor %s: %w", migration.Source, err)
				}
				files[migration.Source] = data
			}
		}
	}
	if err := marshal("registry/profiles/"+profile.Name+".json", modkit.ProfileDocument{Schema: 2, Profile: profile}); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		path string
		kind modkit.CatalogKind
	}{
		{"registry/elements.json", modkit.CatalogElement}, {"registry/components.json", modkit.CatalogComponent},
		{"registry/pages.json", modkit.CatalogPage}, {"registry/workflows.json", modkit.CatalogWorkflow},
		{"registry/systems.json", modkit.CatalogSystem}, {"registry/profiles.json", modkit.CatalogProfile},
	} {
		sort.Strings(indexes[item.kind])
		if err := marshal(item.path, modkit.CatalogIndex{Schema: 2, Kind: item.kind, Items: indexes[item.kind]}); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// genesisRunner executes the genesis build-out. Subprocess output goes to
// stderr, never stdout: `ggg new --json` must emit one envelope on stdout and
// nothing else, and generator chatter is progress.
func (c *Controller) genesisRunner() TaskRunner {
	if c.taskRunner != nil {
		return c.taskRunner
	}
	return osTaskRunner{out: os.Stderr, err: os.Stderr}
}

// genesisBuildOut leaves a created project buildable. `ggg new` installs
// authored source, writes the lock, and renders every registry aggregate, but
// three inputs to compilation are tool outputs rather than authored files:
// templ's *_templ.go, the sqlc package internal/db/module.go imports, and
// static/app.css, which static/embed_registry_gen.go names in a compile-time
// //go:embed pattern. Without them nothing in the project compiles, including
// the bin/ggg that `ggg setup` has to build before it can run anything at all
// — which is why genesis cannot defer this to setup.
//
// Tailwind is a pinned standalone binary rather than a Go tool, so producing
// static/app.css means installing the declared tool artifacts through the same
// digest-verified path setup uses. That is the honest option: the file is a
// generated output whose only correct producer is the declared compiler, and a
// placeholder would ship broken CSS behind a compiling package. Genesis
// already fetches and signature-verifies a registry snapshot and runs
// `go mod download all`, so one digest-pinned artifact adds no new trust or
// connectivity assumption, and it leaves bin/tailwindcss exactly where
// `ggg generate` and `ggg dev` look for it.
//
// The order is setup's, minus the step already done: `go mod download all` ran
// inside dependency reconciliation, so install the tools, complete the require
// graph tolerantly (`tidy -e`, because the generated packages do not exist
// yet and a readonly `go tool` refuses an incomplete graph), generate, then
// complete the require graph for real.
func genesisBuildOut(ctx context.Context, root string, runner TaskRunner) error {
	if err := installDeclaredTools(ctx, root); err != nil {
		return err
	}
	// The created project's stack reads .ggg/env/<environment>.env, so genesis
	// leaves development and test carrying the declared development posture.
	// Production is never written.
	for _, environment := range []string{"development", "test"} {
		if err := ensureEnvironmentFile(root, environment); err != nil {
			return err
		}
	}
	lock, _, err := readProjectLock(root)
	if err != nil {
		return err
	}
	steps := append([][]string{{"go", "mod", "tidy", "-e"}}, generationSteps(lock)...)
	steps = append(steps, []string{"go", "mod", "tidy"})
	for _, argv := range steps {
		if err := runner.Run(ctx, root, argv); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
	}
	return nil
}

// verifyGenesisCompiles is the commit check of the genesis transaction. A
// success envelope over a tree that cannot compile is the defect this guards:
// `ggg setup` there cannot even build bin/ggg, so the operator is left with no
// command to run and no diagnostic. The scope is the whole project because the
// break is not local — a missing tool output surfaces wherever its importer
// happens to be, and the one that shipped reached cmd/ggg through
// internal/gggcli, internal/db, and internal/db/sqlc.
func verifyGenesisCompiles(ctx context.Context, root string, runner TaskRunner) error {
	if err := runner.Run(ctx, root, []string{"go", "build", "./..."}); err != nil {
		return fmt.Errorf("the created project does not compile (go build ./...): %w", err)
	}
	return nil
}

func (c *Controller) applyNew(ctx context.Context, plan Plan) (Result, error) {
	state := plan.newProject
	if state == nil || plan.Local == nil {
		return Result{}, runtimeError(fmt.Errorf("new plan is incomplete"))
	}
	createdRoot := false
	if state.rootCreated {
		if err := os.MkdirAll(state.target, 0o755); err != nil {
			return Result{}, runtimeError(err)
		}
		createdRoot = true
	}
	created := make([]string, 0, len(state.baseFiles)+2)
	cleanup := func() {
		if createdRoot {
			_ = os.RemoveAll(state.target)
			return
		}
		sort.Slice(created, func(i, j int) bool { return len(created[i]) > len(created[j]) })
		for _, name := range created {
			_ = os.Remove(name)
		}
	}
	write := func(relative string, data []byte, mode os.FileMode) error {
		name := filepath.Join(state.target, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if _, err := os.Lstat(name); err == nil {
			return fmt.Errorf("new refuses to overwrite %s", relative)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := c.Write(name, data, mode); err != nil {
			return err
		}
		created = append(created, name)
		return nil
	}
	if err := write("go.mod", []byte("module "+plan.Local.ModulePath+"\n\ngo 1.26.6\n"), 0o644); err != nil {
		cleanup()
		return Result{}, rollbackError(err)
	}
	if err := write(modkit.ProjectFileName, state.projectData, 0o644); err != nil {
		cleanup()
		return Result{}, rollbackError(err)
	}
	names := make([]string, 0, len(state.baseFiles))
	for name := range state.baseFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := write(name, state.baseFiles[name], 0o644); err != nil {
			cleanup()
			return Result{}, rollbackError(err)
		}
	}
	engine, err := c.engine(operationOffline(plan))
	if err != nil {
		cleanup()
		return Result{}, err
	}
	// The generated aggregates are not part of plan.Changes, so result.Written
	// never names them and an in-place rollback would leave them behind. Note
	// the ones that are absent now, before the apply creates them: a path an
	// operator already had at a generator-owned name is theirs, and the
	// engine's own journal is what restores it.
	newlyGenerated := make([]string, 0)
	for _, name := range (modkit.RegistryGenerator{}).GeneratedPaths(*plan.Local) {
		full := filepath.Join(state.target, filepath.FromSlash(name))
		if _, statErr := os.Lstat(full); errors.Is(statErr, fs.ErrNotExist) {
			newlyGenerated = append(newlyGenerated, full)
		}
	}
	result, err := engine.Apply(ctx, *plan.Local)
	if err != nil {
		cleanup()
		env := planEnvelope(*plan.Local, "new", exitRollback)
		env.OK = false
		return Result{Envelope: env}, rollbackError(err)
	}
	// The tree the apply installed is source only. Finish the transaction by
	// producing the tool outputs compilation needs and proving the result
	// builds; a failure here rolls the genesis back rather than reporting
	// success over a project no command can run.
	runner := c.genesisRunner()
	buildErr := genesisBuildOut(ctx, state.target, runner)
	if buildErr == nil {
		buildErr = verifyGenesisCompiles(ctx, state.target, runner)
	}
	if buildErr != nil {
		// A root this command created is removed outright, which is the whole
		// rollback. An in-place genesis cannot remove a directory it did not
		// create, so it removes what it wrote — the base files, every
		// journalled path, the lock, and the aggregates this run generated.
		// go.sum is none of those: the dependency reconciliation writes it
		// through the go tool, so the remedy names it along with the tool
		// outputs and bin/.
		if !createdRoot {
			created = append(created, filepath.Join(state.target, modkit.LockFileName))
			for _, name := range result.Written {
				created = append(created, filepath.Join(state.target, filepath.FromSlash(name)))
			}
			created = append(created, newlyGenerated...)
		}
		cleanup()
		env := planEnvelope(*plan.Local, "new", exitRollback)
		env.OK = false
		remedy := fmt.Errorf("%w; nothing was kept, fix the cause and re-run `ggg new`", buildErr)
		if !createdRoot {
			remedy = fmt.Errorf("%w; installed source and the lock were removed, but go.sum, the generated tool outputs and bin/ remain: delete them and re-run", buildErr)
		}
		return Result{Envelope: env}, rollbackError(remedy)
	}
	env := planEnvelope(*plan.Local, "new", exitOK)
	env.Generated = appendUnique(env.Generated, result.Written...)
	return Result{Envelope: env, Payload: map[string]any{"root": state.target}}, nil
}
