package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"
)

// removalTombstoneReason marks a lock module whose authored files were removed
// but whose immutable migration ledger is retained forever.
const removalTombstoneReason = "removed"

func (e *Engine) planRemove(
	ctx context.Context,
	canonicalRoot string,
	project Project,
	modulePath string,
	currentLock Lock,
	op Operation,
) (Plan, error) {
	if len(op.Modules) == 0 {
		return Plan{}, fmt.Errorf("remove requires at least one module")
	}
	if op.RegistryRef != "" {
		return Plan{}, fmt.Errorf("remove does not accept a registry ref")
	}
	requested := append([]string{}, op.Modules...)
	sort.Strings(requested)
	requested = dedupeSorted(requested)

	installed := make(map[string]LockedModule, len(currentLock.Modules))
	for _, module := range currentLock.Modules {
		if module.Reason == removalTombstoneReason {
			continue
		}
		installed[module.ID] = module
	}
	requestSet := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		requestSet[id] = struct{}{}
		if _, ok := installed[id]; !ok {
			return Plan{}, fmt.Errorf("module %s is not installed", id)
		}
		for slot, selections := range project.Providers {
			for env, choice := range []ProviderSelection{selections.Development, selections.Test, selections.Production} {
				if choice.Adapter == id {
					return Plan{}, fmt.Errorf("cannot remove selected adapter %s for provider slot %s; replace all environment selections first", id, slot)
				}
				_ = env
			}
		}
	}

	for _, id := range requested {
		for _, other := range currentLock.Modules {
			if other.ID == id || other.Reason == removalTombstoneReason {
				continue
			}
			if _, alsoRemoved := requestSet[other.ID]; alsoRemoved {
				continue
			}
			for _, requirement := range other.Manifest.Requires {
				if requirement.ID == id {
					return Plan{}, fmt.Errorf("module %s is required by %s; remove the dependent first", id, other.ID)
				}
			}
		}
	}

	needsDrainSnapshot := false
	for _, id := range requested {
		module := installed[id]
		switch module.Manifest.RemovalPolicy {
		case RemovalReplacementRequired:
			return Plan{}, fmt.Errorf("module %s is replacement-required; replacing it is a manual migration, not a removal", id)
		case RemovalMajorVersionOnly:
			return Plan{}, fmt.Errorf("module %s can only be removed in a major version change", id)
		case RemovalDrainRequired:
			if !manifestHasMigrationKind(module.Manifest, MigrationNeutralize) {
				return Plan{}, fmt.Errorf(
					"drain-required module %s cannot be removed without a reviewed forward neutralization migration", id,
				)
			}
			if op.PurgeData && !manifestHasMigrationKind(module.Manifest, MigrationPurge) {
				return Plan{}, fmt.Errorf(
					"--purge-data requires module %s to declare a reviewed forward teardown migration", id,
				)
			}
			needsDrainSnapshot = true
		}
	}

	changes := make([]Change, 0)
	for _, id := range requested {
		module := installed[id]
		for _, file := range module.Files {
			if err := ctx.Err(); err != nil {
				return Plan{}, err
			}
			_, digest, missing, err := currentTargetState(canonicalRoot, file.Path)
			if err != nil {
				return Plan{}, err
			}
			if missing {
				return Plan{}, fmt.Errorf(
					"owned file %s of module %s is missing; restore or delete it deliberately before removing", file.Path, id,
				)
			}
			if digest != file.BaseSHA256 {
				return Plan{}, fmt.Errorf(
					"module %s owns locally modified file %s; run ggg diff %s and revert or back up the customization before removing",
					id, file.Path, id,
				)
			}
			changes = append(changes, Change{
				Path: file.Path, Module: id, Source: file.Source,
				Kind: ChangeDelete, Class: DestinationAuthored, SHA256: file.BaseSHA256,
			})
		}
	}

	// A module's sources are declared; the build output derived from them is not,
	// because the emitter owns it. Removal has to take that output with the
	// source: a deleted `X.templ` whose generated `X_templ.go` survives still
	// defines the renderer, so the component keeps rendering and removal is a
	// report of work that did not happen.
	declared := make([]string, 0, len(changes))
	for _, change := range changes {
		declared = append(declared, change.Path)
	}
	for _, id := range requested {
		owned := make([]string, 0)
		for _, file := range installed[id].Files {
			owned = append(owned, file.Path)
		}
		for _, path := range generatedSiblings(owned) {
			if slices.Contains(declared, path) {
				continue
			}
			_, digest, missing, err := currentTargetState(canonicalRoot, path)
			if err != nil {
				return Plan{}, err
			}
			// Absent output is nothing to undo - a project that has never run
			// the generator is a legal state, not a failure.
			if missing {
				continue
			}
			changes = append(changes, Change{
				Path: path, Module: id, Kind: ChangeDelete,
				Class: DestinationGenerated, SHA256: digest,
			})
			declared = append(declared, path)
		}
	}

	var drainSnapshots map[string]Snapshot
	if needsDrainSnapshot {
		if op.Offline {
			return Plan{}, fmt.Errorf("drain-required removal needs the registry at a pinned snapshot; offline removal cannot materialize it")
		}
		drainSnapshots = make(map[string]Snapshot)
		for _, id := range requested {
			module := installed[id]
			if module.Manifest.RemovalPolicy != RemovalDrainRequired {
				continue
			}
			namespace := module.RegistryNamespace
			if namespace == "" {
				return Plan{}, fmt.Errorf("drain-required module %s has no registry namespace pin", id)
			}
			var configured ProjectRegistry
			foundRegistry := false
			for _, candidate := range project.Registries {
				if candidate.Namespace == namespace {
					configured = candidate
					foundRegistry = true
					break
				}
			}
			if !foundRegistry {
				return Plan{}, fmt.Errorf("drain-required module %s registry namespace %q is not configured", id, namespace)
			}
			var pinned LockedSnapshot
			foundSnapshot := false
			for _, candidate := range currentLock.Snapshots {
				if candidate.Namespace == namespace {
					pinned = candidate
					foundSnapshot = true
					break
				}
			}
			if !foundSnapshot || pinned.Commit == "" {
				return Plan{}, fmt.Errorf("drain-required module %s has no pinned snapshot for registry namespace %q", id, namespace)
			}
			configured.Ref = pinned.Commit
			snapshot, err := e.source.Resolve(ctx, configured)
			if err != nil {
				return Plan{}, fmt.Errorf("resolve registry %s at %s for drain migrations: %w",
					namespace, pinned.Commit, err)
			}
			if snapshot.Commit != pinned.Commit {
				return Plan{}, fmt.Errorf("drain registry %s resolved commit %s, want lock snapshot commit %s",
					namespace, snapshot.Commit, pinned.Commit)
			}
			drainSnapshots[namespace] = snapshot
		}
	}

	desired := project
	desired.Modules = append([]string{}, project.Modules...)
	desired.Exclude = append([]string{}, project.Exclude...)
	for _, id := range requested {
		desired.Modules = removeString(desired.Modules, id)
		for _, selected := range desired.Modules {
			if strings.HasSuffix(selected, "/profile/"+strings.TrimPrefix(selected, "ggg/profile/")) || strings.HasPrefix(selected, "ggg/profile/") {
				desired.Exclude = append(desired.Exclude, id)
				break
			}
		}
	}
	sort.Strings(desired.Exclude)
	desired.Exclude = dedupeSorted(desired.Exclude)
	if _, err := MarshalProject(desired); err != nil {
		return Plan{}, fmt.Errorf("plan removal intent: %w", err)
	}

	allocated := make(map[string][]LockedMigration, len(requested))
	if len(drainSnapshots) > 0 {
		maxNumber := 0
		for _, module := range currentLock.Modules {
			for _, migration := range module.Migrations {
				if migration.Number > maxNumber {
					maxNumber = migration.Number
				}
			}
		}
		diskMax, err := scanMigrationNumbers(canonicalRoot)
		if err != nil {
			return Plan{}, err
		}
		if diskMax > maxNumber {
			maxNumber = diskMax
		}
		for _, id := range requested {
			module := installed[id]
			retained := make(map[string]LockedMigration, len(module.Migrations))
			for _, existing := range module.Migrations {
				retained[existing.ID] = existing
			}
			if module.Manifest.RemovalPolicy != RemovalDrainRequired {
				continue
			}
			kinds := []MigrationKind{MigrationNeutralize}
			if op.PurgeData {
				kinds = append(kinds, MigrationPurge)
			}
			for _, kind := range kinds {
				migration, ok := manifestMigrationOfKind(module.Manifest, kind)
				if !ok {
					return Plan{}, fmt.Errorf("module %s lacks a %s migration", id, kind)
				}
				drainSnapshot, ok := drainSnapshots[module.RegistryNamespace]
				if !ok {
					return Plan{}, fmt.Errorf("drain snapshot for registry namespace %q is unavailable", module.RegistryNamespace)
				}
				content, err := fs.ReadFile(drainSnapshot.FS, migration.Source)
				if err != nil {
					return Plan{}, fmt.Errorf("read module %s migration %s: %w", id, migration.Source, err)
				}
				if digestBytes(content) != migration.SHA256 {
					return Plan{}, fmt.Errorf("module %s migration %s sha256 mismatch", id, migration.Source)
				}
				if existing, retainedAlready := retained[migration.ID]; retainedAlready {
					// The immutable ID→number mapping is permanent: a drain
					// module removed again after a re-add reuses its ledger
					// entry instead of allocating a duplicate row.
					if existing.SHA256 != migration.SHA256 {
						return Plan{}, fmt.Errorf("drain migration %q payload changed after allocation", migration.ID)
					}
					change, err := classifyOwnedTarget(canonicalRoot, existing.Path, id, migration.Source, DestinationMigration, content, false)
					if err != nil {
						return Plan{}, err
					}
					allocated[id] = append(allocated[id], existing)
					changes = append(changes, change)
					continue
				}
				maxNumber++
				path := fmt.Sprintf("internal/db/migrations/%04d_%s.sql", maxNumber, sanitizeMigrationID(migration.ID))
				for _, other := range currentLock.Modules {
					for _, existing := range other.Migrations {
						if existing.Path == path {
							return Plan{}, fmt.Errorf("drain migration target %q collides with the immutable ledger", path)
						}
					}
					if _, removedAlso := requestSet[other.ID]; !removedAlso && other.Reason != removalTombstoneReason {
						for _, file := range other.Manifest.Files {
							if file.Target == path {
								return Plan{}, fmt.Errorf("drain migration target %q collides with an authored target of %s", path, other.ID)
							}
						}
					}
				}
				if path == "go.mod" || path == "gogogadget.json" || path == "gogogadget.lock.json" {
					return Plan{}, fmt.Errorf("drain migration target %q is reserved", path)
				}
				change, err := classifyOwnedTarget(canonicalRoot, path, id, migration.Source, DestinationMigration, content, false)
				if err != nil {
					return Plan{}, err
				}
				allocated[id] = append(allocated[id], LockedMigration{
					ID: migration.ID, Number: maxNumber, Path: path, SHA256: migration.SHA256,
				})
				changes = append(changes, change)
			}
		}
	}

	remaining := make(map[string]Manifest, len(currentLock.Modules))
	for _, module := range currentLock.Modules {
		if module.Reason != removalTombstoneReason {
			if _, removed := requestSet[module.ID]; !removed {
				remaining[module.ID] = module.Manifest
			}
		}
	}
	remainingSet := make(map[string]struct{}, len(remaining))
	for id := range remaining {
		remainingSet[id] = struct{}{}
	}
	order, err := stableTopologicalOrder(ctx, remainingSet, remaining)
	if err != nil {
		return Plan{}, err
	}
	resolved := append([]string{}, order...)
	for _, module := range currentLock.Modules {
		if module.Reason == removalTombstoneReason {
			order = append(order, module.ID)
		}
	}
	for _, id := range requested {
		order = append(order, id)
	}

	requiredBy := make(map[string][]string, len(currentLock.Modules))
	for _, module := range currentLock.Modules {
		requiredBy[module.ID] = []string{}
	}
	for _, module := range currentLock.Modules {
		if _, removed := requestSet[module.ID]; removed || module.Reason == removalTombstoneReason {
			continue
		}
		for _, requirement := range module.Manifest.Requires {
			requiredBy[requirement.ID] = append(requiredBy[requirement.ID], module.ID)
		}
	}
	for id := range requiredBy {
		sort.Strings(requiredBy[id])
	}

	modules := make([]LockedModule, 0, len(currentLock.Modules))
	for _, module := range currentLock.Modules {
		if _, removed := requestSet[module.ID]; removed {
			tombstone := module
			// A tombstone remembers identity, removal policy, data
			// obligations, and the immutable migration ledger — nothing
			// that could claim namespaces or regenerate wiring for source
			// files that no longer exist.
			tombstone.Manifest.Files = []ManifestFile{}
			tombstone.Manifest.Requires = []Requirement{}
			tombstone.Manifest.Runtime = RuntimeContributions{}
			tombstone.Manifest.Claims = NamespaceClaims{}
			tombstone.Manifest.Environment = []EnvironmentVariable{}
			tombstone.Manifest.Docs = []DocumentationRef{}
			tombstone.Manifest.Tests = TestMetadata{}
			tombstone.Files = []LockedFile{}
			tombstone.Pending = nil
			tombstone.Reason = removalTombstoneReason
			tombstone.RequiredBy = []string{}
			ledger := append([]LockedMigration{}, module.Migrations...)
			known := make(map[string]struct{}, len(ledger))
			for _, migration := range ledger {
				known[migration.ID] = struct{}{}
			}
			for _, migration := range allocated[module.ID] {
				if _, exists := known[migration.ID]; exists {
					continue
				}
				ledger = append(ledger, migration)
			}
			tombstone.Migrations = ledger
			sort.Slice(tombstone.Migrations, func(i, j int) bool {
				return tombstone.Migrations[i].ID < tombstone.Migrations[j].ID
			})
			modules = append(modules, tombstone)
			continue
		}
		kept := module
		kept.RequiredBy = append([]string{}, requiredBy[module.ID]...)
		modules = append(modules, kept)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].ID < modules[j].ID })
	finalLock := Lock{
		Schema: 2, RegistryCommit: registryCommitForModules(modules),
		Registries: append([]LockedRegistry{}, currentLock.Registries...),
		Snapshots:  append([]LockedSnapshot{}, currentLock.Snapshots...),
		Order:      order, RuntimeOrders: currentLock.RuntimeOrders,
		Dependencies: append([]LockedDependency{}, currentLock.Dependencies...),
		Modules:      modules,
	}

	if !equalProjects(project, desired) {
		intentContent, err := MarshalProject(desired)
		if err != nil {
			return Plan{}, fmt.Errorf("marshal removal intent: %w", err)
		}
		intentChange, err := classifyOwnedTarget(
			canonicalRoot, "gogogadget.json", "", "", DestinationIntent, intentContent, true,
		)
		if err != nil {
			return Plan{}, err
		}
		changes = append(changes, intentChange)
	}
	lockContent, err := MarshalLock(finalLock)
	if err != nil {
		return Plan{}, fmt.Errorf("marshal removal lock: %w", err)
	}
	lockChange, err := classifyOwnedTarget(canonicalRoot, "gogogadget.lock.json", "", "", DestinationLock, lockContent, true)
	if err != nil {
		return Plan{}, err
	}
	changes = append(changes, lockChange)

	diagnostics := make([]Diagnostic, 0)
	if op.PurgeData {
		for _, id := range requested {
			if installed[id].Manifest.RemovalPolicy != RemovalDrainRequired {
				diagnostics = append(diagnostics, Diagnostic{
					Code: "purge_not_applicable", Severity: "warn", Module: id,
					Message: "--purge-data has no effect: module is not drain-required",
				})
			}
		}
	}
	conflicts := conflictsFromLock(finalLock)
	sortPlanOutputs(changes, conflicts, nil)
	operation := op
	operation.Modules = append([]string{}, op.Modules...)
	return Plan{
		Operation: operation, Root: canonicalRoot, RegistryCommit: finalLock.RegistryCommit,
		ModulePath: modulePath, Project: desired, Lock: finalLock,
		Resolved: resolved, Order: append([]string{}, order...),
		Changes: changes, Diagnostics: diagnostics, Conflicts: conflicts, Staged: []StagedFile{},
	}, nil
}

func dedupeSorted(values []string) []string {
	result := make([]string, 0, len(values))
	for i, value := range values {
		if i > 0 && values[i-1] == value {
			continue
		}
		result = append(result, value)
	}
	return result
}

func manifestHasMigrationKind(manifest Manifest, kind MigrationKind) bool {
	for _, migration := range manifest.Migrations {
		if migration.Kind == kind {
			return true
		}
	}
	return false
}

func manifestMigrationOfKind(manifest Manifest, kind MigrationKind) (ManifestMigration, bool) {
	for _, migration := range manifest.Migrations {
		if migration.Kind == kind {
			return migration, true
		}
	}
	return ManifestMigration{}, false
}
func equalProjects(left, right Project) bool {
	return strings.Join(left.Modules, "\x00") == strings.Join(right.Modules, "\x00") &&
		strings.Join(left.Exclude, "\x00") == strings.Join(right.Exclude, "\x00") &&
		left.Schema == right.Schema &&
		slices.Equal(left.Registries, right.Registries) &&
		maps.Equal(left.Providers, right.Providers) &&
		left.Deployment == right.Deployment
}

// generatedSiblings names the build output derived from each deleted source.
//
// Only templ sources have one. A plain Go file or a static asset has no
// generated twin, and inventing a path for it would delete a file nobody
// generated. Paths already in the input are skipped so a plan never deletes the
// same file twice.
func generatedSiblings(paths []string) []string {
	present := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		present[path] = struct{}{}
	}
	out := make([]string, 0)
	for _, path := range paths {
		if !strings.HasSuffix(path, ".templ") {
			continue
		}
		generated := strings.TrimSuffix(path, ".templ") + "_templ.go"
		if _, ok := present[generated]; ok {
			continue
		}
		out = append(out, generated)
		present[generated] = struct{}{}
	}
	return out
}
