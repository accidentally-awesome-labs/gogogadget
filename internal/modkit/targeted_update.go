package modkit

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// targetedUpdate is the outcome of computing a targeted `ggg update` closure:
// which installed modules advance to the freshly resolved catalogs and which
// stay pinned at their per-module snapshots.
type targetedUpdate struct {
	// updated maps every module that advances — the named operands plus the
	// required closure they pull in — to its new manifest.
	updated map[string]Manifest
	// retained holds every other installed module id. Their lock rows, file
	// digests, and snapshot provenance carry forward verbatim.
	retained map[string]struct{}
	// updatedOrder lists updated ids deterministically for messages.
	updatedOrder []string
}

// planTargetedUpdate computes the targeted closure for `ggg update MODULES...`.
//
// Named modules must be installed and must exist in the freshly resolved
// catalogs. Each advances to its owning registry's declared ref. The closure
// walks forward through the new manifests' requirements: a requirement that is
// already installed and still satisfied leaves that dependency untouched, one
// that is no longer satisfied pulls the dependency's new manifest into the
// update, and one that no catalog satisfies is refused. Every already-
// installed module that requires an updated module but is not itself updated
// must still accept the new contract — otherwise the refusal names the pair
// that has to move together.
func planTargetedUpdate(ctx context.Context, existing Lock, catalog Catalog, named []string) (targetedUpdate, error) {
	if len(named) == 0 {
		return targetedUpdate{}, fmt.Errorf("targeted update requires at least one module")
	}
	installed := make(map[string]LockedModule)
	for _, module := range existing.Modules {
		if module.Reason == TombstoneReason {
			continue
		}
		installed[module.ID] = module
	}
	catalogByID := make(map[string]Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		catalogByID[module.ID] = module
	}
	namedSet := make(map[string]struct{}, len(named))
	queue := make([]string, 0, len(named))
	for _, id := range named {
		if err := ValidateScopedProjectModuleID(id); err != nil {
			return targetedUpdate{}, fmt.Errorf("%s: %w", id, err)
		}
		if _, duplicate := namedSet[id]; duplicate {
			continue
		}
		namedSet[id] = struct{}{}
		if _, ok := installed[id]; !ok {
			return targetedUpdate{}, fmt.Errorf("update target %s is not installed; use `ggg add` to install it", id)
		}
		if _, ok := catalogByID[id]; !ok {
			return targetedUpdate{}, fmt.Errorf("update target %s is not published by any resolved registry", id)
		}
		queue = append(queue, id)
	}
	result := targetedUpdate{
		updated:  map[string]Manifest{},
		retained: map[string]struct{}{},
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return targetedUpdate{}, err
		}
		id := queue[0]
		queue = queue[1:]
		if _, done := result.updated[id]; done {
			continue
		}
		next, ok := catalogByID[id]
		if !ok {
			return targetedUpdate{}, fmt.Errorf("required module %s is not published by any resolved registry", id)
		}
		result.updated[id] = next
		for _, requirement := range next.Requires {
			installedModule, isInstalled := installed[requirement.ID]
			if !isInstalled {
				// A brand-new dependency joins the required closure.
				queue = append(queue, requirement.ID)
				continue
			}
			if requirement.Contract.Min <= installedModule.Contract && installedModule.Contract <= requirement.Contract.Max {
				// Still satisfied: leave the dependency at its snapshot.
				continue
			}
			candidate, published := catalogByID[requirement.ID]
			if !published || candidate.Contract < requirement.Contract.Min || candidate.Contract > requirement.Contract.Max {
				return targetedUpdate{}, fmt.Errorf(
					"contract conflict: %s requires %s in [%d,%d], installed %s has contract %d; "+
						"%s and %s must move together",
					id, requirement.ID, requirement.Contract.Min, requirement.Contract.Max,
					requirement.ID, installedModule.Contract, id, requirement.ID)
			}
			queue = append(queue, requirement.ID)
		}
	}
	// Reverse-dependency impact: every installed module that requires an
	// updated module must still accept the new contract, or it names itself
	// and the updated module as ones that must move together.
	for _, module := range existing.Modules {
		if module.Reason == TombstoneReason {
			continue
		}
		if _, advancing := result.updated[module.ID]; advancing {
			continue
		}
		for _, requirement := range module.Manifest.Requires {
			next, advancing := result.updated[requirement.ID]
			if !advancing {
				continue
			}
			if requirement.Contract.Min <= next.Contract && next.Contract <= requirement.Contract.Max {
				continue
			}
			return targetedUpdate{}, fmt.Errorf(
				"contract conflict: %s requires %s in [%d,%d] but the update brings contract %d; "+
					"%s and %s must move together",
				module.ID, requirement.ID, requirement.Contract.Min, requirement.Contract.Max, next.Contract,
				module.ID, requirement.ID)
		}
	}
	for id := range result.updated {
		result.updatedOrder = append(result.updatedOrder, id)
	}
	sort.Strings(result.updatedOrder)
	for id := range installed {
		if _, advancing := result.updated[id]; !advancing {
			result.retained[id] = struct{}{}
		}
	}
	return result, nil
}

// overlayTargetedGraph replaces every retained module's manifest with its
// locked manifest, so the resolved graph — and everything derived from it —
// describes the tree the plan actually produces: updated modules advance,
// retained modules stay pinned even when the new snapshot changed them.
func overlayTargetedGraph(graph []Manifest, existing Lock, targeted targetedUpdate) ([]Manifest, error) {
	locked := make(map[string]LockedModule, len(existing.Modules))
	for _, module := range existing.Modules {
		locked[module.ID] = module
	}
	overlay := make([]Manifest, len(graph))
	copy(overlay, graph)
	for i, module := range overlay {
		if _, advancing := targeted.updated[module.ID]; advancing {
			continue
		}
		pinned, ok := locked[module.ID]
		if !ok {
			return nil, fmt.Errorf("module %s is selected but not installed; targeted updates cannot install it", module.ID)
		}
		overlay[i] = pinned.Manifest
	}
	sort.Slice(overlay, func(i, j int) bool { return overlay[i].ID < overlay[j].ID })
	return overlay, nil
}

// setRegistryRef moves exactly one configured registry to a new ref. The
// namespace must already be part of the project: a targeted ref change is a
// pin move, never a way to introduce a source.
func setRegistryRef(registries []ProjectRegistry, namespace, ref string) error {
	if strings.TrimSpace(ref) == "" || ref != strings.TrimSpace(ref) {
		return fmt.Errorf("operation registry ref must be non-empty and trimmed")
	}
	for i := range registries {
		if registries[i].Namespace == namespace {
			registries[i].Ref = ref
			return nil
		}
	}
	return fmt.Errorf("registry %q is not configured in this project", namespace)
}
