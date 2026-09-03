package modkit

import (
	"context"
	"fmt"
	"reflect"
	"sort"
)

// ResolveConflict plans one explicit resolution against verified pending metadata.
func (e *Engine) ResolveConflict(ctx context.Context, root, moduleID, targetPath string, mode ResolutionMode) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if e == nil || e.source == nil {
		return Plan{}, fmt.Errorf("planner source is required")
	}
	switch mode {
	case ResolutionAcceptUpstream, ResolutionKeepLocal, ResolutionMerged:
	default:
		return Plan{}, fmt.Errorf("resolution mode %q is invalid", mode)
	}
	canonicalRoot, err := canonicalProjectRoot(root)
	if err != nil {
		return Plan{}, err
	}
	project, modulePath, currentLock, hasLock, err := readPlannerInputs(canonicalRoot)
	if err != nil {
		return Plan{}, err
	}
	if !hasLock {
		return Plan{}, fmt.Errorf("resolve conflict: gogogadget.lock.json is missing")
	}

	moduleIndex := -1
	conflictIndex := -1
	var pendingConflict PendingConflict
	for i := range currentLock.Modules {
		module := &currentLock.Modules[i]
		if module.ID != moduleID {
			continue
		}
		moduleIndex = i
		if module.Pending == nil {
			return Plan{}, fmt.Errorf("module %s has no pending update", moduleID)
		}
		for j, conflict := range module.Pending.Conflicts {
			if conflict.Path == targetPath {
				conflictIndex = j
				pendingConflict = conflict
				break
			}
		}
		break
	}
	if moduleIndex < 0 {
		return Plan{}, fmt.Errorf("module %s is not installed", moduleID)
	}
	if conflictIndex < 0 {
		return Plan{}, fmt.Errorf("module %s has no pending conflict for %s", moduleID, targetPath)
	}

	candidate, candidateDigest, candidateMissing, err := CurrentTargetState(canonicalRoot, pendingConflict.CandidatePath)
	if err != nil {
		return Plan{}, fmt.Errorf("read conflict candidate: %w", err)
	}
	if candidateMissing {
		return Plan{}, fmt.Errorf(
			"conflict candidate %s is missing; run ggg update at registry commit %s to re-materialize it",
			pendingConflict.CandidatePath, currentLock.Modules[moduleIndex].Pending.RegistryCommit,
		)
	}
	if candidateDigest != pendingConflict.CandidateSHA256 {
		return Plan{}, fmt.Errorf("conflict candidate %s sha256 mismatch", pendingConflict.CandidatePath)
	}
	_, localDigest, localMissing, err := CurrentTargetState(canonicalRoot, targetPath)
	if err != nil {
		return Plan{}, err
	}
	if localMissing && mode != ResolutionAcceptUpstream {
		return Plan{}, fmt.Errorf("cannot keep local bytes for %s: file is missing", targetPath)
	}

	pending := currentLock.Modules[moduleIndex].Pending
	resolved := make([]resolvedRegistry, 0, len(project.Registries))
	for _, registry := range project.Registries {
		if registry.Namespace == currentLock.Modules[moduleIndex].RegistryNamespace && pending.SourceCommit != "" {
			registry.Ref = pending.SourceCommit
		}
		snapshot, err := e.source.Resolve(ctx, registry)
		if err != nil {
			return Plan{}, fmt.Errorf("resolve pending registry commit %s: %w", pending.RegistryCommit, err)
		}
		resolved = append(resolved, resolvedRegistry{config: registry, snapshot: snapshot})
	}
	catalog, err := mergeResolvedCatalogs(ctx, resolved)
	if err != nil {
		return Plan{}, fmt.Errorf("load pending registry catalog: %w", err)
	}
	var targetManifest Manifest
	for _, manifest := range catalog.Modules {
		if manifest.ID == moduleID {
			targetManifest = manifest
			break
		}
	}
	if targetManifest.ID == "" || !reflect.DeepEqual(targetManifest, pending.Manifest) {
		return Plan{}, fmt.Errorf("pending manifest for %s does not match registry commit", moduleID)
	}

	finalLock, err := cloneLock(currentLock)
	if err != nil {
		return Plan{}, fmt.Errorf("clone conflict lock: %w", err)
	}
	targetFS := catalog.ModuleSources[moduleID]
	if targetFS == nil {
		return Plan{}, fmt.Errorf("pending module %s has no source filesystem", moduleID)
	}
	module := &finalLock.Modules[moduleIndex]
	remaining := append([]PendingConflict{}, module.Pending.Conflicts[:conflictIndex]...)
	remaining = append(remaining, module.Pending.Conflicts[conflictIndex+1:]...)
	targetPayloads, err := readPlannedPayloadsFromCatalog(
		ctx, catalog, []Manifest{targetManifest}, canonicalPrefixes(catalog), modulePath,
	)
	if err != nil {
		return Plan{}, err
	}
	changes, err := resolveModuleFiles(
		canonicalRoot, currentLock, module, targetManifest, targetPayloads,
		targetPath, pendingConflict, candidate, candidateDigest, localDigest, localMissing, mode, remaining,
	)
	if err != nil {
		return Plan{}, err
	}
	if len(remaining) == 0 {
		module.Manifest = targetManifest
		module.Revision = targetManifest.Revision
		module.Contract = targetManifest.Contract
		module.SourceCommit = pending.SourceCommit
		module.Pending = nil
		migrationFiles, migrationChanges, err := planMigrations(
			ctx, canonicalRoot, targetFS, []Manifest{targetManifest}, currentLock, true, nil,
		)
		if err != nil {
			return Plan{}, fmt.Errorf("plan resolved migrations: %w", err)
		}
		module.Migrations = append([]LockedMigration{}, migrationFiles[moduleID]...)
		changes = append(changes, migrationChanges...)
		if err := recomputeLockGraph(ctx, &finalLock); err != nil {
			return Plan{}, fmt.Errorf("recompute resolved lock graph: %w", err)
		}
	} else {
		module.Pending.Conflicts = remaining
	}

	lockContent, err := MarshalLock(finalLock)
	if err != nil {
		return Plan{}, fmt.Errorf("marshal resolved lock: %w", err)
	}
	lockChange, err := classifyOwnedTarget(canonicalRoot, "gogogadget.lock.json", "", "", DestinationLock, lockContent, true)
	if err != nil {
		return Plan{}, err
	}
	changes = append(changes, lockChange)

	conflicts := conflictsFromLock(finalLock)
	sortPlanOutputs(changes, conflicts, nil)
	return Plan{
		Operation: Operation{Kind: OpSync}, Root: canonicalRoot,
		RegistryCommit: finalLock.RegistryCommit, ModulePath: modulePath,
		Project: project, Lock: finalLock,
		Resolved: liveModuleOrder(finalLock), Order: append([]string{}, finalLock.Order...),
		Changes: changes, Diagnostics: []Diagnostic{}, Conflicts: conflicts, Staged: []StagedFile{},
	}, nil
}

func liveModuleOrder(lock Lock) []string {
	reasons := make(map[string]string, len(lock.Modules))
	for _, module := range lock.Modules {
		reasons[module.ID] = module.Reason
	}
	order := make([]string, 0, len(lock.Order))
	for _, id := range lock.Order {
		if reasons[id] != TombstoneReason {
			order = append(order, id)
		}
	}
	return order
}

func resolveModuleFiles(
	root string,
	currentLock Lock,
	module *LockedModule,
	targetManifest Manifest,
	payloads []plannedAuthoredPayload,
	targetPath string,
	pendingConflict PendingConflict,
	candidate []byte,
	candidateDigest string,
	localDigest string,
	localMissing bool,
	mode ResolutionMode,
	remaining []PendingConflict,
) ([]Change, error) {
	oldFiles := lockedFilesByPath(module.Files)
	payloadByPath := make(map[string]plannedAuthoredPayload, len(payloads))
	for _, payload := range payloads {
		payloadByPath[payload.file.Target] = payload
	}
	freshPayload, declared := payloadByPath[targetPath]
	if !declared {
		return nil, fmt.Errorf("pending conflict path %s is not declared by the target manifest", targetPath)
	}
	if digestBytes(freshPayload.content) != candidateDigest {
		return nil, fmt.Errorf("candidate sha256 does not match target manifest payload")
	}

	changes := []Change{}
	if len(remaining) != 0 {
		file, ok := oldFiles[targetPath]
		if !ok {
			return nil, fmt.Errorf("pending conflict path %s is not a locked file", targetPath)
		}
		state := FileModified
		finalLocal := localDigest
		if mode == ResolutionAcceptUpstream {
			state = FileClean
			finalLocal = candidateDigest
			kind := ChangeUpdate
			if localMissing {
				kind = ChangeCreate
			} else if localDigest == candidateDigest {
				kind = ChangeUnchanged
			}
			changes = append(changes, Change{
				Path: targetPath, Module: module.ID, Source: pendingConflict.CandidatePath,
				Kind: kind, Class: DestinationAuthored, SHA256: candidateDigest,
				Content: append([]byte(nil), candidate...),
				// Mode is declared data: the resolved bytes install with the
				// class the manifest declares, exactly like a plain sync.
				Executable: freshPayload.file.Class == FileClassScript,
			})
		} else if localDigest == candidateDigest {
			state = FileClean
		}
		for i := range module.Files {
			if module.Files[i].Path == targetPath {
				module.Files[i] = LockedFile{
					Path: file.Path, Source: file.Source, BaseSHA256: candidateDigest,
					LocalSHA256: finalLocal, State: state,
				}
				break
			}
		}
		return changes, nil
	}

	for _, oldFile := range module.Files {
		if _, exists := payloadByPath[oldFile.Path]; exists {
			continue
		}
		_, digest, missing, err := CurrentTargetState(root, oldFile.Path)
		if err != nil {
			return nil, err
		}
		if missing {
			continue
		}
		if digest != oldFile.BaseSHA256 {
			return nil, fmt.Errorf(
				"module %s dropped file %s with local modifications; removal planning is required", module.ID, oldFile.Path,
			)
		}
		changes = append(changes, Change{
			Path: oldFile.Path, Module: module.ID, Source: oldFile.Source,
			Kind: ChangeDelete, Class: DestinationAuthored, SHA256: oldFile.BaseSHA256,
		})
	}

	ownership := lockedFileOwnership(currentLock, true)
	files := make([]LockedFile, 0, len(payloads))
	for _, payload := range payloads {
		newDigest := digestBytes(payload.content)
		oldFile, hadOld := oldFiles[payload.file.Target]
		if payload.file.Target == targetPath {
			finalLocal := localDigest
			state := FileModified
			if mode == ResolutionAcceptUpstream {
				finalLocal = candidateDigest
				state = FileClean
				kind := ChangeUpdate
				if localMissing {
					kind = ChangeCreate
				} else if localDigest == candidateDigest {
					kind = ChangeUnchanged
				}
				changes = append(changes, Change{
					Path: targetPath, Module: module.ID, Source: pendingConflict.CandidatePath,
					Kind: kind, Class: DestinationAuthored, SHA256: candidateDigest,
					Content:    append([]byte(nil), candidate...),
					Executable: freshPayload.file.Class == FileClassScript,
				})
			} else if localDigest == candidateDigest {
				state = FileClean
			}
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: candidateDigest, LocalSHA256: finalLocal, State: state,
			})
			continue
		}
		if !hadOld {
			change, err := classifyAuthoredTarget(root, module.ID, payload.file, payload.content, ownership)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
			})
			continue
		}
		_, currentDigest, missing, err := CurrentTargetState(root, payload.file.Target)
		if err != nil {
			return nil, err
		}
		if missing {
			change, err := classifyAuthoredTarget(root, module.ID, payload.file, payload.content, ownership)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
			})
			continue
		}
		switch {
		case currentDigest == newDigest:
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
			})
		case currentDigest == oldFile.BaseSHA256:
			change, err := classifyAuthoredTarget(root, module.ID, payload.file, payload.content, ownership)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: newDigest, LocalSHA256: newDigest, State: FileClean,
			})
		case newDigest == oldFile.BaseSHA256:
			files = append(files, LockedFile{
				Path: payload.file.Target, Source: payload.file.Source,
				BaseSHA256: oldFile.BaseSHA256, LocalSHA256: currentDigest, State: FileModified,
			})
		default:
			return nil, fmt.Errorf("module %s file %s has an unresolved local/upstream collision", module.ID, payload.file.Target)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	module.Files = files
	return changes, nil
}

func recomputeLockGraph(ctx context.Context, lock *Lock) error {
	manifests := make(map[string]Manifest, len(lock.Modules))
	selected := make(map[string]struct{}, len(lock.Modules))
	for _, module := range lock.Modules {
		manifests[module.ID] = module.Manifest
		selected[module.ID] = struct{}{}
	}
	order, err := stableTopologicalOrder(ctx, selected, manifests)
	if err != nil {
		return err
	}
	requiredBy := make(map[string][]string, len(lock.Modules))
	for _, module := range lock.Modules {
		requiredBy[module.ID] = []string{}
	}
	for _, module := range lock.Modules {
		for _, requirement := range module.Manifest.Requires {
			requiredBy[requirement.ID] = append(requiredBy[requirement.ID], module.ID)
		}
	}
	for i := range lock.Modules {
		sort.Strings(requiredBy[lock.Modules[i].ID])
		lock.Modules[i].RequiredBy = append([]string{}, requiredBy[lock.Modules[i].ID]...)
	}
	lock.Order = append([]string{}, order...)
	return nil
}

func conflictsFromLock(lock Lock) []Conflict {
	result := []Conflict{}
	for _, module := range lock.Modules {
		if module.Pending == nil {
			continue
		}
		files := lockedFilesByPath(module.Files)
		for _, conflict := range module.Pending.Conflicts {
			file := files[conflict.Path]
			result = append(result, Conflict{
				Module: module.ID, Path: conflict.Path,
				BaseSHA256: file.BaseSHA256, LocalSHA256: file.LocalSHA256,
				UpstreamSHA256: conflict.CandidateSHA256,
				CandidatePath:  conflict.CandidatePath, DiffPath: conflict.DiffPath,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Module != result[j].Module {
			return result[i].Module < result[j].Module
		}
		return result[i].Path < result[j].Path
	})
	return result
}
