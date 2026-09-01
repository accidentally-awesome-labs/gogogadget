package modkit

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

func validateProject(p Project, canonical bool) error {
	if p.Schema != 2 {
		return fmt.Errorf("project schema must be 2; migrate schema 1 explicitly")
	}
	if p.Registries == nil {
		return fmt.Errorf("project registries array is required")
	}
	seenNamespaces := map[string]struct{}{}
	for i, registry := range p.Registries {
		if err := validateProjectRegistry(registry); err != nil {
			return fmt.Errorf("project registries[%d]: %w", i, err)
		}
		if _, exists := seenNamespaces[registry.Namespace]; exists {
			return fmt.Errorf("project registries contain duplicate namespace %q", registry.Namespace)
		}
		seenNamespaces[registry.Namespace] = struct{}{}
	}
	if p.Modules == nil {
		return fmt.Errorf("project modules array is required")
	}
	if p.Exclude == nil {
		return fmt.Errorf("project exclude array is required")
	}
	if p.Providers == nil {
		return fmt.Errorf("project providers object is required")
	}
	if p.Deployment != "" && strings.TrimSpace(p.Deployment) != p.Deployment {
		return fmt.Errorf("project deployment must be trimmed")
	}
	if err := validateStringSet("project modules", p.Modules, canonical, ValidateScopedProjectModuleID); err != nil {
		return err
	}
	if err := validateStringSet("project exclude", p.Exclude, canonical, ValidateScopedProjectModuleID); err != nil {
		return err
	}
	selected := make(map[string]struct{}, len(p.Modules))
	hasProfile := false
	for _, id := range p.Modules {
		selected[id] = struct{}{}
		_, kind, _, _ := splitScopedModuleID(id)
		hasProfile = hasProfile || kind == "profile"
	}
	for _, id := range p.Exclude {
		_, kind, _, _ := splitScopedModuleID(id)
		if kind == "profile" {
			return fmt.Errorf("project exclude cannot contain profile %q", id)
		}
		if _, ok := selected[id]; ok {
			return fmt.Errorf("project exclude %q overlaps modules", id)
		}
	}
	if len(p.Exclude) != 0 && !hasProfile {
		return fmt.Errorf("project exclude requires a selected profile")
	}
	for _, slot := range sortedKeys(p.Providers) {
		if err := validateProviderSelections(slot, p.Providers[slot]); err != nil {
			return err
		}
	}
	if p.Deployment != "" {
		if err := ValidateScopedProjectModuleID(p.Deployment); err != nil {
			return fmt.Errorf("project deployment: %w", err)
		}
	}
	return nil
}

func validateProjectRegistry(registry ProjectRegistry) error {
	if !validNamespace(registry.Namespace) {
		return fmt.Errorf("namespace %q is invalid", registry.Namespace)
	}
	switch registry.Source {
	case "github":
		if err := validateRepository(registry.Repository); err != nil {
			return fmt.Errorf("repository: %w", err)
		}
		if strings.TrimSpace(registry.Ref) == "" || registry.Ref != strings.TrimSpace(registry.Ref) {
			return fmt.Errorf("ref must be non-empty and trimmed")
		}
		if strings.TrimSpace(registry.PublicKey) == "" {
			return fmt.Errorf("public_key is required")
		}
		key, err := base64.StdEncoding.DecodeString(registry.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return fmt.Errorf("public_key must be base64 raw %d bytes", ed25519.PublicKeySize)
		}
		if registry.Path != "" {
			return fmt.Errorf("path is forbidden for github source")
		}
	case "directory":
		if strings.TrimSpace(registry.Path) == "" || registry.Path != strings.TrimSpace(registry.Path) ||
			strings.HasPrefix(registry.Path, "/") || strings.Contains(registry.Path, "..") {
			return fmt.Errorf("path must be project-contained")
		}
		if registry.Repository != "" || registry.Ref != "" || registry.PublicKey != "" {
			return fmt.Errorf("repository, ref, and public_key are forbidden for directory source")
		}
	default:
		return fmt.Errorf("source must be github or directory")
	}
	return nil
}
func validNamespace(value string) bool {
	if value == "" || !validKebab(value) {
		return false
	}
	return true
}

func splitScopedModuleID(id string) (namespace, kind, name string, ok bool) {
	if strings.Count(id, "/") != 2 {
		return "", "", "", false
	}
	parts := strings.Split(id, "/")
	if !validNamespace(parts[0]) || !validKebab(parts[1]) || !validKebab(parts[2]) {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func ValidateScopedProjectModuleID(id string) error {
	namespace, kind, _, ok := splitScopedModuleID(id)
	if !ok || !validNamespace(namespace) {
		return fmt.Errorf("module id %q is invalid", id)
	}
	if kind != "element" && kind != "component" && kind != "page" && kind != "workflow" && kind != "system" && kind != "profile" {
		return fmt.Errorf("module kind %q is invalid", kind)
	}
	return nil
}

func validateProviderSelections(slot string, choices ProviderSelections) error {
	if strings.TrimSpace(slot) == "" {
		return fmt.Errorf("provider slot id must be non-empty")
	}
	for env, choice := range map[string]ProviderSelection{
		"development": choices.Development, "test": choices.Test, "production": choices.Production,
	} {
		if strings.TrimSpace(choice.Adapter) == "" || strings.TrimSpace(choice.Target) == "" {
			return fmt.Errorf("provider %s selection for %s must name adapter and target", slot, env)
		}
	}
	return nil
}
func validateRequirements(owner string, reqs []Requirement, canonical bool) error {
	seen := map[string]struct{}{}
	last := ""
	for i, req := range reqs {
		if err := ValidateScopedProjectModuleID(req.ID); err != nil {
			return fmt.Errorf("manifest requires[%d] id: %w", i, err)
		}
		if strings.HasSuffix(req.ID, "/profile") {
			return fmt.Errorf("manifest requires[%d] cannot require a profile", i)
		}
		if req.ID == owner {
			return fmt.Errorf("manifest requires cannot contain its own id")
		}
		if req.Contract.Min <= 0 || req.Contract.Max <= 0 || req.Contract.Min > req.Contract.Max {
			return fmt.Errorf("manifest requires[%d] contract bounds are invalid", i)
		}
		if _, exists := seen[req.ID]; exists {
			return fmt.Errorf("manifest requires contain duplicate id %q", req.ID)
		}
		seen[req.ID] = struct{}{}
		if canonical && i > 0 && last > req.ID {
			return fmt.Errorf("manifest requires must be sorted by id")
		}
		last = req.ID
	}
	return nil
}

func validateDependencies(deps Dependencies) error {
	if deps.Go == nil || deps.Tools == nil || deps.Containers == nil {
		return fmt.Errorf("manifest dependencies go, tools, and containers arrays are required")
	}
	seenGo, seenTools, seenContainers := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, dep := range deps.Go {
		if !validPackagePath(dep.Module) || strings.TrimSpace(dep.Version) == "" {
			return fmt.Errorf("manifest dependency go entry is invalid")
		}
		if _, ok := seenGo[dep.Module]; ok {
			return fmt.Errorf("duplicate go dependency %q", dep.Module)
		}
		seenGo[dep.Module] = struct{}{}
	}
	for _, tool := range deps.Tools {
		if tool.OS == "" || tool.Arch == "" || tool.URL == "" || !strings.HasPrefix(tool.URL, "https://") ||
			!validSHA256(tool.SHA256) || (tool.Format != "raw" && tool.Format != "zip" && tool.Format != "tar.gz") ||
			!validSafeInstallPath(tool.InstallPath) || !validSafeArchivePath(tool.BinaryPath) {
			return fmt.Errorf("manifest dependency tool %q is invalid", tool.InstallPath)
		}
		if _, ok := seenTools[tool.InstallPath]; ok {
			return fmt.Errorf("duplicate tool dependency %q", tool.InstallPath)
		}
		seenTools[tool.InstallPath] = struct{}{}
	}
	for _, container := range deps.Containers {
		if strings.TrimSpace(container.Name) == "" || !strings.Contains(container.Image, "@sha256:") {
			return fmt.Errorf("manifest container dependency %q must use an immutable digest", container.Name)
		}
		if _, ok := seenContainers[container.Name]; ok {
			return fmt.Errorf("duplicate container dependency %q", container.Name)
		}
		seenContainers[container.Name] = struct{}{}
	}
	return nil
}

func validSafeInstallPath(value string) bool {
	return strings.HasPrefix(value, "bin/") && validateSafePath(value) == nil
}

func validSafeArchivePath(value string) bool {
	return value != "" && validateSafePath(value) == nil
}

func validateManifest(m Manifest, canonical bool) error {
	namespace, kind, name, ok := splitScopedModuleID(m.ID)
	if !ok || !validModuleKind(ModuleKind(kind)) || !validNamespace(namespace) {
		return fmt.Errorf("manifest id %q is not a valid scoped module id", m.ID)
	}
	if !validModuleKind(m.Kind) {
		return fmt.Errorf("manifest kind %q is invalid", m.Kind)
	}
	if m.Name == "" || !validKebab(m.Name) {
		return fmt.Errorf("manifest name %q is invalid", m.Name)
	}
	if name != m.Name || kind != string(m.Kind) {
		return fmt.Errorf("manifest identity does not match id, kind, and name")
	}
	if m.Revision <= 0 || m.Contract <= 0 {
		return fmt.Errorf("manifest revision and contract must be positive")
	}
	if strings.TrimSpace(m.Title) == "" || strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("manifest title and description must be non-empty")
	}
	if !validRemovalPolicy(m.RemovalPolicy) {
		return fmt.Errorf("manifest removal_policy %q is invalid", m.RemovalPolicy)
	}
	if m.Requires == nil {
		return fmt.Errorf("manifest requires array is required")
	}
	if err := validateRequirements(m.ID, m.Requires, canonical); err != nil {
		return err
	}
	if err := validateDependencies(m.Dependencies); err != nil {
		return err
	}
	if m.Files == nil {
		return fmt.Errorf("manifest files array is required")
	}
	if err := validateManifestFiles(m.Files, canonical); err != nil {
		return err
	}
	if m.Migrations == nil {
		return fmt.Errorf("manifest migrations array is required")
	}
	if err := validateManifestMigrations(m.Migrations, canonical); err != nil {
		return err
	}
	if err := validateEnvironment(m.Environment, canonical); err != nil {
		return err
	}
	if err := validateEnvironmentTargets(m.Environment, m.Runtime.System, m.ID); err != nil {
		return err
	}
	if m.Docs == nil {
		return fmt.Errorf("manifest docs array is required")
	}
	if err := validateDocs(m.Docs, canonical); err != nil {
		return err
	}
	if m.Data == nil {
		return fmt.Errorf("manifest data array is required")
	}
	if err := validateData(m.Data, canonical); err != nil {
		return err
	}
	if err := validateClaims(m.Claims, canonical); err != nil {
		return err
	}
	if err := validateRuntime(m.Runtime, canonical); err != nil {
		return err
	}
	for _, command := range m.Runtime.CLI {
		if !slices.Contains(m.Claims.CLI, command.Name) {
			return fmt.Errorf("manifest runtime cli %q requires a claims.cli entry", command.Name)
		}
	}
	if err := validateAdapterEnvironment(m.Environment, m.Runtime.System); err != nil {
		return err
	}
	if err := validateVendors(m.Vendors, canonical); err != nil {
		return err
	}
	if err := validateTests(m.Tests, canonical); err != nil {
		return err
	}
	return nil
}

func validateManifestFiles(files []ManifestFile, canonical bool) error {
	seen := make(map[string]struct{}, len(files))
	last := ""
	for i, file := range files {
		if err := validateSafePath(file.Source); err != nil {
			return fmt.Errorf("manifest files[%d] source path: %w", i, err)
		}
		if err := validateSafePath(file.Target); err != nil {
			return fmt.Errorf("manifest files[%d] target path: %w", i, err)
		}
		if !validFileClass(file.Class) {
			return fmt.Errorf("manifest files[%d] class %q is invalid", i, file.Class)
		}
		// A generated file carries no digest: the registry snapshot excludes
		// generated outputs on purpose, so there are no stable bytes to hash.
		// Everything distributed as source must be pinned.
		if file.Class != FileClassGenerated && !validSHA256(file.SHA256) {
			return fmt.Errorf("manifest files[%d] sha256 is invalid", i)
		}
		if file.Class == FileClassGenerated && file.SHA256 != "" && !validSHA256(file.SHA256) {
			return fmt.Errorf("manifest files[%d] sha256 is invalid", i)
		}
		if _, ok := seen[file.Target]; ok {
			return fmt.Errorf("manifest files contain duplicate target %q", file.Target)
		}
		seen[file.Target] = struct{}{}
		if canonical && i > 0 && last > file.Target {
			return fmt.Errorf("manifest files must be sorted by target")
		}
		last = file.Target
	}
	return nil
}

func validateManifestMigrations(migrations []ManifestMigration, canonical bool) error {
	seen := make(map[string]struct{}, len(migrations))
	last := ""
	for i, migration := range migrations {
		if strings.TrimSpace(migration.ID) == "" || migration.ID != strings.TrimSpace(migration.ID) {
			return fmt.Errorf("manifest migrations[%d] id is invalid", i)
		}
		if !validMigrationKind(migration.Kind) {
			return fmt.Errorf("manifest migrations[%d] kind is invalid", i)
		}
		if err := validateSafePath(migration.Source); err != nil {
			return fmt.Errorf("manifest migrations[%d] source path: %w", i, err)
		}
		if !validSHA256(migration.SHA256) {
			return fmt.Errorf("manifest migrations[%d] sha256 is invalid", i)
		}
		if _, ok := seen[migration.ID]; ok {
			return fmt.Errorf("manifest migrations contain duplicate id %q", migration.ID)
		}
		seen[migration.ID] = struct{}{}
		if canonical && i > 0 && last > migration.ID {
			return fmt.Errorf("manifest migrations must be sorted by id")
		}
		last = migration.ID
	}
	return nil
}

func validateClaims(claims NamespaceClaims, canonical bool) error {
	sets := []struct {
		name   string
		values []string
	}{
		{"packages", claims.Packages}, {"routes", claims.Routes}, {"jobs", claims.Jobs},
		{"environment", claims.Environment}, {"i18n", claims.I18n}, {"queries", claims.Queries},
		{"openapi", claims.OpenAPI}, {"content_types", claims.ContentTypes}, {"ui", claims.UI},
		{"assets", claims.Assets}, {"data", claims.Data}, {"provider_slots", claims.ProviderSlots},
		{"provisioners", claims.Provisioners}, {"database_ops", claims.DatabaseOps},
		{"cli", claims.CLI}, {"deploy", claims.Deploy},
	}
	for _, set := range sets {
		if err := validateOptionalStringSet("manifest claims "+set.name, set.values, canonical); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntime(runtime RuntimeContributions, canonical bool) error {
	if runtime.System != nil {
		s := runtime.System
		if !validPackagePath(s.Package) {
			return fmt.Errorf("manifest runtime system package is invalid")
		}
		if !validIdentifier(s.Constructor) {
			return fmt.Errorf("manifest runtime system constructor is invalid")
		}
		if s.Needs == nil {
			return fmt.Errorf("manifest runtime system needs array is required")
		}
		if s.Provides == nil {
			return fmt.Errorf("manifest runtime system provides array is required")
		}
		seenNeeds := make(map[string]struct{}, len(s.Needs))
		last := ""
		for i, need := range s.Needs {
			if !validIdentifier(need.Field) {
				return fmt.Errorf("manifest runtime system needs[%d] field is invalid", i)
			}
			if strings.TrimSpace(need.Capability) == "" {
				return fmt.Errorf("manifest runtime system needs[%d] capability is invalid", i)
			}
			if !validGoTypeRef(need.Type) {
				return fmt.Errorf("manifest runtime system needs[%d] type is invalid", i)
			}
			if _, ok := seenNeeds[need.Field]; ok {
				return fmt.Errorf("manifest runtime system needs contain duplicate field %q", need.Field)
			}
			seenNeeds[need.Field] = struct{}{}
			if canonical && i > 0 && last > need.Field {
				return fmt.Errorf("manifest runtime system needs must be sorted by field")
			}
			last = need.Field
		}
		seenProvides := make(map[string]struct{}, len(s.Provides))
		last = ""
		for i, provided := range s.Provides {
			if !validIdentifier(provided.Field) {
				return fmt.Errorf("manifest runtime system provides[%d] field is invalid", i)
			}
			if strings.TrimSpace(provided.Capability) == "" {
				return fmt.Errorf("manifest runtime system provides[%d] capability is invalid", i)
			}
			if !validGoTypeRef(provided.Type) {
				return fmt.Errorf("manifest runtime system provides[%d] type is invalid", i)
			}
			if _, ok := seenProvides[provided.Field]; ok {
				return fmt.Errorf("manifest runtime system provides contain duplicate field %q", provided.Field)
			}
			seenProvides[provided.Field] = struct{}{}
			if canonical && i > 0 && last > provided.Field {
				return fmt.Errorf("manifest runtime system provides must be sorted by field")
			}
			last = provided.Field
		}
	}

	if err := validateProviderSlots(runtime.ProviderSlots, canonical); err != nil {
		return err
	}
	if runtime.System != nil {
		if err := validateAdapter(runtime.System.Adapter); err != nil {
			return fmt.Errorf("runtime adapter: %w", err)
		}
	}
	for _, contribution := range runtime.Provisioners {
		if !validIdentifier(contribution.ID) || !validPackagePath(contribution.Package) || !validIdentifier(contribution.Constructor) {
			return fmt.Errorf("runtime provisioner %q is invalid", contribution.ID)
		}
	}
	for _, contribution := range runtime.DatabaseOps {
		if !validIdentifier(contribution.ID) || !validPackagePath(contribution.Package) || !validIdentifier(contribution.Constructor) {
			return fmt.Errorf("runtime database operator %q is invalid", contribution.ID)
		}
	}
	for _, contribution := range runtime.Deploy {
		if !validIdentifier(contribution.ID) || !validPackagePath(contribution.Package) || !validIdentifier(contribution.Constructor) {
			return fmt.Errorf("runtime deployer %q is invalid", contribution.ID)
		}
	}
	if err := validateCLIContributions(runtime.CLI, canonical); err != nil {
		return err
	}
	if err := validateRoutes(runtime.Routes, canonical); err != nil {
		return err
	}
	if err := validateJobs(runtime.Jobs, canonical); err != nil {
		return err
	}
	if err := validateContentTypes(runtime.ContentTypes, canonical); err != nil {
		return err
	}
	if err := validateNavigation(runtime.Navigation, canonical); err != nil {
		return err
	}
	if err := validateSlots(runtime.Slots, canonical); err != nil {
		return err
	}
	if err := validateUI(runtime.UI, canonical); err != nil {
		return err
	}
	if err := validateAssets(runtime.Assets, canonical); err != nil {
		return err
	}
	if err := validateScenarios(runtime.Scenarios, canonical); err != nil {
		return err
	}
	if err := validateVisual(runtime.Visual, canonical); err != nil {
		return err
	}
	return nil
}
func validateProviderSlots(slots []ProviderSlotContribution, canonical bool) error {
	seen := map[string]struct{}{}
	for i, slot := range slots {
		if !validScopedSlotID(slot.ID) {
			return fmt.Errorf("provider slot %d id %q is invalid", i, slot.ID)
		}
		if _, ok := seen[slot.ID]; ok {
			return fmt.Errorf("duplicate provider slot %q", slot.ID)
		}
		seen[slot.ID] = struct{}{}
		if slot.Capabilities == nil || len(slot.Capabilities) == 0 {
			return fmt.Errorf("provider slot %q capabilities are required", slot.ID)
		}
		capSeen := map[string]struct{}{}
		for _, capability := range slot.Capabilities {
			if strings.TrimSpace(capability.Capability) == "" || !validGoTypeRef(capability.Type) {
				return fmt.Errorf("provider slot %q capability is invalid", slot.ID)
			}
			if _, ok := capSeen[capability.Capability]; ok {
				return fmt.Errorf("provider slot %q duplicate capability %q", slot.ID, capability.Capability)
			}
			capSeen[capability.Capability] = struct{}{}
		}
	}
	if canonical && len(slots) > 1 {
		for i := 1; i < len(slots); i++ {
			if slots[i-1].ID > slots[i].ID {
				return fmt.Errorf("provider slots must be sorted by id")
			}
		}
	}
	return nil
}

// validateCLIContributions checks the declared project-local ggg commands.
// Names are Go identifiers claimed under claims.cli and unique across the
// manifest; the package and handler name the contributed command handler.
// Reservation of the built-in command names is enforced when gggcli assembles
// the command registry, because that table — not the schema — owns them.
func validateCLIContributions(commands []CLIContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(commands))
	last := ""
	for i, command := range commands {
		if !validIdentifier(command.Name) {
			return fmt.Errorf("manifest runtime cli[%d] name is invalid", i)
		}
		if strings.TrimSpace(command.Summary) == "" {
			return fmt.Errorf("manifest runtime cli[%d] summary is required", i)
		}
		if !validPackagePath(command.Package) {
			return fmt.Errorf("manifest runtime cli[%d] package is invalid", i)
		}
		if !validIdentifier(command.Handler) {
			return fmt.Errorf("manifest runtime cli[%d] handler is invalid", i)
		}
		if _, ok := seen[command.Name]; ok {
			return fmt.Errorf("manifest runtime cli contain duplicate name %q", command.Name)
		}
		seen[command.Name] = struct{}{}
		if canonical && i > 0 && last > command.Name {
			return fmt.Errorf("manifest runtime cli must be sorted by name")
		}
		last = command.Name
	}
	return nil
}

func validScopedSlotID(id string) bool {
	parts := strings.Split(id, "/")
	return len(parts) == 2 && validNamespace(parts[0]) && validKebab(parts[1])
}

func validateAdapter(adapter *AdapterContribution) error {
	if adapter == nil {
		return nil
	}
	if !validScopedSlotID(adapter.Slot) || adapter.Targets == nil || len(adapter.Targets) == 0 {
		return fmt.Errorf("adapter slot and targets are required")
	}
	seen := map[string]struct{}{}
	for i, target := range adapter.Targets {
		if err := validateServiceTarget(target); err != nil {
			return fmt.Errorf("adapter target %d: %w", i, err)
		}
		if _, ok := seen[target.ID]; ok {
			return fmt.Errorf("duplicate adapter target %q", target.ID)
		}
		seen[target.ID] = struct{}{}
	}
	return nil
}

func validateServiceTarget(target ServiceTarget) error {
	if !validKebab(target.ID) || strings.TrimSpace(target.Title) == "" || target.DocsURL == "" {
		return fmt.Errorf("target id, title, and docs_url are required")
	}
	if target.Mode != "development" && target.Mode != "self-hosted" && target.Mode != "managed" {
		return fmt.Errorf("target mode is invalid")
	}
	if target.Automation != "provision" && target.Automation != "configure" && target.Automation != "manual" {
		return fmt.Errorf("target automation is invalid")
	}
	if len(target.Environments) == 0 {
		return fmt.Errorf("target environments are required")
	}
	envSeen := map[string]struct{}{}
	for _, env := range target.Environments {
		if env != "development" && env != "test" && env != "production" {
			return fmt.Errorf("target environment %q is invalid", env)
		}
		if _, ok := envSeen[env]; ok {
			return fmt.Errorf("target environments contain duplicate %q", env)
		}
		envSeen[env] = struct{}{}
	}
	if target.Inputs == nil {
		return fmt.Errorf("target inputs array is required")
	}
	inputSeen := map[string]struct{}{}
	for _, input := range target.Inputs {
		if strings.TrimSpace(input.Key) == "" || strings.TrimSpace(input.Label) == "" {
			return fmt.Errorf("target input key and label are required")
		}
		switch input.Type {
		case "string", "url", "integer", "boolean", "enum":
		default:
			return fmt.Errorf("target input %q type is invalid", input.Key)
		}
		if input.Type == "enum" && len(input.Enum) == 0 {
			return fmt.Errorf("target input %q enum is required", input.Key)
		}
		if _, ok := inputSeen[input.Key]; ok {
			return fmt.Errorf("target inputs contain duplicate %q", input.Key)
		}
		inputSeen[input.Key] = struct{}{}
	}
	if target.LocalService != nil {
		if err := validateLocalService(*target.LocalService); err != nil {
			return err
		}
	}
	if target.Automation == "provision" || target.Automation == "configure" {
		if target.Provisioner == "" {
			return fmt.Errorf("target provisioner is required for %s automation", target.Automation)
		}
	}
	return nil
}

func validateLocalService(service LocalService) error {
	if strings.TrimSpace(service.Container) == "" || service.Ports == nil || service.Environment == nil || service.Volumes == nil {
		return fmt.Errorf("local service container, ports, environment, and volumes are required")
	}
	if service.Health.Kind != "tcp" && service.Health.Kind != "http" {
		return fmt.Errorf("local service health kind is invalid")
	}
	if service.Health.Port <= 0 || (service.Health.Kind == "http" && service.Health.Path == "") {
		return fmt.Errorf("local service health is invalid")
	}
	for _, env := range service.Environment {
		if (env.Value == "") == (env.FromKey == "") {
			return fmt.Errorf("local service env %q must set exactly one value or from_key", env.Key)
		}
	}
	return nil
}

func validateRoutes(routes []RouteContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(routes))
	last := ""
	for i, route := range routes {
		if strings.TrimSpace(route.ID) == "" {
			return fmt.Errorf("manifest runtime routes[%d] id is empty", i)
		}
		if !validHTTPMethod(route.Method) {
			return fmt.Errorf("manifest runtime routes[%d] method is invalid", i)
		}
		if err := validateRoutePath(route.Pattern); err != nil {
			return fmt.Errorf("manifest runtime routes[%d] pattern: %w", i, err)
		}
		if !validRouteScope(route.Scope) {
			return fmt.Errorf("manifest runtime routes[%d] scope is invalid", i)
		}
		if !validPackagePath(route.Package) {
			return fmt.Errorf("manifest runtime routes[%d] package is invalid", i)
		}
		if !validIdentifier(route.Handler) {
			return fmt.Errorf("manifest runtime routes[%d] handler is invalid", i)
		}
		if route.Enabled != "" && !validIdentifier(route.Enabled) {
			return fmt.Errorf("manifest runtime routes[%d] enabled is invalid", i)
		}
		if route.Policy.CSRFExempt && strings.TrimSpace(route.Policy.CSRFReason) == "" {
			return fmt.Errorf("manifest runtime routes[%d] policy csrf_reason is required for exemption", i)
		}
		if !route.Policy.CSRFExempt && route.Policy.CSRFReason != "" {
			return fmt.Errorf("manifest runtime routes[%d] policy csrf_reason requires csrf_exempt", i)
		}
		// The declared cap only ever narrows the global 10 MB reader, so a value
		// at or above it is a no-op that reads like a limit. Refuse it rather than
		// let a manifest claim a bound the runtime will not apply.
		if route.Policy.MaxBodyBytes < 0 {
			return fmt.Errorf("manifest runtime routes[%d] policy max_body_bytes is negative", i)
		}
		if route.Policy.MaxBodyBytes >= GlobalRequestBodyLimit {
			return fmt.Errorf(
				"manifest runtime routes[%d] policy max_body_bytes %d does not narrow the global %d-byte cap",
				i, route.Policy.MaxBodyBytes, GlobalRequestBodyLimit)
		}
		if _, ok := seen[route.ID]; ok {
			return fmt.Errorf("manifest runtime routes contain duplicate id %q", route.ID)
		}
		seen[route.ID] = struct{}{}
		if canonical && i > 0 && last > route.ID {
			return fmt.Errorf("manifest runtime routes must be sorted by id")
		}
		last = route.ID
	}
	return nil
}

func validateJobs(jobs []JobContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(jobs))
	last := ""
	for i, job := range jobs {
		if strings.TrimSpace(job.Kind) == "" {
			return fmt.Errorf("manifest runtime jobs[%d] kind is invalid", i)
		}
		if !validPackagePath(job.Package) {
			return fmt.Errorf("manifest runtime jobs[%d] package is invalid", i)
		}
		if !validIdentifier(job.Handler) {
			return fmt.Errorf("manifest runtime jobs[%d] handler is invalid", i)
		}
		if job.MaxAttempts < 0 {
			return fmt.Errorf("manifest runtime jobs[%d] max_attempts is negative", i)
		}
		if _, ok := seen[job.Kind]; ok {
			return fmt.Errorf("manifest runtime jobs contain duplicate kind %q", job.Kind)
		}
		seen[job.Kind] = struct{}{}
		if canonical && i > 0 && last > job.Kind {
			return fmt.Errorf("manifest runtime jobs must be sorted by kind")
		}
		last = job.Kind
	}
	return nil
}

func validateContentTypes(items []ContentTypeContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("manifest runtime content_types[%d] id is invalid", i)
		}
		if !validContentMode(item.Mode) {
			return fmt.Errorf("manifest runtime content_types[%d] mode is invalid", i)
		}
		if item.Paths == nil {
			return fmt.Errorf("manifest runtime content_types[%d] paths array is required", i)
		}
		if err := validateStringSet(fmt.Sprintf("manifest runtime content_types[%d] paths", i), item.Paths, canonical, func(value string) error {
			return validateRoutePath(value)
		}); err != nil {
			return err
		}
		if !validPackagePath(item.Package) {
			return fmt.Errorf("manifest runtime content_types[%d] package is invalid", i)
		}
		if !validIdentifier(item.Handler) {
			return fmt.Errorf("manifest runtime content_types[%d] handler is invalid", i)
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("manifest runtime content_types contain duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime content_types must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func validateNavigation(items []NavigationContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.LabelKey) == "" {
			return fmt.Errorf("manifest runtime navigation[%d] required field is empty", i)
		}
		// A link needs exactly one target. A route id is preferred, because the
		// href then comes from the route table and cannot go stale; a literal
		// href is for the targets that are not routes, such as an in-page anchor.
		hasRoute := strings.TrimSpace(item.RouteID) != ""
		hasHref := strings.TrimSpace(item.Href) != ""
		if hasRoute == hasHref {
			return fmt.Errorf("manifest runtime navigation[%d] must declare exactly one of route_id and href", i)
		}
		if hasHref && !strings.HasPrefix(item.Href, "/") {
			return fmt.Errorf("manifest runtime navigation[%d] href must be site-relative", i)
		}
		if (item.Area == NavAreaFooter) != (strings.TrimSpace(item.Group) != "") {
			return fmt.Errorf("manifest runtime navigation[%d] group is required for footer entries and forbidden elsewhere", i)
		}
		if !validNavArea(item.Area) {
			return fmt.Errorf("manifest runtime navigation[%d] area is invalid", i)
		}
		sets := []struct {
			name   string
			values []string
		}{
			{"before", item.Before},
			{"after", item.After},
			{"roles", item.Roles},
			{"flags", item.Flags},
		}
		for _, set := range sets {
			if err := validateOptionalStringSet("manifest runtime navigation "+set.name, set.values, canonical); err != nil {
				return err
			}
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("manifest runtime navigation contains duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime navigation must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func validateSlots(items []SlotContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("manifest runtime slots[%d] id is invalid", i)
		}
		if !validShellSlot(item.Slot) {
			return fmt.Errorf("manifest runtime slots[%d] slot is invalid", i)
		}
		if !validPackagePath(item.Package) {
			return fmt.Errorf("manifest runtime slots[%d] package is invalid", i)
		}
		if !validIdentifier(item.Renderer) {
			return fmt.Errorf("manifest runtime slots[%d] renderer is invalid", i)
		}
		if err := validateOptionalStringSet("manifest runtime slots before", item.Before, canonical); err != nil {
			return err
		}
		if err := validateOptionalStringSet("manifest runtime slots after", item.After, canonical); err != nil {
			return err
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("manifest runtime slots contain duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime slots must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func validateUI(items []UIContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if !validComponentName(item.Name) {
			return fmt.Errorf("manifest runtime ui[%d] name is invalid", i)
		}
		if !validGalleryFamily(item.Family) {
			return fmt.Errorf("manifest runtime ui[%d] family is invalid", i)
		}
		// A declared signature is the renderer's exact templ declaration. Any
		// other shape means the manifest is describing something that is not the
		// renderer, and the reference would publish it verbatim.
		if item.Signature != "" && !strings.HasPrefix(item.Signature, "templ ") {
			return fmt.Errorf("manifest runtime ui[%d] signature must start with %q", i, "templ ")
		}
		if err := validateStringSet(
			fmt.Sprintf("manifest runtime ui[%d] states", i),
			item.States, canonical,
			func(value string) error {
				if !validUIState(value) {
					return fmt.Errorf("state %q is not a known rendering state", value)
				}
				return nil
			},
		); err != nil {
			return err
		}
		if _, ok := seen[item.Name]; ok {
			return fmt.Errorf("manifest runtime ui contains duplicate name %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		if canonical && i > 0 && last > item.Name {
			return fmt.Errorf("manifest runtime ui must be sorted by name")
		}
		last = item.Name
	}
	return nil
}

// validateScenarios checks the declared dev-catalog compositions. Slug, layout
// and state vocabularies are closed because every one of them is dereferenced
// somewhere that cannot fail softly: the slug becomes a URL segment and a
// baseline file name, the layout selects a real shell renderer, and a state is
// accepted or refused by the page.
func validateScenarios(items []ScenarioContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if !validComponentName(item.Slug) {
			return fmt.Errorf("manifest runtime scenarios[%d] slug is invalid", i)
		}
		if strings.TrimSpace(item.Title) == "" {
			return fmt.Errorf("manifest runtime scenarios[%d] title is invalid", i)
		}
		if strings.TrimSpace(item.Summary) == "" {
			return fmt.Errorf("manifest runtime scenarios[%d] summary is invalid", i)
		}
		if !validScenarioLayout(item.Layout) {
			return fmt.Errorf("manifest runtime scenarios[%d] layout %q is invalid", i, item.Layout)
		}
		// Surfaces are the reason the scenario exists. An empty list would
		// declare a composition that covers nothing, which the accessibility
		// sweep and the matrix would both silently accept.
		if len(item.Surfaces) == 0 {
			return fmt.Errorf("manifest runtime scenarios[%d] declares no surfaces", i)
		}
		if err := validateStringSet(
			fmt.Sprintf("manifest runtime scenarios[%d] surfaces", i),
			item.Surfaces, false,
			func(value string) error {
				if !validComponentName(value) {
					return fmt.Errorf("surface %q is not a component name", value)
				}
				return nil
			},
		); err != nil {
			return err
		}
		if err := validateStringSet(
			fmt.Sprintf("manifest runtime scenarios[%d] states", i),
			item.States, false,
			func(value string) error {
				if !validUIState(value) {
					return fmt.Errorf("state %q is not a known rendering state", value)
				}
				return nil
			},
		); err != nil {
			return err
		}
		if _, ok := seen[item.Slug]; ok {
			return fmt.Errorf("manifest runtime scenarios contain duplicate slug %q", item.Slug)
		}
		seen[item.Slug] = struct{}{}
		if canonical && i > 0 && last > item.Slug {
			return fmt.Errorf("manifest runtime scenarios must be sorted by slug")
		}
		last = item.Slug
	}
	return nil
}

// validateVisual checks the declared page surfaces of the comparison matrix.
// Viewports are required rather than defaulted: a surface that quietly gained a
// width would be compared against a baseline nobody captured, which reads as a
// failure in the page instead of a gap in the declaration.
func validateVisual(items []VisualContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if !validComponentName(item.ID) {
			return fmt.Errorf("manifest runtime visual[%d] id is invalid", i)
		}
		if !strings.HasPrefix(item.Path, "/") {
			return fmt.Errorf("manifest runtime visual[%d] path %q must be rooted", i, item.Path)
		}
		if item.Persona != "" && !validComponentName(item.Persona) {
			return fmt.Errorf("manifest runtime visual[%d] persona is invalid", i)
		}
		if len(item.Viewports) == 0 {
			return fmt.Errorf("manifest runtime visual[%d] declares no viewports", i)
		}
		if err := validateStringSet(
			fmt.Sprintf("manifest runtime visual[%d] viewports", i),
			item.Viewports, false,
			func(value string) error {
				if !validViewport(value) {
					return fmt.Errorf("viewport %q is not a compared width", value)
				}
				return nil
			},
		); err != nil {
			return err
		}
		if err := validateOptionalStringSet(
			fmt.Sprintf("manifest runtime visual[%d] masks", i), item.Masks, false); err != nil {
			return err
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("manifest runtime visual contains duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime visual must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func validateAssets(items []AssetContribution, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("manifest runtime assets[%d] id is invalid", i)
		}
		if !validAssetKind(item.Kind) {
			return fmt.Errorf("manifest runtime assets[%d] kind is invalid", i)
		}
		// An engine asset is injected at runtime rather than named by a template,
		// so integrity is the only thing standing between a swapped vendor file
		// and arbitrary execution. The two fields only make sense together: an
		// integrity value with no engine would be silently ignored, because the
		// shell's own script tags are owned by templates.
		if item.Engine != "" {
			if !validComponentName(item.Engine) {
				return fmt.Errorf("manifest runtime assets[%d] engine is invalid", i)
			}
			if strings.TrimSpace(item.Integrity) == "" {
				return fmt.Errorf("manifest runtime assets[%d] engine %q must declare integrity", i, item.Engine)
			}
		} else if strings.TrimSpace(item.Integrity) != "" {
			return fmt.Errorf("manifest runtime assets[%d] declares integrity without an engine", i)
		}
		if err := validateSafePath(item.Path); err != nil {
			return fmt.Errorf("manifest runtime assets[%d] path: %w", i, err)
		}
		if err := validateOptionalStringSet("manifest runtime assets before", item.Before, canonical); err != nil {
			return err
		}
		if err := validateOptionalStringSet("manifest runtime assets after", item.After, canonical); err != nil {
			return err
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("manifest runtime assets contain duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if canonical && i > 0 && last > item.ID {
			return fmt.Errorf("manifest runtime assets must be sorted by id")
		}
		last = item.ID
	}
	return nil
}

func validateEnvironment(items []EnvironmentVariable, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if !validEnvironmentKey(item.Key) {
			return fmt.Errorf("manifest environment[%d] key is invalid", i)
		}
		if !validIdentifier(item.Field) {
			return fmt.Errorf("manifest environment[%d] field is invalid", i)
		}
		if !item.Type.Valid() {
			return fmt.Errorf("manifest environment[%d] type is invalid", i)
		}
		if strings.TrimSpace(item.Description) == "" {
			return fmt.Errorf("manifest environment[%d] description is empty", i)
		}
		if _, ok := seen[item.Key]; ok {
			return fmt.Errorf("manifest environment contains duplicate key %q", item.Key)
		}
		seen[item.Key] = struct{}{}
		targets := map[string]struct{}{}
		for _, target := range item.Targets {
			if strings.TrimSpace(target) == "" || !strings.Contains(target, "@") {
				return fmt.Errorf("manifest environment[%d] target %q is invalid", i, target)
			}
			if _, ok := targets[target]; ok {
				return fmt.Errorf("manifest environment[%d] has duplicate target %q", i, target)
			}
			targets[target] = struct{}{}
		}
		if item.Type == EnvInt && item.Min != nil && item.Max != nil && *item.Min > *item.Max {
			return fmt.Errorf("manifest environment[%d] min exceeds max", i)
		}
		if canonical && i > 0 && last > item.Key {
			return fmt.Errorf("manifest environment must be sorted by key")
		}
		last = item.Key
	}
	return nil
}

func validateDocs(items []DocumentationRef, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if err := validateSafePath(item.Path); err != nil {
			return fmt.Errorf("manifest docs[%d] path: %w", i, err)
		}
		if strings.TrimSpace(item.Title) == "" {
			return fmt.Errorf("manifest docs[%d] title is empty", i)
		}
		if _, ok := seen[item.Path]; ok {
			return fmt.Errorf("manifest docs contain duplicate path %q", item.Path)
		}
		seen[item.Path] = struct{}{}
		if canonical && i > 0 && last > item.Path {
			return fmt.Errorf("manifest docs must be sorted by path")
		}
		last = item.Path
	}
	return nil
}

func validateTests(tests TestMetadata, canonical bool) error {
	sets := []struct {
		name string
		set  []string
	}{
		{"go_packages", tests.GoPackages}, {"smoke", tests.Smoke}, {"e2e", tests.E2E},
		{"visual", tests.Visual}, {"accessibility", tests.Accessibility}, {"capabilities", tests.Capabilities},
	}
	for _, item := range sets {
		if err := validateOptionalStringSet("manifest tests "+item.name, item.set, canonical); err != nil {
			return err
		}
	}
	return nil
}

func validateData(items []DataDeclaration, canonical bool) error {
	seen := make(map[string]struct{}, len(items))
	last := ""
	for i, item := range items {
		if !validSQLIdentifier(item.Table) {
			return fmt.Errorf("manifest data[%d] table is invalid", i)
		}
		if item.RowDiscriminator != "" && !validSQLIdentifier(item.RowDiscriminator) {
			return fmt.Errorf("manifest data[%d] row_discriminator is invalid", i)
		}
		if !validDataScope(item.Scope) {
			return fmt.Errorf("manifest data[%d] scope is invalid", i)
		}
		if !validDeleteBehavior(item.AccountDelete) {
			return fmt.Errorf("manifest data[%d] account_delete is invalid", i)
		}
		if !validDeleteBehavior(item.OrganizationDelete) {
			return fmt.Errorf("manifest data[%d] organization_delete is invalid", i)
		}
		if item.SecretRedactionOwner != "" {
			if err := ValidateInstallableModuleID(item.SecretRedactionOwner); err != nil {
				return fmt.Errorf("manifest data[%d] secret_redaction_owner: %w", i, err)
			}
		}
		if err := validateOptionalStringSet("manifest data external_objects", item.ExternalObjects, canonical); err != nil {
			return err
		}
		if err := validateOptionalStringSet("manifest data persisted_jobs", item.PersistedJobs, canonical); err != nil {
			return err
		}
		key := item.Table + "\x00" + item.RowDiscriminator
		if _, ok := seen[key]; ok {
			return fmt.Errorf("manifest data contains duplicate table/discriminator %q", item.Table)
		}
		seen[key] = struct{}{}
		if canonical && i > 0 && last > key {
			return fmt.Errorf("manifest data must be sorted by table and row_discriminator")
		}
		last = key
	}
	return nil
}

func validateLock(lock Lock, canonical bool) error {
	if lock.Schema != 2 {
		return fmt.Errorf("lock schema must be 2; migrate schema 1 explicitly")
	}
	if strings.TrimSpace(lock.RegistryCommit) == "" {
		return fmt.Errorf("lock registry_commit must be non-empty")
	}
	if lock.Registries == nil || lock.Snapshots == nil || lock.Order == nil || lock.RuntimeOrders.Development == nil ||
		lock.RuntimeOrders.Test == nil || lock.RuntimeOrders.Production == nil || lock.Dependencies == nil || lock.Modules == nil {
		return fmt.Errorf("lock registries, snapshots, order, runtime_orders, dependencies, and modules are required")
	}
	if err := validateStringSet("lock order", lock.Order, false, ValidateScopedProjectModuleID); err != nil {
		return err
	}

	moduleByID := make(map[string]*LockedModule, len(lock.Modules))
	lastModule := ""
	for i := range lock.Modules {
		module := &lock.Modules[i]
		if err := validateLockedModule(module, canonical); err != nil {
			return fmt.Errorf("lock modules[%d]: %w", i, err)
		}
		if _, ok := moduleByID[module.ID]; ok {
			return fmt.Errorf("lock modules contain duplicate id %q", module.ID)
		}
		moduleByID[module.ID] = module
		if canonical && i > 0 && lastModule > module.ID {
			return fmt.Errorf("lock modules must be sorted by id")
		}
		lastModule = module.ID
	}
	if len(lock.Order) != len(lock.Modules) {
		return fmt.Errorf("lock order must cover modules exactly")
	}
	position := make(map[string]int, len(lock.Order))
	for i, id := range lock.Order {
		if _, ok := moduleByID[id]; !ok {
			return fmt.Errorf("lock order contains unknown module %q", id)
		}
		position[id] = i
	}
	expectedRequiredBy := make(map[string][]string, len(lock.Modules))
	for _, module := range lock.Modules {
		expectedRequiredBy[module.ID] = []string{}
	}
	for i := range lock.Modules {
		module := &lock.Modules[i]
		modulePosition, ok := position[module.ID]
		if !ok {
			return fmt.Errorf("lock order does not contain module %q", module.ID)
		}
		for _, required := range module.Manifest.Requires {
			requiredPosition, ok := position[required.ID]
			if !ok {
				return fmt.Errorf("lock order omits required module %q", required.ID)
			}
			if requiredPosition >= modulePosition {
				return fmt.Errorf("lock order places dependency %q after %q", required.ID, module.ID)
			}
			expectedRequiredBy[required.ID] = append(expectedRequiredBy[required.ID], module.ID)
		}
	}
	for i := range lock.Modules {
		module := &lock.Modules[i]
		expected := expectedRequiredBy[module.ID]
		if !equalStrings(module.RequiredBy, expected) {
			return fmt.Errorf(
				"lock modules[%d] required_by does not match direct reverse dependencies: got %v, want %v",
				i, module.RequiredBy, expected,
			)
		}
	}

	migrationIDs := make(map[string]string)
	migrationNumbers := make(map[int]string)
	migrationPaths := make(map[string]string)
	for _, module := range lock.Modules {
		for _, migration := range module.Migrations {
			if owner, ok := migrationIDs[migration.ID]; ok {
				return fmt.Errorf("lock migrations contain duplicate id %q in %s and %s", migration.ID, owner, module.ID)
			}
			migrationIDs[migration.ID] = module.ID
			if owner, ok := migrationNumbers[migration.Number]; ok {
				return fmt.Errorf("lock migrations contain duplicate number %d in %s and %s", migration.Number, owner, module.ID)
			}
			migrationNumbers[migration.Number] = module.ID
			if owner, ok := migrationPaths[migration.Path]; ok {
				return fmt.Errorf("lock migrations contain duplicate path %q in %s and %s", migration.Path, owner, module.ID)
			}
			migrationPaths[migration.Path] = module.ID
		}
	}
	return nil
}

func validateLockedModule(module *LockedModule, canonical bool) error {
	if err := ValidateScopedProjectModuleID(module.ID); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if module.Revision <= 0 {
		return fmt.Errorf("revision must be positive")
	}
	if module.Contract <= 0 {
		return fmt.Errorf("contract must be positive")
	}
	if strings.TrimSpace(module.SourceCommit) == "" {
		return fmt.Errorf("source_commit must be non-empty")
	}
	if strings.TrimSpace(module.Reason) == "" {
		return fmt.Errorf("reason must be non-empty")
	}
	if module.RequiredBy == nil {
		return fmt.Errorf("required_by array is required")
	}
	if err := validateStringSet("required_by", module.RequiredBy, canonical, ValidateScopedProjectModuleID); err != nil {
		return err
	}
	if err := validateManifest(module.Manifest, canonical); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if module.Manifest.TestOnly {
		return fmt.Errorf("manifest test_only is not allowed in a production lock")
	}
	if module.Manifest.ID != module.ID || module.Manifest.Revision != module.Revision || module.Manifest.Contract != module.Contract {
		return fmt.Errorf("manifest identity/revision/contract does not match locked module")
	}
	if module.Files == nil {
		return fmt.Errorf("files array is required")
	}
	if module.Migrations == nil {
		return fmt.Errorf("migrations array is required")
	}
	manifestFiles := make(map[string]ManifestFile, len(module.Manifest.Files))
	for _, file := range module.Manifest.Files {
		manifestFiles[file.Target] = file
	}
	seenFiles := make(map[string]struct{}, len(module.Files))
	last := ""
	for i, file := range module.Files {
		if err := validateLockedFile(file); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
		if _, ok := seenFiles[file.Path]; ok {
			return fmt.Errorf("files contain duplicate path %q", file.Path)
		}
		seenFiles[file.Path] = struct{}{}
		manifestFile, ok := manifestFiles[file.Path]
		if !ok {
			return fmt.Errorf("files path %q is not owned by manifest", file.Path)
		}
		if file.Source != manifestFile.Source {
			return fmt.Errorf("files path %q source does not match manifest", file.Path)
		}
		// Generated targets carry no digest on either side.
		if manifestFile.Class == FileClassGenerated {
			if file.State != FileGenerated {
				return fmt.Errorf("files path %q is generated by the manifest and must be recorded as generated", file.Path)
			}
			continue
		}
		if !manifestFile.RewriteModule && file.BaseSHA256 != manifestFile.SHA256 {
			matchesPending := false
			if module.Pending != nil {
				for _, pendingFile := range module.Pending.Manifest.Files {
					if pendingFile.Target == file.Path && file.BaseSHA256 == pendingFile.SHA256 {
						matchesPending = true
						break
					}
				}
			}
			if !matchesPending {
				return fmt.Errorf("files path %q base_sha256 does not match manifest sha256", file.Path)
			}
		}
		if canonical && i > 0 && last > file.Path {
			return fmt.Errorf("files must be sorted by path")
		}
		last = file.Path
	}
	if len(seenFiles) != len(manifestFiles) {
		return fmt.Errorf("files must cover every manifest file target exactly")
	}

	seenMigrations := make(map[string]struct{}, len(module.Migrations))
	last = ""
	for i, migration := range module.Migrations {
		if strings.TrimSpace(migration.ID) == "" || migration.ID != strings.TrimSpace(migration.ID) {
			return fmt.Errorf("migrations[%d] id is invalid", i)
		}
		if migration.Number <= 0 {
			return fmt.Errorf("migrations[%d] number must be positive", i)
		}
		if err := validateSafePath(migration.Path); err != nil {
			return fmt.Errorf("migrations[%d] path: %w", i, err)
		}
		if !validSHA256(migration.SHA256) {
			return fmt.Errorf("migrations[%d] sha256 is invalid", i)
		}
		if _, ok := seenMigrations[migration.ID]; ok {
			return fmt.Errorf("migrations contain duplicate id %q", migration.ID)
		}
		seenMigrations[migration.ID] = struct{}{}
		if canonical && i > 0 && last > migration.ID {
			return fmt.Errorf("migrations must be sorted by id")
		}
		last = migration.ID
	}
	if module.Pending != nil {
		if err := validatePending(module.ID, module.Pending, module.Files, canonical); err != nil {
			return fmt.Errorf("pending: %w", err)
		}
	} else {
		for _, file := range module.Files {
			if file.State == FileConflicted {
				return fmt.Errorf("files path %q state conflicted requires pending metadata", file.Path)
			}
		}
	}
	return nil
}

func validateLockedFile(file LockedFile) error {
	if err := validateSafePath(file.Path); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if err := validateSafePath(file.Source); err != nil {
		return fmt.Errorf("source path: %w", err)
	}
	// A generated target has no canonical bytes; everything else is pinned.
	if file.State != FileGenerated && !validSHA256(file.BaseSHA256) {
		return fmt.Errorf("base_sha256 is invalid")
	}
	if file.State == FileGenerated && file.BaseSHA256 != "" {
		return fmt.Errorf("base_sha256 must be empty for a generated file")
	}
	if !validFileState(file.State) {
		return fmt.Errorf("state %q is invalid", file.State)
	}
	switch file.State {
	case FileClean:
		if !validSHA256(file.LocalSHA256) || file.LocalSHA256 != file.BaseSHA256 {
			return fmt.Errorf("local_sha256 for clean state must equal base_sha256")
		}
	case FileModified, FileConflicted:
		if !validSHA256(file.LocalSHA256) || file.LocalSHA256 == file.BaseSHA256 {
			return fmt.Errorf("local_sha256 for %s state must be valid and differ from base_sha256", file.State)
		}
	case FileMissing, FileGenerated:
		if file.LocalSHA256 != "" {
			return fmt.Errorf("local_sha256 for %s state must be empty", file.State)
		}
	}
	return nil
}

func validatePending(moduleID string, pending *PendingUpdate, files []LockedFile, canonical bool) error {
	if strings.TrimSpace(pending.RunID) == "" {
		return fmt.Errorf("run_id must be non-empty")
	}
	if strings.TrimSpace(pending.RegistryCommit) == "" {
		return fmt.Errorf("registry_commit must be non-empty")
	}
	if strings.TrimSpace(pending.SourceCommit) == "" {
		return fmt.Errorf("source_commit must be non-empty")
	}
	if err := validateManifest(pending.Manifest, canonical); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if pending.Manifest.TestOnly {
		return fmt.Errorf("manifest test_only is not allowed in a production lock")
	}
	if pending.Manifest.ID != moduleID {
		return fmt.Errorf("manifest id does not match locked module")
	}
	if pending.Conflicts == nil {
		return fmt.Errorf("conflicts array is required")
	}
	lockedFiles := make(map[string]LockedFile, len(files))
	for _, file := range files {
		lockedFiles[file.Path] = file
	}
	candidateFiles := make(map[string]ManifestFile, len(pending.Manifest.Files))
	for _, file := range pending.Manifest.Files {
		candidateFiles[file.Target] = file
	}
	seen := make(map[string]struct{}, len(pending.Conflicts))
	last := ""
	for i, conflict := range pending.Conflicts {
		if err := validateSafePath(conflict.Path); err != nil {
			return fmt.Errorf("conflicts[%d] path: %w", i, err)
		}
		if !validSHA256(conflict.CandidateSHA256) {
			return fmt.Errorf("conflicts[%d] candidate_sha256 is invalid", i)
		}
		if err := validateSafePath(conflict.CandidatePath); err != nil {
			return fmt.Errorf("conflicts[%d] candidate_path: %w", i, err)
		}
		if !strings.HasPrefix(conflict.CandidatePath, conflictArtifactPrefix) {
			return fmt.Errorf("conflicts[%d] candidate_path must stay under %s", i, conflictArtifactPrefix)
		}
		if err := validateSafePath(conflict.DiffPath); err != nil {
			return fmt.Errorf("conflicts[%d] diff_path: %w", i, err)
		}
		if !strings.HasPrefix(conflict.DiffPath, conflictArtifactPrefix) {
			return fmt.Errorf("conflicts[%d] diff_path must stay under %s", i, conflictArtifactPrefix)
		}
		locked, ok := lockedFiles[conflict.Path]
		if !ok {
			return fmt.Errorf("conflicts[%d] path is not a locked file", i)
		}
		if locked.State != FileConflicted {
			return fmt.Errorf("conflicts[%d] path does not have conflicted state", i)
		}
		candidate, ok := candidateFiles[conflict.Path]
		if !ok {
			return fmt.Errorf("conflicts[%d] path is not owned by pending manifest", i)
		}
		if !candidate.RewriteModule && candidate.SHA256 != conflict.CandidateSHA256 {
			return fmt.Errorf("conflicts[%d] candidate_sha256 does not match pending manifest", i)
		}
		if _, ok := seen[conflict.Path]; ok {
			return fmt.Errorf("conflicts contain duplicate path %q", conflict.Path)
		}
		seen[conflict.Path] = struct{}{}
		if canonical && i > 0 && last > conflict.Path {
			return fmt.Errorf("conflicts must be sorted by path")
		}
		last = conflict.Path
	}
	for _, file := range files {
		if file.State == FileConflicted {
			if _, ok := seen[file.Path]; !ok {
				return fmt.Errorf("conflicts must cover conflicted file %q", file.Path)
			}
		}
	}
	return nil
}

func validateStringSet(field string, values []string, canonical bool, validate func(string) error) error {
	seen := make(map[string]struct{}, len(values))
	last := ""
	for i, value := range values {
		if err := validate(value); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s contains duplicate value %q", field, value)
		}
		seen[value] = struct{}{}
		if canonical && i > 0 && last > value {
			return fmt.Errorf("%s must be sorted", field)
		}
		last = value
	}
	return nil
}
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validateOptionalStringSet(field string, values []string, canonical bool) error {
	if values == nil {
		return nil
	}
	return validateStringSet(field, values, canonical, func(value string) error {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("value must be non-empty and trimmed")
		}
		return nil
	})
}

func validateProjectModuleID(id string) error {
	kind, _, ok := splitModuleID(id)
	if !ok {
		return fmt.Errorf("module id %q is invalid", id)
	}
	switch kind {
	case "element", "component", "page", "workflow", "system", "profile":
		return nil
	default:
		return fmt.Errorf("module kind %q is invalid", kind)
	}
}

func ValidateInstallableModuleID(id string) error {
	kind, _, ok := splitModuleID(id)
	if !ok || !validModuleKind(ModuleKind(kind)) {
		return fmt.Errorf("module id %q is invalid", id)
	}
	return nil
}

func splitModuleID(id string) (string, string, bool) {
	if strings.Count(id, "/") != 1 {
		return "", "", false
	}
	kind, name, _ := strings.Cut(id, "/")
	return kind, name, validKebab(kind) && validKebab(name)
}

func validKebab(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' || value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			previousDash = false
		case r == '-' && !previousDash:
			previousDash = true
		default:
			return false
		}
	}
	return true
}

func validateRepository(repository string) error {
	if strings.Count(repository, "/") != 1 {
		return fmt.Errorf("must have owner/repo form")
	}
	owner, repo, _ := strings.Cut(repository, "/")
	if !validRepositoryPart(owner) || !validRepositoryPart(repo) {
		return fmt.Errorf("must have safe owner/repo form")
	}
	return nil
}

func validRepositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func validateSafePath(value string) error {
	if value == "" {
		return fmt.Errorf("must be a safe relative slash path")
	}
	for _, segment := range strings.Split(value, "/") {
		if !validSafePathSegment(segment) {
			return fmt.Errorf("must be a safe relative slash path")
		}
	}
	return nil
}

func validSafePathSegment(value string) bool {
	dots := 0
	for dots < len(value) && dots < 2 && value[dots] == '.' {
		dots++
	}
	if dots == len(value) || !validSafePathInitial(value[dots]) {
		return false
	}
	for i := dots + 1; i < len(value); i++ {
		if !validSafePathInitial(value[i]) && value[i] != '.' {
			return false
		}
	}
	return true
}

func validSafePathInitial(value byte) bool {
	return value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' ||
		value == '_' || value == '~' || value == '+' || value == '@' || value == '-'
}

func validateRoutePath(value string) error {
	if value == "/" {
		return nil
	}
	if value == "" || value[0] != '/' {
		return fmt.Errorf("must be a safe absolute slash path")
	}
	// A single trailing slash is Go's subtree pattern, and real surfaces need it:
	// "/static/" serves an asset tree, "/debug/pprof/" serves the profiler index.
	// Strip it before checking segments so the empty tail is not mistaken for an
	// empty segment; "//" is still rejected because that leaves a genuine empty
	// segment behind.
	trimmed := strings.TrimSuffix(value, "/")
	if trimmed == "" {
		return fmt.Errorf("must be a safe absolute slash path")
	}
	for _, segment := range strings.Split(trimmed[1:], "/") {
		if !validRoutePathSegment(segment) {
			return fmt.Errorf("must be a safe absolute slash path")
		}
	}
	return nil
}

func validRoutePathSegment(value string) bool {
	if validSafePathSegment(value) {
		return true
	}
	if len(value) < 3 || value[0] != '{' || value[len(value)-1] != '}' {
		return false
	}
	wildcard := value[1 : len(value)-1]
	if wildcard == "$" {
		return true
	}
	wildcard = strings.TrimSuffix(wildcard, "...")
	return validIdentifier(wildcard)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func validModuleKind(value ModuleKind) bool {
	switch value {
	case ModuleElement, ModuleComponent, ModulePage, ModuleWorkflow, ModuleSystem:
		return true
	default:
		return false
	}
}

func validRemovalPolicy(value RemovalPolicy) bool {
	switch value {
	case RemovalFree, RemovalRetainData, RemovalDrainRequired, RemovalReplacementRequired, RemovalMajorVersionOnly:
		return true
	default:
		return false
	}
}

func validFileState(value FileState) bool {
	switch value {
	case FileClean, FileModified, FileMissing, FileConflicted, FileGenerated:
		return true
	default:
		return false
	}
}

func validFileClass(value FileClass) bool {
	switch value {
	case FileClassGo, FileClassTempl, FileClassStyle, FileClassScript, FileClassAsset, FileClassQuery, FileClassGenerated,
		FileClassMigration, FileClassI18n, FileClassContent, FileClassSeed, FileClassDocs, FileClassTest, FileClassOpenAPI:
		return true
	default:
		return false
	}
}

func validMigrationKind(value MigrationKind) bool {
	return value == MigrationImmutable || value == MigrationNeutralize || value == MigrationPurge
}

func validDataScope(value DataScope) bool {
	return value == DataScopeUser || value == DataScopeOrg || value == DataScopePlatform
}

func validDeleteBehavior(value DeleteBehavior) bool {
	return value == DeleteCascade || value == DeleteManual || value == DeleteRetain
}

func validRouteScope(value RouteScope) bool {
	switch value {
	case RoutePublic, RouteApp, RouteAdmin, RouteAPIRead, RouteAPIWrite, RouteWebhook, RouteStatic, RouteProbe, RouteDev:
		return true
	default:
		return false
	}
}
func validContentMode(value ContentMode) bool {
	return value == ContentModePages || value == ContentModeSingle
}

func validNavArea(value NavArea) bool {
	switch value {
	case NavAreaPublic, NavAreaApp, NavAreaAdmin, NavAreaFooter, NavAreaSettings:
		return true
	default:
		return false
	}
}

func validShellSlot(value ShellSlot) bool {
	switch value {
	case ShellSlotHead, ShellSlotAppBanner, ShellSlotSidebar, ShellSlotTopbar,
		ShellSlotPersistentBody, ShellSlotDashboardWidget, ShellSlotSettingsSection,
		ShellSlotAdminRowAction, ShellSlotBillingUsage, ShellSlotContentEditor:
		return true
	default:
		return false
	}
}

// validGalleryFamily reads the ordered family list rather than restating it, so
// the set the manifest accepts and the order the visual matrix walks can never
// drift apart.
func validGalleryFamily(value GalleryFamily) bool {
	for _, family := range GalleryFamilies {
		if family == value {
			return true
		}
	}
	return false
}

// validUIState is the closed set of rendering states a component may declare.
// The reference pages and the visual matrix render exactly the states listed on
// a component, so a value outside this set would name a state nothing knows how
// to produce — a silent documentation hole rather than a build failure.
//
// "hover" and "focus" are deliberately absent: the browser applies them, so no
// renderer can produce one and a declaration would be a state the reference
// cannot show. Locale, direction, density and long content are absent for a
// different reason — they are context dimensions that apply to every component
// at once, so they live in GalleryContext rather than being repeated on 172
// entries.
func validUIState(value string) bool {
	switch value {
	case "default", "disabled", "readonly", "busy", "loading",
		"empty", "error", "success", "overflow":
		return true
	default:
		return false
	}
}

// validScenarioLayout is the closed set of shells a scenario may render inside.
// It is narrower than the layouts the renderer has: "docs" is absent because a
// scenario is a product surface, and rendering one in the documentation shell
// would compare chrome no product page uses.
func validScenarioLayout(value string) bool {
	switch value {
	case "public", "app", "admin":
		return true
	default:
		return false
	}
}

// validViewport is the closed set of widths the comparison job configures. A
// value outside it names a Playwright project that does not exist, which would
// silently drop the surface from the run instead of failing.
func validViewport(value string) bool {
	switch value {
	case "desktop", "tablet", "mobile":
		return true
	default:
		return false
	}
}

func validAssetKind(value AssetKind) bool {
	switch value {
	case AssetScript, AssetStyle, AssetFont, AssetImage, AssetFile:
		return true
	default:
		return false
	}
}

func validHTTPMethod(value string) bool {
	switch value {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

// validComponentName accepts the kebab-case slug a component renders as its
// data-ui value: lowercase words joined by single hyphens. Anything else could
// not be matched by the attribute selector the gallery and tests rely on.
func validComponentName(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	prevHyphen := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			prevHyphen = false
		case c == '-':
			if prevHyphen {
				return false
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return true
}

func validIdentifier(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for _, r := range value[1:] {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func validPackagePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, r := range segment {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
				r == '-' || r == '.' || r == '_' || r == '~') {
				return false
			}
		}
	}
	return true
}

func validGoTypeRef(value string) bool {
	for {
		switch {
		case strings.HasPrefix(value, "*"):
			value = value[1:]
		case strings.HasPrefix(value, "[]"):
			value = value[2:]
		default:
			goto base
		}
	}

base:
	if value == "" || strings.Count(value, ".") > 1 {
		return false
	}
	qualifier, identifier, qualified := strings.Cut(value, ".")
	if !qualified {
		return validIdentifier(value)
	}
	return validIdentifier(qualifier) && validIdentifier(identifier)
}

func validEnvironmentKey(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, r := range value {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func validSQLIdentifier(value string) bool {
	if value == "" || !((value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func validateAdapterEnvironment(items []EnvironmentVariable, system *SystemContribution) error {
	if system == nil || system.Adapter == nil {
		return nil
	}
	declared := map[string]EnvironmentVariable{}
	for _, item := range items {
		declared[item.Key] = item
	}
	mapped := map[string]string{}
	for _, target := range system.Adapter.Targets {
		for _, input := range target.Inputs {
			if input.EnvKey == "" {
				continue
			}
			item, ok := declared[input.EnvKey]
			if !ok {
				return fmt.Errorf("adapter target %s input %q maps unknown env key %q", target.ID, input.Key, input.EnvKey)
			}
			if prior, exists := mapped[input.EnvKey]; exists {
				return fmt.Errorf("adapter env key %q maps targets %s and %s", input.EnvKey, prior, target.ID)
			}
			mapped[input.EnvKey] = target.ID
			wantType := EnvString
			switch input.Type {
			case "integer":
				wantType = EnvInt
			case "boolean":
				wantType = EnvBool
			}
			if wantType != item.Type {
				return fmt.Errorf("adapter env key %q type mismatch: target %s vs declaration %s", input.EnvKey, wantType, item.Type)
			}
			if input.Secret != item.Secret {
				return fmt.Errorf("adapter env key %q secret mismatch", input.EnvKey)
			}
		}
	}
	return nil
}

func validateEnvironmentTargets(items []EnvironmentVariable, system *SystemContribution, adapterID string) error {
	known := map[string]struct{}{}
	if system != nil && system.Adapter != nil {
		for _, target := range system.Adapter.Targets {
			known[adapterID+"@"+target.ID] = struct{}{}
		}
	}
	for i, item := range items {
		for _, target := range item.Targets {
			if system == nil || system.Adapter == nil {
				return fmt.Errorf("manifest environment[%d] target %q requires an adapter", i, target)
			}
			if _, ok := known[target]; !ok {
				return fmt.Errorf("manifest environment[%d] target %q is not declared by adapter", i, target)
			}
		}
	}
	return nil
}
