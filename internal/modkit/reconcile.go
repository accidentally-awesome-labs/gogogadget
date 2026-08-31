package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// conflictArtifactPrefix is the ignored scratch root for staged conflict
// candidates and diffs; lock validation and doctor enforce it.
const conflictArtifactPrefix = "tmp/ggg/conflicts/"

type reconciledModule struct {
	manifest     Manifest
	sourceCommit string
	files        []LockedFile
	pending      *PendingUpdate
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

func readPlannedPayloads(ctx context.Context, registryFS fs.FS, modules []Manifest, canonicalModule, modulePath string) ([]plannedAuthoredPayload, error) {
	payloads := make([]plannedAuthoredPayload, 0)
	for _, module := range modules {
		for _, file := range module.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// A generated file is produced by the build, not distributed by the
			// registry: the snapshot deliberately excludes generated outputs, so
			// there is nothing to read or verify here.
			if file.Class == FileClassGenerated {
				continue
			}
			content, err := fs.ReadFile(registryFS, file.Source)
			if err != nil {
				return nil, fmt.Errorf("module %s payload %s: %w", module.ID, file.Source, err)
			}
			if digestBytes(content) != file.SHA256 {
				return nil, fmt.Errorf("module %s payload %s sha256 mismatch", module.ID, file.Source)
			}
			installed := append([]byte(nil), content...)
			if file.RewriteModule {
				installed, err = rewriteModuleImports(file.Target, content, canonicalModule, modulePath)
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
	graph selectedGraph,
	payloads []plannedAuthoredPayload,
	existing Lock,
	hasLock bool,
	claims map[string]struct{},
) (Lock, []Change, []Conflict, []StagedFile, []Diagnostic, error) {
	// Generated outputs are tool-owned: authored module targets must never
	// claim them, so a manifest that points at a registry-owned artifact is a
	// preflight refusal rather than a silent overwrite. Enforce this before
	// any lock-state branching so fresh installs and reconciled updates fail
	// the same way.
	for _, payload := range payloads {
		if IsGeneratedOutputPath(payload.file.Target) {
			return Lock{}, nil, nil, nil, nil, fmt.Errorf(
				"module %s targets generated output %s; generated outputs are tool-owned and cannot be authored",
				payload.module, payload.file.Target,
			)
		}
	}
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
			_, localDigest, missing, stateErr := currentTargetState(root, payload.file.Target)
			if stateErr != nil {
				return Lock{}, nil, nil, nil, nil, stateErr
			}
			if !missing && localDigest != upstream {
				if _, claimed := claims[payload.file.Target]; !claimed {
					return Lock{}, nil, nil, nil, nil, fmt.Errorf(
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
				return Lock{}, nil, nil, nil, nil, err
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
		migrations, migrationChanges, err := planMigrations(ctx, root, snapshot.FS, graph.modules, Lock{}, false)
		if err != nil {
			return Lock{}, nil, nil, nil, nil, err
		}
		changes = append(changes, migrationChanges...)
		return buildPlannedLock(snapshot.Commit, graph, files, migrations), changes, []Conflict{}, []StagedFile{}, []Diagnostic{}, nil
	}

	oldModules := make(map[string]LockedModule, len(existing.Modules))
	for _, module := range existing.Modules {
		oldModules[module.ID] = module
	}
	newModules := make(map[string]Manifest, len(graph.modules))
	for _, module := range graph.modules {
		newModules[module.ID] = module
	}
	for id, module := range oldModules {
		if module.Reason == removalTombstoneReason {
			continue
		}
		if _, selected := newModules[id]; !selected {
			return Lock{}, nil, nil, nil, nil, fmt.Errorf(
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
			local, localDigest, missing, err := currentTargetState(root, payload.file.Target)
			if err != nil {
				return Lock{}, nil, nil, nil, nil, err
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
	conflicts, pendingConflicts, staged := buildConflictArtifacts(runID, detected)
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
	ownership := lockedFileOwnership(existing, true)
	for _, module := range graph.modules {
		if err := ctx.Err(); err != nil {
			return Lock{}, nil, nil, nil, nil, err
		}
		oldModule, hadOld := oldModules[module.ID]
		if _, held := hold[module.ID]; held && !hadOld {
			return Lock{}, nil, nil, nil, nil, fmt.Errorf(
				"module %s cannot be installed while a required module is conflicted", module.ID,
			)
		} else if held {
			lockedFiles := make([]LockedFile, 0, len(oldModule.Files))
			for _, oldFile := range oldModule.Files {
				_, localDigest, missing, err := currentTargetState(root, oldFile.Path)
				if err != nil {
					return Lock{}, nil, nil, nil, nil, err
				}
				if _, conflicted := directConflicts[module.ID][oldFile.Path]; conflicted {
					if missing {
						return Lock{}, nil, nil, nil, nil, fmt.Errorf(
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
			pending := &PendingUpdate{
				RunID: runID, RegistryCommit: snapshot.Commit, SourceCommit: snapshot.Commit,
				Manifest: module, Conflicts: append([]PendingConflict{}, pendingConflicts[module.ID]...),
			}
			states[module.ID] = reconciledModule{
				manifest: oldModule.Manifest, sourceCommit: oldModule.SourceCommit,
				files: lockedFiles, pending: pending,
			}
			continue
		}

		oldFiles := map[string]LockedFile{}
		if hadOld {
			oldFiles = lockedFilesByPath(oldModule.Files)
			newTargets := make(map[string]struct{}, len(module.Files))
			for _, file := range module.Files {
				newTargets[file.Target] = struct{}{}
			}
			for _, oldFile := range oldModule.Files {
				if _, exists := newTargets[oldFile.Path]; exists {
					continue
				}
				_, digest, missing, err := currentTargetState(root, oldFile.Path)
				if err != nil {
					return Lock{}, nil, nil, nil, nil, err
				}
				if missing {
					continue
				}
				if digest != oldFile.BaseSHA256 {
					return Lock{}, nil, nil, nil, nil, fmt.Errorf(
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
				_, localDigest, missing, err := currentTargetState(root, payload.file.Target)
				if err != nil {
					return Lock{}, nil, nil, nil, nil, err
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
				return Lock{}, nil, nil, nil, nil, err
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
		return Lock{}, nil, nil, nil, nil, err
	}
	effectiveList := make([]Manifest, 0, len(order))
	for _, id := range order {
		effectiveList = append(effectiveList, effectiveModules[id])
	}
	migrationFiles, migrationChanges, err := planMigrations(ctx, root, snapshot.FS, effectiveList, existing, true)
	if err != nil {
		return Lock{}, nil, nil, nil, nil, err
	}
	changes = append(changes, migrationChanges...)

	finalGraph := selectedGraph{modules: effectiveList, order: order, reasons: graph.reasons}
	finalLock := buildReconciledLock(snapshot.Commit, finalGraph, states, migrationFiles)
	// Tombstones of previously removed modules are permanent ledger rows:
	// carry them forward verbatim so their immutable migration mappings are
	// never lost and re-adds reuse the retained numbers.
	for _, module := range existing.Modules {
		if module.Reason != removalTombstoneReason {
			continue
		}
		if _, selectedAgain := newModules[module.ID]; selectedAgain {
			continue
		}
		finalLock.Modules = append(finalLock.Modules, module)
		finalLock.Order = append(finalLock.Order, module.ID)
	}
	sort.Slice(finalLock.Modules, func(i, j int) bool { return finalLock.Modules[i].ID < finalLock.Modules[j].ID })
	return finalLock, changes, conflicts, staged, diagnostics, nil
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
	content, digest, missing, err := currentTargetState(root, target)
	if err != nil {
		return nil, "", err
	}
	if missing {
		return nil, "", fmt.Errorf("owned target %s is missing", target)
	}
	return content, digest, nil
}

func currentTargetState(root, target string) (content []byte, digest string, missing bool, err error) {
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

func buildConflictArtifacts(runID string, detected []detectedConflict) ([]Conflict, map[string][]PendingConflict, []StagedFile) {
	conflicts := make([]Conflict, 0, len(detected))
	pending := make(map[string][]PendingConflict)
	staged := make([]StagedFile, 0, len(detected)*2)
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
		staged = append(staged,
			StagedFile{Path: candidatePath, SHA256: upstreamDigest, Content: append([]byte(nil), item.upstream.content...)},
			StagedFile{Path: diffPath, SHA256: digestBytes(diff), Content: diff},
		)
	}
	return conflicts, pending, staged
}

func unifiedConflictDiff(target string, local, upstream []byte) []byte {
	if !utf8.Valid(local) || !utf8.Valid(upstream) {
		return []byte("Binary conflict for " + target + "; inspect the complete .candidate file.\n")
	}
	oldLines, oldNewline := splitDiffLines(string(local))
	newLines, newNewline := splitDiffLines(string(upstream))
	oldStart, newStart := 1, 1
	if len(oldLines) == 0 {
		oldStart = 0
	}
	if len(newLines) == 0 {
		newStart = 0
	}
	var out strings.Builder
	fmt.Fprintf(
		&out, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n",
		target, target, oldStart, len(oldLines), newStart, len(newLines),
	)
	for i, line := range oldLines {
		out.WriteByte('-')
		out.WriteString(line)
		out.WriteByte('\n')
		if i == len(oldLines)-1 && !oldNewline {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	for i, line := range newLines {
		out.WriteByte('+')
		out.WriteString(line)
		out.WriteByte('\n')
		if i == len(newLines)-1 && !newNewline {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	return []byte(out.String())
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
			SnapshotSHA256: commit, Reason: graph.reasons[module.ID],
			RequiredBy: append([]string{}, requiredBy[module.ID]...), Manifest: state.manifest,
			Files: append([]LockedFile{}, state.files...), Migrations: append([]LockedMigration{}, migrations[module.ID]...),
			Pending: state.pending,
		})
	}
	return Lock{Schema: 2, RegistryCommit: commit, Registries: []LockedRegistry{}, Snapshots: []LockedSnapshot{},
		Order: append([]string{}, graph.order...), RuntimeOrders: RuntimeOrders{
			Development: append([]string{}, graph.order...), Test: append([]string{}, graph.order...), Production: append([]string{}, graph.order...),
		}, Dependencies: []LockedDependency{}, Modules: locked}
}

func sortPlanOutputs(changes []Change, conflicts []Conflict, staged []StagedFile) {
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
	sort.Slice(staged, func(i, j int) bool { return staged[i].Path < staged[j].Path })
}
