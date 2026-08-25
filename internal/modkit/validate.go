package modkit

import (
	"fmt"
	"strings"
)

func validateProject(p Project, canonical bool) error {
	if p.Schema != 1 {
		return fmt.Errorf("project schema must be 1")
	}
	if err := validateRepository(p.Registry.Repository); err != nil {
		return fmt.Errorf("project registry repository: %w", err)
	}
	if strings.TrimSpace(p.Registry.Ref) == "" || p.Registry.Ref != strings.TrimSpace(p.Registry.Ref) {
		return fmt.Errorf("project registry ref must be non-empty and trimmed")
	}
	if p.Modules == nil {
		return fmt.Errorf("project modules array is required")
	}
	if p.Exclude == nil {
		return fmt.Errorf("project exclude array is required")
	}
	if err := validateStringSet("project modules", p.Modules, canonical, validateProjectModuleID); err != nil {
		return err
	}
	if err := validateStringSet("project exclude", p.Exclude, canonical, validateProjectModuleID); err != nil {
		return err
	}

	selected := make(map[string]struct{}, len(p.Modules))
	hasProfile := false
	for _, id := range p.Modules {
		selected[id] = struct{}{}
		kind, _, _ := splitModuleID(id)
		hasProfile = hasProfile || kind == "profile"
	}
	for _, id := range p.Exclude {
		kind, _, _ := splitModuleID(id)
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
	return nil
}

func validateManifest(m Manifest, canonical bool) error {
	kind, name, ok := splitModuleID(m.ID)
	if !ok || !validModuleKind(ModuleKind(kind)) {
		return fmt.Errorf("manifest id %q is not a valid module id", m.ID)
	}
	if !validModuleKind(m.Kind) {
		return fmt.Errorf("manifest kind %q is invalid", m.Kind)
	}
	if m.Name == "" || !validKebab(m.Name) {
		return fmt.Errorf("manifest name %q is invalid", m.Name)
	}
	if kind != string(m.Kind) || name != m.Name || m.ID != string(m.Kind)+"/"+m.Name {
		return fmt.Errorf("manifest identity does not match id, kind, and name")
	}
	if m.Revision <= 0 {
		return fmt.Errorf("manifest revision must be positive")
	}
	if m.Contract <= 0 {
		return fmt.Errorf("manifest contract must be positive")
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("manifest title must be non-empty")
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("manifest description must be non-empty")
	}
	if !validRemovalPolicy(m.RemovalPolicy) {
		return fmt.Errorf("manifest removal_policy %q is invalid", m.RemovalPolicy)
	}
	if m.Requires == nil {
		return fmt.Errorf("manifest requires array is required")
	}
	if err := validateStringSet("manifest requires", m.Requires, canonical, validateInstallableModuleID); err != nil {
		return err
	}
	for _, required := range m.Requires {
		if required == m.ID {
			return fmt.Errorf("manifest requires cannot contain its own id")
		}
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
	if m.Environment == nil {
		return fmt.Errorf("manifest environment array is required")
	}
	if err := validateEnvironment(m.Environment, canonical); err != nil {
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
		{"assets", claims.Assets}, {"data", claims.Data},
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
		if route.Policy.MaxBodyBytes < 0 {
			return fmt.Errorf("manifest runtime routes[%d] policy max_body_bytes is negative", i)
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
		if !validIdentifier(item.Name) {
			return fmt.Errorf("manifest runtime ui[%d] name is invalid", i)
		}
		if !validGalleryFamily(item.Family) {
			return fmt.Errorf("manifest runtime ui[%d] family is invalid", i)
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
		if strings.TrimSpace(item.Description) == "" {
			return fmt.Errorf("manifest environment[%d] description is empty", i)
		}
		if _, ok := seen[item.Key]; ok {
			return fmt.Errorf("manifest environment contains duplicate key %q", item.Key)
		}
		seen[item.Key] = struct{}{}
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
			if err := validateInstallableModuleID(item.SecretRedactionOwner); err != nil {
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
	if lock.Schema != 1 {
		return fmt.Errorf("lock schema must be 1")
	}
	if strings.TrimSpace(lock.RegistryCommit) == "" {
		return fmt.Errorf("lock registry_commit must be non-empty")
	}
	if lock.Order == nil {
		return fmt.Errorf("lock order array is required")
	}
	if lock.Modules == nil {
		return fmt.Errorf("lock modules array is required")
	}
	if err := validateStringSet("lock order", lock.Order, false, validateInstallableModuleID); err != nil {
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
			requiredPosition, ok := position[required]
			if !ok {
				return fmt.Errorf("lock order omits required module %q", required)
			}
			if requiredPosition >= modulePosition {
				return fmt.Errorf("lock order places dependency %q after %q", required, module.ID)
			}
			expectedRequiredBy[required] = append(expectedRequiredBy[required], module.ID)
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
	if err := validateInstallableModuleID(module.ID); err != nil {
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
	if err := validateStringSet("required_by", module.RequiredBy, canonical, validateInstallableModuleID); err != nil {
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

func validateInstallableModuleID(id string) error {
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

func validGalleryFamily(value GalleryFamily) bool {
	switch value {
	case GalleryFoundations, GalleryActions, GalleryForms, GalleryNavigation, GalleryFeedback,
		GalleryOverlays, GalleryData, GalleryCommunication, GalleryLayout, GalleryAdvanced:
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
