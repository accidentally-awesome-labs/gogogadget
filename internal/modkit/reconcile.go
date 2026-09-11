package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// conflictArtifactPrefix is the ignored scratch root for staged conflict
// candidates and diffs; lock validation and doctor enforce it.
const conflictArtifactPrefix = "tmp/ggg/conflicts/"

type reconciledModule struct {
	manifest       Manifest
	sourceCommit   string
	snapshotSHA256 string
	files          []LockedFile
	pending        *PendingUpdate
}

// snapshotDigest is the per-module snapshot provenance: a retained module
// keeps the snapshot it was installed from, everything else is pinned to the
// snapshot this plan resolved.
func (m reconciledModule) snapshotDigest(fallback string) string {
	if m.snapshotSHA256 != "" {
		return m.snapshotSHA256
	}
	return fallback
}

type detectedConflict struct {
	module      string
	path        string
	oldFile     LockedFile
	local       []byte
	localDigest string
	upstream    plannedAuthoredPayload
}

func projectAfterAdd(project Project, catalog Catalog, requested []string) (Project, error) {
	if len(requested) == 0 {
		return Project{}, fmt.Errorf("add requires at least one module")
	}
	modules := make(map[string]Manifest, len(catalog.Modules))
	profiles := make(map[string]Profile, len(catalog.Profiles))
	for _, module := range catalog.Modules {
		modules[module.ID] = module
	}
	for _, profile := range catalog.Profiles {
		profiles[profile.ID] = profile
	}

	result := project
	result.Modules = append([]string{}, project.Modules...)
	result.Exclude = append([]string{}, project.Exclude...)
	requested = append([]string{}, requested...)
	sort.Strings(requested)
	for i, id := range requested {
		if i > 0 && requested[i-1] == id {
			continue
		}
		if _, moduleOK := modules[id]; !moduleOK {
			if _, profileOK := profiles[id]; !profileOK {
				return Project{}, fmt.Errorf("add selects unknown catalog id %q", id)
			}
		}
		result.Exclude = removeString(result.Exclude, id)
		if containsString(result.Modules, id) {
			continue
		}
		if _, isProfile := profiles[id]; isProfile {
			result.Modules = append(result.Modules, id)
			continue
		}
		supplied := false
		for _, selectedID := range result.Modules {
			profile, ok := profiles[selectedID]
			if ok && containsString(profile.Members, id) {
				supplied = true
				break
			}
		}
		if !supplied {
			result.Modules = append(result.Modules, id)
		}
	}
	sort.Strings(result.Modules)
	sort.Strings(result.Exclude)
	if _, err := MarshalProject(result); err != nil {
		return Project{}, err
	}
	return result, nil
}

func removeString(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func readPlannedPayloadsFromCatalog(ctx context.Context, catalog Catalog, modules []Manifest, prefixes []string, modulePath string) ([]plannedAuthoredPayload, error) {
	return readPlannedPayloadsWithSources(ctx, func(module Manifest) (fs.FS, string) {
		return catalog.ModuleSources[module.ID], catalog.ModuleCanonical[module.ID]
	}, modules, prefixes, modulePath)
}

// InstallsSelfHostPayloads reports whether a project is the repository that
// publishes a module: its go.mod module path is that registry's
// canonical_module. Only there does a self-host assertion have the artifacts
// it asserts on.
func InstallsSelfHostPayloads(modulePath, canonicalModule string) bool {
	return canonicalModule != "" && modulePath == canonicalModule
}

func readPlannedPayloadsWithSources(
	ctx context.Context,
	source func(Manifest) (fs.FS, string),
	modules []Manifest,
	prefixes []string,
	modulePath string,
) ([]plannedAuthoredPayload, error) {
	payloads := make([]plannedAuthoredPayload, 0)
	for _, module := range modules {
		registryFS, canonicalModule := source(module)
		if registryFS == nil {
			return nil, fmt.Errorf("module %s has no registry source", module.ID)
		}
		selfHost := InstallsSelfHostPayloads(modulePath, canonicalModule)
		for _, file := range module.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if file.Class == FileClassGenerated {
				continue
			}
			// The payload is still read and verified here — a self-host
			// assertion is part of the signed snapshot and a tampered one must
			// refuse everywhere — it simply does not become an install.
			content, err := fs.ReadFile(registryFS, file.Source)
			if err != nil {
				return nil, fmt.Errorf("module %s payload %s: %w", module.ID, file.Source, err)
			}
			if digestBytes(content) != file.SHA256 {
				return nil, fmt.Errorf("module %s payload %s sha256 mismatch", module.ID, file.Source)
			}
			if file.SelfHost && !selfHost {
				continue
			}
			installed := append([]byte(nil), content...)
			if file.RewriteModule {
				installed, err = rewriteModuleImportsForPrefixes(file.Target, content, prefixes, modulePath)
				if err != nil {
					return nil, fmt.Errorf("module %s payload %s: %w", module.ID, file.Source, err)
				}
			}
			payloads = append(payloads, plannedAuthoredPayload{module: module.ID, file: file, content: installed})
		}
	}
	sort.Slice(payloads, func(i, j int) bool {
		if payloads[i].module != payloads[j].module {
			return payloads[i].module < payloads[j].module
		}
		return payloads[i].file.Target < payloads[j].file.Target
	})
	return payloads, nil
}

func reconcilePlannedState(
	ctx context.Context,
	root string,
	snapshot Snapshot,
	registrySources []resolvedRegistry,
	graph selectedGraph,
	payloads []plannedAuthoredPayload,
	existing Lock,
	hasLock bool,
	claims map[string]struct{},
	retire map[string]retirement,
	retained map[string]struct{},
) (Lock, []Change, []Conflict, []Diagnostic, error) {
	// Generated outputs are tool-owned: authored module targets must never
	// claim them, so a manifest that points at a registry-owned artifact is a
	// preflight refusal rather than a silent overwrite. Enforce this before
	// any lock-state branching so fresh installs and reconciled updates fail
	// the same way.
	for _, payload := range payloads {
		if IsGeneratedOutputPath(payload.file.Target) {
			return Lock{}, nil, nil, nil, fmt.Errorf(
				"module %s targets generated output %s; generated outputs are tool-owned and cannot be authored",
				payload.module, payload.file.Target,
			)
		}
	}
	graphOwners := graphFileOwnership(graph.modules)
	if !hasLock {
		ownership := lockedFileOwnership(Lock{}, false)
		files := make(map[string][]LockedFile, len(graph.modules))
		changes := make([]Change, 0, len(payloads))
		for _, module := range graph.modules {
			files[module.ID] = []LockedFile{}
			// A generated output has no distributed bytes to pin: it is recorded
			// so the lock covers every declared target, but with no digests.
			for _, file := range module.Files {
				if file.Class == FileClassGenerated {
					files[module.ID] = append(files[module.ID], LockedFile{
						Path: file.Target, Source: file.Source, State: FileGenerated,
					})
				}
			}
		}
		for _, payload := range payloads {
			upstream := digestBytes(payload.content)

			// Adoption runs against a tree that already has files in it. A
			// pre-existing file that diverges from its payload is unowned: the
			// registry never produced those bytes. Overwriting it would destroy
			// work that predates adoption, and recording it as clean would lie
			// about what is installed, so a claim is required before the
			// ownership classifier ever sees it.
			_, localDigest, missing, stateErr := CurrentTargetState(root, payload.file.Target)
			if stateErr != nil {
				return Lock{}, nil, nil, nil, stateErr
			}
			if !missing && localDigest != upstream {
				if _, claimed := claims[payload.file.Target]; !claimed {
					return Lock{}, nil, nil, nil, fmt.Errorf(
						"adoption blocked: %s already exists with different bytes than %s provides; "+
							"re-run with --claim %s to adopt your version as a recorded modification, "+
							"or delete the file to take the registry copy",
						payload.file.Target, payload.module, payload.file.Target,
					)
				}
				changes = append(changes, Change{
					Path: payload.file.Target, Module: payload.module, Source: payload.file.Source,
					Class: DestinationAuthored, Kind: ChangeUnchanged,
				})
				files[payload.module] = append(files[payload.module], LockedFile{
					Path: payload.file.Target, Source: payload.file.Source,
					BaseSHA256: upstream, LocalSHA256: localDigest, State: FileModified,
				})
				continue
			}

			change, err := classifyAuthoredTarget(root, payload.module, payload.file, payload.content, ownership)
			if err != nil {
				return Lock{}, nil, nil, nil, err
			}
			changes = append(changes, change)
			files[payload.module] = append(files[payload.module], LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: upstream, LocalSHA256: upstream, State: FileClean,
			})
		}
		for module := range files {
			sort.Slice(files[module], func(i, j int) bool { return files[module][i].Path < files[module][j].Path })
		}
		migrations, migrationChanges, err := planMigrations(ctx, root, snapshot.FS, graph.modules, Lock{}, false, nil)
		if err != nil {
			return Lock{}, nil, nil, nil, err
		}
		changes = append(changes, migrationChanges...)
		return buildPlannedLock(snapshot.Commit, graph, files, migrations), changes, []Conflict{}, []Diagnostic{}, nil
	}

	oldModules := make(map[string]LockedModule, len(existing.Modules))
	for _, module := range existing.Modules {
		oldModules[module.ID] = module
	}
	newModules := make(map[string]Manifest, len(graph.modules))
	for _, module := range graph.modules {
		newModules[module.ID] = module
	}
	retiredTombstones := make([]LockedModule, 0, len(retire))
	retiredChanges := make([]Change, 0)
	for id, module := range oldModules {
		if module.Reason == TombstoneReason {
			continue
		}
		if _, selected := newModules[id]; !selected {
			if _, retiring := retire[id]; retiring {
				tombstone, deletions, err := planRetirement(ctx, root, id, module, retire[id])
				if err != nil {
					return Lock{}, nil, nil, nil, err
				}
				retiredTombstones = append(retiredTombstones, *tombstone)
				retiredChanges = append(retiredChanges, deletions...)
				continue
			}
			return Lock{}, nil, nil, nil, fmt.Errorf(
				"module %s is installed but no longer selected; removal planning is required", id,
			)
		}
	}
	payloadByModule := make(map[string]map[string]plannedAuthoredPayload, len(graph.modules))
	for _, payload := range payloads {
		if payloadByModule[payload.module] == nil {
			payloadByModule[payload.module] = make(map[string]plannedAuthoredPayload)
		}
		payloadByModule[payload.module][payload.file.Target] = payload
	}
	detected := make([]detectedConflict, 0)
	for _, module := range graph.modules {
		oldModule, ok := oldModules[module.ID]
		if !ok {
			continue
		}
		oldFiles := lockedFilesByPath(oldModule.Files)
		for _, payload := range payloadsForModule(payloadByModule, module.ID) {
			oldFile, ok := oldFiles[payload.file.Target]
			if !ok {
				continue
			}
			local, localDigest, missing, err := CurrentTargetState(root, payload.file.Target)
			if err != nil {
				return Lock{}, nil, nil, nil, err
			}
			if missing {
				continue
			}
			upstreamDigest := digestBytes(payload.content)
			if localDigest != oldFile.BaseSHA256 && upstreamDigest != oldFile.BaseSHA256 && localDigest != upstreamDigest {
				detected = append(detected, detectedConflict{
					module: module.ID, path: payload.file.Target, oldFile: oldFile,
					local: local, localDigest: localDigest, upstream: payload,
				})
			}
		}
	}
	sort.Slice(detected, func(i, j int) bool {
		if detected[i].module != detected[j].module {
			return detected[i].module < detected[j].module
		}
		return detected[i].path < detected[j].path
	})

	hold := heldModules(detected, graph.modules, oldModules, newModules)
	runID := conflictRunID(snapshot.Commit, detected)
	conflicts, pendingConflicts, staged, err := buildConflictArtifacts(root, runID, detected)
	if err != nil {
		return Lock{}, nil, nil, nil, err
	}
	directConflicts := make(map[string]map[string]struct{})
	for _, conflict := range detected {
		if directConflicts[conflict.module] == nil {
			directConflicts[conflict.module] = make(map[string]struct{})
		}
		directConflicts[conflict.module][conflict.path] = struct{}{}
	}

	states := make(map[string]reconciledModule, len(graph.modules))
	changes := make([]Change, 0, len(payloads))
	diagnostics := make([]Diagnostic, 0)
	ownership := applyOwnershipTransfers(lockedFileOwnership(existing, true), graphOwners)
	for _, module := range graph.modules {
		if err := ctx.Err(); err != nil {
			return Lock{}, nil, nil, nil, err
		}
		if _, keep := retained[module.ID]; keep {
			// A targeted update leaves this module at its prior per-module
			// snapshot: the lock row — manifest, file digests, source
			// provenance — carries forward verbatim and no payload is read,
			// so no byte of its tree can move. Its migrations were already
			// applied and are carried by planMigrations from the lock.
			oldModule := oldModules[module.ID]
			states[module.ID] = reconciledModule{
				manifest: oldModule.Manifest, sourceCommit: oldModule.SourceCommit,
				snapshotSHA256: oldModule.SnapshotSHA256, files: append([]LockedFile{}, oldModule.Files...),
			}
			continue
		}
		oldModule, hadOld := oldModules[module.ID]
		if _, held := hold[module.ID]; held && !hadOld {
			return Lock{}, nil, nil, nil, fmt.Errorf(
				"module %s cannot be installed while a required module is conflicted", module.ID,
			)
		} else if held {
			lockedFiles := make([]LockedFile, 0, len(oldModule.Files))
			for _, oldFile := range oldModule.Files {
				// A generated target has no canonical bytes and no local
				// digest to compare: its row is state-only, and restating it
				// through the clean/modified branch below would attach the
				// on-disk render's digest to an empty base — a planned lock
				// that fails its own validation (`base_sha256 is invalid`)
				// and kills the promised exit-4 staging with exit 3 before
				// anything is staged (finding P0-3: every core-module
				// conflict on the v0.22→v0.23 pair died exactly here,
				// because the held modules behind it own generated outputs).
				// Generated rows carry forward verbatim, the same way the
				// retained branch above carries whole rows.
				if oldFile.State == FileGenerated {
					lockedFiles = append(lockedFiles, LockedFile{
						Path: oldFile.Path, Source: oldFile.Source, State: FileGenerated,
					})
					continue
				}
				_, localDigest, missing, err := CurrentTargetState(root, oldFile.Path)
				if err != nil {
					return Lock{}, nil, nil, nil, err
				}
				if _, conflicted := directConflicts[module.ID][oldFile.Path]; conflicted {
					if missing {
						return Lock{}, nil, nil, nil, fmt.Errorf(
							"conflicted file %s of module %s is missing; restore it before resolving", oldFile.Path, module.ID,
						)
					}
					lockedFiles = append(lockedFiles, LockedFile{
						Path: oldFile.Path, Source: oldFile.Source, BaseSHA256: oldFile.BaseSHA256,
						LocalSHA256: localDigest, State: FileConflicted,
					})
					continue
				}
				if missing {
					diagnostics = append(diagnostics, Diagnostic{
						Code: "file_missing", Severity: "warn", Module: module.ID, Path: oldFile.Path,
						Message: "held module file is missing locally and will not be restored until its conflict clears",
					})
					lockedFiles = append(lockedFiles, LockedFile{
						Path: oldFile.Path, Source: oldFile.Source, BaseSHA256: oldFile.BaseSHA256,
						LocalSHA256: "", State: FileMissing,
					})
					continue
				}
				state := FileClean
				if localDigest != oldFile.BaseSHA256 {
					state = FileModified
				}
				lockedFiles = append(lockedFiles, LockedFile{
					Path: oldFile.Path, Source: oldFile.Source, BaseSHA256: oldFile.BaseSHA256,
					LocalSHA256: localDigest, State: state,
				})
			}
			// The pending candidate is pinned to the registry that PUBLISHES
			// this module, never to whichever registry happens to sit first in
			// the configured list. A conflicted module in the second registry
			// once recorded the first registry's commit here, and `resolve`
			// then wrote that foreign commit — and its snapshot digest — into
			// the module's own lock row: the provenance ledger named a cache
			// entry the module's bytes never came from, which is the one lie
			// the ledger exists to prevent.
			ownSnapshot := snapshot
			for _, source := range registrySources {
				if source.config.Namespace == moduleNamespace(module.ID) {
					ownSnapshot = source.snapshot
				}
			}
			pending := &PendingUpdate{
				RunID: runID, RegistryCommit: ownSnapshot.Commit, SourceCommit: ownSnapshot.Commit,
				Manifest: module, Conflicts: append([]PendingConflict{}, pendingConflicts[module.ID]...),
			}
			// While the conflict stands the installed bytes are still the ones
			// the OLD snapshot pinned, so the row keeps that provenance — the
			// same carry-forward the retained branch above applies verbatim.
			// `resolve` moves both fields to the pending snapshot once the
			// operator has decided.
			states[module.ID] = reconciledModule{
				manifest: oldModule.Manifest, sourceCommit: oldModule.SourceCommit,
				snapshotSHA256: oldModule.SnapshotSHA256, files: lockedFiles, pending: pending,
			}
			continue
		}
		oldFiles := map[string]LockedFile{}
		if hadOld {
			oldFiles = lockedFilesByPath(oldModule.Files)
			// What this plan keeps is what it installs, not everything the
			// manifest declares: a self-host payload a previous install of the
			// publishing repository left behind leaves with the plan that stops
			// installing it, instead of lingering unowned by the lock.
			newTargets := make(map[string]struct{}, len(module.Files))
			for _, file := range module.Files {
				if file.Class == FileClassGenerated {
					newTargets[file.Target] = struct{}{}
				}
			}
			for target := range payloadByModule[module.ID] {
				newTargets[target] = struct{}{}
			}
			for _, oldFile := range oldModule.Files {
				if _, exists := newTargets[oldFile.Path]; exists {
					continue
				}
				if next, claimed := graphOwners[oldFile.Path]; claimed && next != module.ID {
					// Ownership moved, the file did not. Deleting it here would
					// race the new owner's write in the same plan, and refusing
					// on a local modification here would report the wrong
					// module: the new owner's classifier sees the same base
					// digest and still refuses.
					continue
				}
				_, digest, missing, err := CurrentTargetState(root, oldFile.Path)
				if err != nil {
					return Lock{}, nil, nil, nil, err
				}
				if missing {
					continue
				}
				if digest != oldFile.BaseSHA256 {
					return Lock{}, nil, nil, nil, fmt.Errorf(
						"module %s dropped file %s with local modifications; removal planning is required", module.ID, oldFile.Path,
					)
				}
				changes = append(changes, Change{
					Path: oldFile.Path, Module: module.ID, Source: oldFile.Source,
					Kind: ChangeDelete, Class: DestinationAuthored, SHA256: oldFile.BaseSHA256,
				})
			}
		}
		lockedFiles := make([]LockedFile, 0, len(module.Files))
		// Generated targets carry no payload: recorded so the lock covers every
		// declared target, with no digests to compare.
		for _, file := range module.Files {
			if file.Class == FileClassGenerated {
				lockedFiles = append(lockedFiles, LockedFile{
					Path: file.Target, Source: file.Source, State: FileGenerated,
				})
			}
		}
		for _, payload := range payloadsForModule(payloadByModule, module.ID) {
			newDigest := digestBytes(payload.content)
			if oldFile, ok := oldFiles[payload.file.Target]; ok {
				_, localDigest, missing, err := CurrentTargetState(root, payload.file.Target)
				if err != nil {
					return Lock{}, nil, nil, nil, err
				}
				if !missing {
					if localDigest == newDigest {
						lockedFiles = append(lockedFiles, LockedFile{
							Path: payload.file.Target, Source: payload.file.Source,
							BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
						})
						continue
					}
					if localDigest != oldFile.BaseSHA256 && newDigest == oldFile.BaseSHA256 {
						lockedFiles = append(lockedFiles, LockedFile{
							Path: payload.file.Target, Source: payload.file.Source,
							BaseSHA256: oldFile.BaseSHA256, LocalSHA256: localDigest, State: FileModified,
						})
						continue
					}
				}
			}
			change, err := classifyAuthoredTarget(root, module.ID, payload.file, payload.content, ownership)
			if err != nil {
				return Lock{}, nil, nil, nil, err
			}
			changes = append(changes, change)
			lockedFiles = append(lockedFiles, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
			})
		}
		states[module.ID] = reconciledModule{
			manifest: module, sourceCommit: snapshot.Commit, files: lockedFiles,
		}
	}

	effectiveModules := make(map[string]Manifest, len(states))
	selected := make(map[string]struct{}, len(states))
	for id, state := range states {
		effectiveModules[id] = state.manifest
		selected[id] = struct{}{}
	}
	order, err := stableTopologicalOrder(ctx, selected, effectiveModules)
	if err != nil {
		return Lock{}, nil, nil, nil, err
	}
	effectiveList := make([]Manifest, 0, len(order))
	for _, id := range order {
		effectiveList = append(effectiveList, effectiveModules[id])
	}
	migrationFiles, migrationChanges, err := planMigrations(ctx, root, snapshot.FS, effectiveList, existing, true, retained)
	if err != nil {
		return Lock{}, nil, nil, nil, err
	}
	changes = append(changes, migrationChanges...)
	changes = append(changes, retiredChanges...)

	finalGraph := selectedGraph{modules: effectiveList, order: order, reasons: graph.reasons}
	finalLock := buildReconciledLock(snapshot.Commit, finalGraph, states, migrationFiles)
	// Tombstones of previously removed modules are permanent ledger rows:
	// carry them forward verbatim so their immutable migration mappings are
	// never lost and re-adds reuse the retained numbers.
	for _, module := range existing.Modules {
		if module.Reason != TombstoneReason {
			continue
		}
		if _, selectedAgain := newModules[module.ID]; selectedAgain {
			continue
		}
		finalLock.Modules = append(finalLock.Modules, module)
		finalLock.Order = append(finalLock.Order, module.ID)
	}
	for _, tombstone := range retiredTombstones {
		finalLock.Modules = append(finalLock.Modules, tombstone)
		finalLock.Order = append(finalLock.Order, tombstone.ID)
	}
	sort.Slice(finalLock.Modules, func(i, j int) bool { return finalLock.Modules[i].ID < finalLock.Modules[j].ID })
	// The staged artifacts ride with every other planned write: one journal,
	// one rollback, one `changes[]` the operator can read before applying.
	changes = append(changes, staged...)
	return finalLock, changes, conflicts, diagnostics, nil
}

func lockedFilesByPath(files []LockedFile) map[string]LockedFile {
	result := make(map[string]LockedFile, len(files))
	for _, file := range files {
		result[file.Path] = file
	}
	return result
}

func payloadsForModule(payloads map[string]map[string]plannedAuthoredPayload, module string) []plannedAuthoredPayload {
	byPath := payloads[module]
	result := make([]plannedAuthoredPayload, 0, len(byPath))
	for _, payload := range byPath {
		result = append(result, payload)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].file.Target < result[j].file.Target })
	return result
}

func readCurrentTarget(root, target string) ([]byte, string, error) {
	content, digest, missing, err := CurrentTargetState(root, target)
	if err != nil {
		return nil, "", err
	}
	if missing {
		return nil, "", fmt.Errorf("owned target %s is missing", target)
	}
	return content, digest, nil
}

func CurrentTargetState(root, target string) (content []byte, digest string, missing bool, err error) {
	info, isMissing, err := lstatProjectPath(root, target)
	if err != nil {
		return nil, "", false, fmt.Errorf("target %s: %w", target, err)
	}
	if isMissing {
		return nil, "", true, nil
	}
	if !info.Mode().IsRegular() {
		return nil, "", false, fmt.Errorf("owned target %s is not a regular file", target)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return nil, "", false, fmt.Errorf("read owned target %s: %w", target, err)
	}
	return data, digestBytes(data), false, nil
}

func heldModules(conflicts []detectedConflict, modules []Manifest, old map[string]LockedModule, current map[string]Manifest) map[string]struct{} {
	hold := make(map[string]struct{})
	reverse := make(map[string][]string)
	for _, module := range modules {
		for _, requirement := range module.Requires {
			reverse[requirement.ID] = append(reverse[requirement.ID], module.ID)
		}
	}
	queue := make([]string, 0)
	for _, conflict := range conflicts {
		if _, exists := hold[conflict.module]; !exists {
			hold[conflict.module] = struct{}{}
			queue = append(queue, conflict.module)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[id] {
			if _, exists := hold[dependent]; exists {
				continue
			}
			hold[dependent] = struct{}{}
			queue = append(queue, dependent)
			sort.Strings(queue)
		}
		oldManifest, oldOK := old[id]
		newManifest, newOK := current[id]
		if !oldOK || !newOK || oldManifest.Contract == newManifest.Contract {
			continue
		}
		for _, requirement := range newManifest.Requires {
			dependency := requirement.ID
			oldDependency, oldOK := old[dependency]
			newDependency, newOK := current[dependency]
			if oldOK && newOK && oldDependency.Contract != newDependency.Contract {
				if _, exists := hold[dependency]; !exists {
					hold[dependency] = struct{}{}
					queue = append(queue, dependency)
					sort.Strings(queue)
				}
			}
		}
	}
	return hold
}

func conflictRunID(newCommit string, conflicts []detectedConflict) string {
	var input strings.Builder
	input.WriteString(newCommit)
	for _, conflict := range conflicts {
		input.WriteByte(0)
		input.WriteString(conflict.module)
		input.WriteByte(0)
		input.WriteString(conflict.path)
		input.WriteByte(0)
		input.WriteString(conflict.oldFile.BaseSHA256)
	}
	return digestBytes([]byte(input.String()))[:16]
}

// buildConflictArtifacts derives the conflict record, the lock's pending
// metadata, and the two staged artifacts per conflict. The artifacts come back
// as ordinary plan changes: Apply is the only thing in this engine that puts
// bytes on disk, and a candidate nothing writes is a recovery path that does
// not exist. Classification runs through classifyOwnedTarget like every other
// tool-owned destination, so a repeated update over unchanged artifacts is
// `unchanged` rather than a rewrite, and a symlink or a directory at one of
// those paths refuses before the transaction opens.
func buildConflictArtifacts(root, runID string, detected []detectedConflict) ([]Conflict, map[string][]PendingConflict, []Change, error) {
	conflicts := make([]Conflict, 0, len(detected))
	pending := make(map[string][]PendingConflict)
	staged := make([]Change, 0, len(detected)*2)
	for _, item := range detected {
		pathHash := digestBytes([]byte(item.path))[:10]
		moduleDir := strings.ReplaceAll(item.module, "/", "-")
		base := pathpkg.Base(item.path)
		prefix := conflictArtifactPrefix + runID + "/" + moduleDir + "/" + pathHash + "-" + base
		candidatePath := prefix + ".candidate"
		diffPath := prefix + ".diff"
		upstreamDigest := digestBytes(item.upstream.content)
		diff := unifiedConflictDiff(item.path, item.local, item.upstream.content)
		conflict := Conflict{
			Module: item.module, Path: item.path, BaseSHA256: item.oldFile.BaseSHA256,
			LocalSHA256: item.localDigest, UpstreamSHA256: upstreamDigest,
			CandidatePath: candidatePath, DiffPath: diffPath,
		}
		conflicts = append(conflicts, conflict)
		pending[item.module] = append(pending[item.module], PendingConflict{
			Path: item.path, CandidateSHA256: upstreamDigest,
			CandidatePath: candidatePath, DiffPath: diffPath,
		})
		// Source is the conflicted target: it is what the artifact is a copy
		// of, and it is what makes the plan line readable.
		candidateChange, err := classifyOwnedTarget(
			root, candidatePath, item.module, item.path, DestinationStaged, item.upstream.content, true,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		diffChange, err := classifyOwnedTarget(
			root, diffPath, item.module, item.path, DestinationStaged, diff, true,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		staged = append(staged, candidateChange, diffChange)
	}
	return conflicts, pending, staged, nil
}

// unifiedConflictDiff renders the operator-facing delta between local and
// upstream bytes as a real unified diff: matching lines collapse into context,
// so a two-line change reads as two lines. Its whole job is to be the thing
// the docs tell the operator to read before choosing --keep-local, and the
// whole-file rewrite this replaces opened @@ -1,223 +1,223 @@ with every line
// marked both ways on a delta that `diff` reports in eight lines — noise that
// made the one decision artifact indistinguishable from a rewrite.
func unifiedConflictDiff(target string, local, upstream []byte) []byte {
	if !utf8.Valid(local) || !utf8.Valid(upstream) {
		return []byte("Binary conflict for " + target + "; inspect the complete .candidate file.\n")
	}
	oldLines, oldFinalNewline := splitDiffLines(string(local))
	newLines, newFinalNewline := splitDiffLines(string(upstream))
	ops := lcsDiffOps(oldLines, newLines)
	if ops == nil {
		return wholeFileConflictDiff(target, oldLines, newLines, oldFinalNewline, newFinalNewline)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", target, target)
	changeAt := make([]int, 0, len(ops))
	for k, op := range ops {
		if op.kind != '=' {
			changeAt = append(changeAt, k)
		}
	}
	if len(changeAt) == 0 {
		return []byte(out.String())
	}
	for hunkStart := 0; hunkStart < len(changeAt); {
		// A hunk spans one change plus diffContextLines of context on each
		// side; consecutive changes whose contexts touch or overlap are one
		// hunk, which is the merge rule diff(1) itself uses.
		hunkEnd := hunkStart
		first := max(0, changeAt[hunkStart]-diffContextLines)
		last := min(len(ops)-1, changeAt[hunkStart]+diffContextLines)
		for hunkEnd+1 < len(changeAt) && changeAt[hunkEnd+1]-diffContextLines <= last+1 {
			hunkEnd++
			last = min(len(ops)-1, changeAt[hunkEnd]+diffContextLines)
		}
		emitConflictHunk(&out, ops, first, last,
			oldLines, newLines, oldFinalNewline, newFinalNewline)
		hunkStart = hunkEnd + 1
	}
	return []byte(out.String())
}

// wholeFileConflictDiff is the fallback for a pair of files too large to diff
// pairwise: one hunk, every old line minus, every new line plus. It states
// what it is, because a reader must be able to tell a rewrite-diff from a
// real one.
func wholeFileConflictDiff(target string, oldLines, newLines []string, oldFinalNewline, newFinalNewline bool) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n",
		target, target, len(oldLines), len(newLines))
	for i, line := range oldLines {
		out.WriteByte('-')
		out.WriteString(line)
		out.WriteByte('\n')
		if i == len(oldLines)-1 && !oldFinalNewline {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	for i, line := range newLines {
		out.WriteByte('+')
		out.WriteString(line)
		out.WriteByte('\n')
		if i == len(newLines)-1 && !newFinalNewline {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	out.WriteString("# whole-file fallback: too many lines to diff pairwise; compare against the .candidate\n")
	return []byte(out.String())
}

const (
	// diffContextLines is the unified-diff context window, matching diff(1).
	diffContextLines = 3
	// diffLineBudget caps each side of the pairwise LCS: the matrix is
	// (n+1)*(m+1) cells, and a conflict between two files larger than this
	// falls back to the whole-file form rather than allocating hundreds of
	// megabytes inside a plan.
	diffLineBudget = 20_000
)

// diffOp is one line of the edit script: '=' shared, '-' local-only, '+'
// upstream-only. The indexes name the line on their side; an op of the other
// kind carries the paired position at emission time.
type diffOp struct {
	kind           byte
	oldIdx, newIdx int
}

// lcsDiffOps computes the edit script between two line slices by
// longest-common-subsequence, preferring deletions before insertions so runs
// read as "the old lines, then their replacements". nil means the pair
// exceeded diffLineBudget and the caller must fall back.
func lcsDiffOps(oldLines, newLines []string) []diffOp {
	n, m := len(oldLines), len(newLines)
	if n > diffLineBudget || m > diffLineBudget {
		return nil
	}
	// suffix[i][j] = LCS length of oldLines[i:] and newLines[j:], kept as one
	// flat array so a large pair is one allocation, not n slices.
	suffix := make([]int32, (n+1)*(m+1))
	stride := m + 1
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				suffix[i*stride+j] = suffix[(i+1)*stride+j+1] + 1
			case suffix[(i+1)*stride+j] >= suffix[i*stride+j+1]:
				suffix[i*stride+j] = suffix[(i+1)*stride+j]
			default:
				suffix[i*stride+j] = suffix[i*stride+j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, diffOp{kind: '=', oldIdx: i, newIdx: j})
			i++
			j++
		case suffix[(i+1)*stride+j] >= suffix[i*stride+j+1]:
			ops = append(ops, diffOp{kind: '-', oldIdx: i, newIdx: j})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', oldIdx: i, newIdx: j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: '-', oldIdx: i, newIdx: j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: '+', oldIdx: i, newIdx: j})
	}
	return ops
}

// emitConflictHunk writes ops[first:last] with the standard @@ header. The
// counts are of the lines the hunk touches on each side, and a count of zero
// is printed against the line BEFORE the hunk, as diff(1) does.
func emitConflictHunk(out *strings.Builder, ops []diffOp, first, last int,
	oldLines, newLines []string, oldFinalNewline, newFinalNewline bool) {
	oldCount, newCount := 0, 0
	for _, op := range ops[first : last+1] {
		switch op.kind {
		case '=', '-':
			oldCount++
		}
		switch op.kind {
		case '=', '+':
			newCount++
		}
	}
	oldStart, newStart := 1, 1
	if ops[first].kind == '+' {
		oldStart = ops[first].oldIdx // the line before the insertion, 0-based
	} else {
		oldStart = ops[first].oldIdx + 1
	}
	if ops[first].kind == '-' {
		newStart = ops[first].newIdx
	} else {
		newStart = ops[first].newIdx + 1
	}
	fmt.Fprintf(out, "@@ -%s +%s @@\n", diffRange(oldStart, oldCount), diffRange(newStart, newCount))
	for _, op := range ops[first : last+1] {
		switch op.kind {
		case '=':
			out.WriteByte(' ')
			out.WriteString(oldLines[op.oldIdx])
			out.WriteByte('\n')
			if op.oldIdx == len(oldLines)-1 && !oldFinalNewline {
				out.WriteString("\\ No newline at end of file\n")
			}
		case '-':
			out.WriteByte('-')
			out.WriteString(oldLines[op.oldIdx])
			out.WriteByte('\n')
			if op.oldIdx == len(oldLines)-1 && !oldFinalNewline {
				out.WriteString("\\ No newline at end of file\n")
			}
		case '+':
			out.WriteByte('+')
			out.WriteString(newLines[op.newIdx])
			out.WriteByte('\n')
			if op.newIdx == len(newLines)-1 && !newFinalNewline {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
	}
}

// diffRange renders one side of a hunk header: a lone line number when the
// count is one, "start,count" otherwise.
func diffRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func splitDiffLines(value string) ([]string, bool) {
	hasFinalNewline := strings.HasSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return []string{}, hasFinalNewline
	}
	return strings.Split(value, "\n"), hasFinalNewline
}

func buildReconciledLock(commit string, graph selectedGraph, states map[string]reconciledModule, migrations map[string][]LockedMigration) Lock {
	requiredBy := make(map[string][]string, len(graph.modules))
	for _, module := range graph.modules {
		requiredBy[module.ID] = []string{}
	}
	for _, module := range graph.modules {
		for _, requirement := range module.Requires {
			requiredBy[requirement.ID] = append(requiredBy[requirement.ID], module.ID)
		}
	}
	for id := range requiredBy {
		sort.Strings(requiredBy[id])
	}
	modules := append([]Manifest{}, graph.modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].ID < modules[j].ID })
	locked := make([]LockedModule, 0, len(modules))
	for _, module := range modules {
		state := states[module.ID]
		locked = append(locked, LockedModule{
			ID: module.ID, Revision: state.manifest.Revision, Contract: state.manifest.Contract,
			RegistryNamespace: moduleNamespace(module.ID), SourceCommit: state.sourceCommit,
			SnapshotSHA256: state.snapshotDigest(commit), Reason: graph.reasons[module.ID],
			RequiredBy: append([]string{}, requiredBy[module.ID]...), Manifest: state.manifest,
			Files: append([]LockedFile{}, state.files...), Migrations: append([]LockedMigration{}, migrations[module.ID]...),
			Pending: state.pending,
		})
	}
	return Lock{Schema: 2, RegistryCommit: commit, Registries: []LockedRegistry{}, Snapshots: []LockedSnapshot{},
		Order: append([]string{}, graph.order...), RuntimeOrders: RuntimeOrders{
			Development: append([]string{}, graph.order...), Test: append([]string{}, graph.order...), Production: append([]string{}, graph.order...),
		}, GoTools: []string{}, Dependencies: []LockedDependency{}, Modules: locked}
}

func sortPlanOutputs(changes []Change, conflicts []Conflict) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		if changes[i].Module != changes[j].Module {
			return changes[i].Module < changes[j].Module
		}
		return changes[i].Source < changes[j].Source
	})
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Module != conflicts[j].Module {
			return conflicts[i].Module < conflicts[j].Module
		}
		return conflicts[i].Path < conflicts[j].Path
	})
}

// retirement names what is replacing a module this plan itself deselects, so
// its refusal can name the remedy that actually applies. A replaced
// deployment and a deselected provider adapter block on the same
// locally-modified file, but only one of them is a `deployment set` away from
// resolved — a provider refusal that names the deployment sends the operator
// hunting for a command that cannot help.
type retirement struct {
	deployment bool
	slot       string
}

// planRetirement is the in-transaction removal of a module this plan itself
// deselected — the deployment module a `deployment set` replacement leaves
// behind, or an adapter a `provider set` selection stops naming. It deletes
// the module's authored files (whose bytes must match the lock: a modified
// file is a human decision, not a side effect) and hands back the tombstone
// that preserves identity and any migration ledger.
func planRetirement(ctx context.Context, root, id string, module LockedModule, replaced retirement) (*LockedModule, []Change, error) {
	what := fmt.Sprintf("provider selection for slot %s", replaced.slot)
	if replaced.deployment {
		what = "deployment"
	}
	changes := make([]Change, 0)
	for _, file := range module.Files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		_, digest, missing, err := CurrentTargetState(root, file.Path)
		if err != nil {
			return nil, nil, err
		}
		if missing {
			return nil, nil, fmt.Errorf("owned file %s of module %s is missing; restore it before replacing the %s", file.Path, id, what)
		}
		if digest != file.BaseSHA256 {
			return nil, nil, fmt.Errorf(
				"module %s owns locally modified file %s; run ggg diff %s and revert or back up the customization before replacing the %s",
				id, file.Path, id, what)
		}
		changes = append(changes, Change{
			Path: file.Path, Module: id, Source: file.Source,
			Kind: ChangeDelete, Class: DestinationAuthored, SHA256: file.BaseSHA256,
		})
	}
	declared := make([]string, 0, len(changes))
	for _, change := range changes {
		declared = append(declared, change.Path)
	}
	owned := make([]string, 0, len(module.Files))
	for _, file := range module.Files {
		owned = append(owned, file.Path)
	}
	for _, path := range generatedSiblings(owned) {
		if slices.Contains(declared, path) {
			continue
		}
		_, digest, missing, err := CurrentTargetState(root, path)
		if err != nil {
			return nil, nil, err
		}
		if missing {
			continue
		}
		changes = append(changes, Change{
			Path: path, Module: id, Kind: ChangeDelete, Class: DestinationGenerated, SHA256: digest,
		})
		declared = append(declared, path)
	}
	tombstone := module
	tombstone.Manifest.Files = []ManifestFile{}
	tombstone.Manifest.Requires = []Requirement{}
	tombstone.Manifest.Runtime = RuntimeContributions{}
	tombstone.Manifest.Claims = NamespaceClaims{}
	tombstone.Manifest.Environment = []EnvironmentVariable{}
	tombstone.Manifest.Docs = []DocumentationRef{}
	tombstone.Manifest.Tests = TestMetadata{}
	tombstone.Files = []LockedFile{}
	tombstone.Pending = nil
	tombstone.Reason = TombstoneReason
	tombstone.RequiredBy = []string{}
	return &tombstone, changes, nil
}
