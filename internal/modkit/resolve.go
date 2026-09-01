package modkit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type selectedGraph struct {
	modules []Manifest
	order   []string
	reasons map[string]string
}

func readPlannerInputs(root string) (Project, string, Lock, bool, error) {
	projectData, err := os.ReadFile(filepath.Join(root, "gogogadget.json"))
	if err != nil {
		return Project{}, "", Lock{}, false, fmt.Errorf("read gogogadget.json: %w", err)
	}
	project, err := ParseProject(projectData)
	if err != nil {
		return Project{}, "", Lock{}, false, err
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return Project{}, "", Lock{}, false, fmt.Errorf("read go.mod: %w", err)
	}
	modulePath, err := parseModulePath(goMod)
	if err != nil {
		return Project{}, "", Lock{}, false, fmt.Errorf("parse go.mod: %w", err)
	}

	lockPath := filepath.Join(root, "gogogadget.lock.json")
	lockData, err := os.ReadFile(lockPath)
	if errors.Is(err, fs.ErrNotExist) {
		return project, modulePath, Lock{}, false, nil
	}
	if err != nil {
		return Project{}, "", Lock{}, false, fmt.Errorf("read gogogadget.lock.json: %w", err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return Project{}, "", Lock{}, false, err
	}
	return project, modulePath, lock, true, nil
}

func parseModulePath(data []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inBlockComment := false
	for scanner.Scan() {
		line, err := stripGoModComments(scanner.Text(), &inBlockComment)
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if len(fields) != 2 {
			return "", fmt.Errorf("module directive must contain exactly one path")
		}
		modulePath := fields[1]
		if strings.HasPrefix(modulePath, "\"") || strings.HasPrefix(modulePath, "`") {
			modulePath, err = strconv.Unquote(modulePath)
			if err != nil {
				return "", fmt.Errorf("module directive path: %w", err)
			}
		}
		if !validPackagePath(modulePath) {
			return "", fmt.Errorf("module path %q is invalid", modulePath)
		}
		return modulePath, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if inBlockComment {
		return "", fmt.Errorf("unterminated block comment")
	}
	return "", fmt.Errorf("module directive is required")
}

func stripGoModComments(line string, inBlock *bool) (string, error) {
	var out strings.Builder
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		if *inBlock {
			if i+1 < len(line) && line[i] == '*' && line[i+1] == '/' {
				*inBlock = false
				out.WriteByte(' ')
				i++
			}
			continue
		}
		if quote != 0 {
			out.WriteByte(line[i])
			if quote == '`' {
				if line[i] == '`' {
					quote = 0
				}
				continue
			}
			if escaped {
				escaped = false
			} else if line[i] == '\\' {
				escaped = true
			} else if line[i] == quote {
				quote = 0
			}
			continue
		}
		if line[i] == '"' || line[i] == '`' {
			quote = line[i]
			out.WriteByte(line[i])
			continue
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			break
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
			*inBlock = true
			out.WriteByte(' ')
			i++
			continue
		}
		out.WriteByte(line[i])
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated quoted string")
	}
	return out.String(), nil
}

func resolveSelectedGraph(ctx context.Context, project Project, catalog Catalog) (selectedGraph, error) {
	moduleByID := make(map[string]Manifest, len(catalog.Modules))
	for _, module := range catalog.Modules {
		moduleByID[module.ID] = module
	}
	profileByID := make(map[string]Profile, len(catalog.Profiles))
	for _, profile := range catalog.Profiles {
		profileByID[profile.ID] = profile
	}
	excluded := make(map[string]struct{}, len(project.Exclude))
	for _, id := range project.Exclude {
		if _, ok := moduleByID[id]; !ok {
			return selectedGraph{}, fmt.Errorf("project exclude contains unknown module %q", id)
		}
		excluded[id] = struct{}{}
	}

	selected := make(map[string]struct{})
	reasons := make(map[string]string)
	for _, id := range project.Modules {
		if err := ctx.Err(); err != nil {
			return selectedGraph{}, err
		}
		if _, ok := moduleByID[id]; ok {
			selected[id] = struct{}{}
			reasons[id] = "explicit"
			continue
		}
		profile, ok := profileByID[id]
		if !ok {
			return selectedGraph{}, fmt.Errorf("project selects unknown catalog id %q", id)
		}
		if err := validateProfileProviderParity(profile, moduleByID); err != nil {
			return selectedGraph{}, err
		}
		for _, member := range profile.Members {
			module, exists := moduleByID[member]
			if !exists {
				return selectedGraph{}, fmt.Errorf("profile %s references missing module %q", profile.ID, member)
			}
			// Profiles publish candidate adapters for discoverability, but adapter
			// activation is exclusively controlled by Project.Providers. Treating
			// every candidate as a profile member makes an unselected adapter an
			// explicit module and prevents registry closure validation.
			if module.Runtime.System != nil && module.Runtime.System.Adapter != nil {
				continue
			}
			_, omitted := excluded[member]
			if omitted && module.RemovalPolicy != RemovalReplacementRequired {
				continue
			}
			if _, exists := selected[member]; !exists {
				selected[member] = struct{}{}
				reasons[member] = "profile"
			}
		}
	}

	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var expand func(string) error
	expand = func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("dependency cycle includes %q", id)
		}
		module, ok := moduleByID[id]
		if !ok {
			return fmt.Errorf("required module %q is not present in the catalog", id)
		}
		visiting[id] = true
		for _, requirement := range module.Requires {
			dependency := requirement.ID
			dependencyModule, ok := moduleByID[dependency]
			if !ok {
				return fmt.Errorf("module %q requires missing dependency %q", id, dependency)
			}
			if dependencyModule.Contract < requirement.Contract.Min || dependencyModule.Contract > requirement.Contract.Max {
				return fmt.Errorf("module %q requires %s contract %d in inclusive range %d..%d", id, dependency, dependencyModule.Contract, requirement.Contract.Min, requirement.Contract.Max)
			}
			if _, exists := selected[dependency]; !exists {
				selected[dependency] = struct{}{}
				reasons[dependency] = "dependency"
			}
			if err := expand(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	// Provider choices are resolved only after the explicit/profile closure is
	// known. This prevents adapters from silently introducing optional seams.
	slots := map[string]struct{}{}
	for id := range selected {
		for _, slot := range moduleByID[id].Runtime.ProviderSlots {
			slots[slot.ID] = struct{}{}
		}
	}
	if len(slots) != len(project.Providers) {
		return selectedGraph{}, fmt.Errorf("project providers must exactly match selected provider slots")
	}
	adapterByID := map[string]Manifest{}
	for _, id := range sortedKeys(moduleByID) {
		module := moduleByID[id]
		if module.Runtime.System != nil && module.Runtime.System.Adapter != nil {
			adapterByID[id] = module
		}
	}
	slotsList := make([]string, 0, len(slots))
	for slot := range slots {
		slotsList = append(slotsList, slot)
	}
	sort.Strings(slotsList)
	chosenAdapters := map[string]struct{}{}
	for _, slot := range slotsList {
		choices, ok := project.Providers[slot]
		if !ok {
			return selectedGraph{}, fmt.Errorf("provider slot %q has no selections", slot)
		}
		for _, selectedChoice := range []struct {
			env    string
			choice ProviderSelection
		}{{"development", choices.Development}, {"test", choices.Test}, {"production", choices.Production}} {
			adapterID := selectedChoice.choice.Adapter
			adapter, ok := adapterByID[adapterID]
			if !ok || adapter.Runtime.System.Adapter.Slot != slot {
				return selectedGraph{}, fmt.Errorf("provider %s adapter %q does not implement slot %q", selectedChoice.env, adapterID, slot)
			}
			found := false
			for _, target := range adapter.Runtime.System.Adapter.Targets {
				if target.ID != selectedChoice.choice.Target {
					continue
				}
				found = true
				if !containsString(target.Environments, selectedChoice.env) {
					return selectedGraph{}, fmt.Errorf("provider %s target %s@%s is not allowed in %s", slot, adapterID, target.ID, selectedChoice.env)
				}
				if selectedChoice.env == "production" && target.Mode == "development" {
					return selectedGraph{}, fmt.Errorf("development target %s@%s cannot be used in production", adapterID, target.ID)
				}
			}
			if !found {
				return selectedGraph{}, fmt.Errorf("provider %s target %q is missing from adapter %q", slot, selectedChoice.choice.Target, adapterID)
			}
			chosenAdapters[adapterID] = struct{}{}
			if _, exists := selected[adapterID]; !exists {
				selected[adapterID] = struct{}{}
				reasons[adapterID] = "provider"
			}
		}
	}
	for _, explicitID := range project.Modules {
		if adapter, ok := adapterByID[explicitID]; ok {
			if _, chosen := chosenAdapters[adapter.ID]; !chosen {
				return selectedGraph{}, fmt.Errorf("explicit adapter %q is not selected by provider choices; use ggg provider set", explicitID)
			}
		}
	}
	if project.Deployment != "" {
		deployment, ok := moduleByID[project.Deployment]
		if !ok {
			return selectedGraph{}, fmt.Errorf("deployment module %q is not present", project.Deployment)
		}
		if deployment.Kind != ModuleSystem || deployment.Runtime.System == nil || len(deployment.Runtime.Deploy) != 1 {
			return selectedGraph{}, fmt.Errorf("deployment module %q must provide exactly one deploy target", project.Deployment)
		}
		selected[project.Deployment] = struct{}{}
		reasons[project.Deployment] = "deployment"
	}
	deployments := []string{}
	for id := range selected {
		if len(moduleByID[id].Runtime.Deploy) > 0 {
			deployments = append(deployments, id)
		}
	}
	sort.Strings(deployments)
	if len(deployments) > 1 {
		return selectedGraph{}, fmt.Errorf("multiple deployment modules selected: %s", strings.Join(deployments, ", "))
	}
	if len(deployments) == 1 && project.Deployment == "" {
		return selectedGraph{}, fmt.Errorf("deployment module %s requires project deployment selection", deployments[0])
	}
	if project.Deployment != "" && len(deployments) == 1 && deployments[0] != project.Deployment {
		return selectedGraph{}, fmt.Errorf("deployment module %s conflicts with selected deployment %s", deployments[0], project.Deployment)
	}
	for id := range selected {
		if err := expand(id); err != nil {
			return selectedGraph{}, err
		}
	}
	order, err := stableTopologicalOrder(ctx, selected, moduleByID)
	if err != nil {
		return selectedGraph{}, err
	}
	modules := make([]Manifest, 0, len(order))
	for _, id := range order {
		modules = append(modules, moduleByID[id])
	}
	return selectedGraph{modules: modules, order: order, reasons: reasons}, nil
}
func stableTopologicalOrder(ctx context.Context, selected map[string]struct{}, modules map[string]Manifest) ([]string, error) {

	indegree := make(map[string]int, len(selected))
	dependents := make(map[string][]string, len(selected))
	for id := range selected {
		indegree[id] = 0
	}
	for id := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, requirement := range modules[id].Requires {
			dependency := requirement.ID
			if _, ok := selected[dependency]; !ok {
				return nil, fmt.Errorf("module %q requires unselected module %q", id, dependency)
			}
			indegree[id]++
			dependents[dependency] = append(dependents[dependency], id)
		}
	}
	for id := range dependents {
		sort.Strings(dependents[id])
	}
	ready := make([]string, 0)
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(selected))
	for len(ready) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(selected) {
		remaining := make([]string, 0)
		for id, degree := range indegree {
			if degree != 0 {
				remaining = append(remaining, id)
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("dependency cycle among modules %s", strings.Join(remaining, ", "))
	}
	return order, nil
}

// RuntimeOrdersFor computes independent runtime DAGs for each environment.
// Mutually exclusive adapters are intentionally never topologically unioned.
// In addition to manifest requires, runtime needs create provider-before-
// consumer edges. This is deliberately separate from the install order:
// install order contains the complete union while each process branch only
// constructs one adapter per slot.
func RuntimeOrdersFor(ctx context.Context, modules []Manifest, project Project) (RuntimeOrders, error) {
	byID := make(map[string]Manifest, len(modules))
	for _, module := range modules {
		byID[module.ID] = module
	}
	build := func(env string) ([]string, error) {
		selected := map[string]struct{}{}
		for _, id := range sortedKeys(byID) {
			module := byID[id]
			if module.Runtime.System != nil && module.Runtime.System.Adapter == nil {
				selected[id] = struct{}{}
			}
		}
		for _, slot := range sortedKeys(project.Providers) {
			choices := project.Providers[slot]
			choice := choices.Development
			switch env {
			case "test":
				choice = choices.Test
			case "production":
				choice = choices.Production
			}
			adapter, ok := byID[choice.Adapter]
			if !ok {
				return nil, fmt.Errorf("provider %s adapter %q missing", slot, choice.Adapter)
			}
			if adapter.Runtime.System == nil || adapter.Runtime.System.Adapter == nil ||
				adapter.Runtime.System.Adapter.Slot != slot {
				return nil, fmt.Errorf("provider %s adapter %q does not implement slot", slot, choice.Adapter)
			}
			selected[choice.Adapter] = struct{}{}
		}
		// Build the standard dependency graph first, then add synthetic edges
		// from each selected provider to a consumer of its capability.
		indegree := make(map[string]int, len(selected))
		dependents := make(map[string][]string, len(selected))
		edges := make(map[string]map[string]struct{}, len(selected))
		for _, id := range sortedKeys(selected) {
			indegree[id] = 0
			edges[id] = map[string]struct{}{}
		}
		addEdge := func(provider, consumer string) {
			if provider == consumer {
				return
			}
			if _, exists := edges[provider][consumer]; exists {
				return
			}
			edges[provider][consumer] = struct{}{}
			indegree[consumer]++
			dependents[provider] = append(dependents[provider], consumer)
		}
		for _, id := range sortedKeys(selected) {
			module := byID[id]
			for _, requirement := range module.Requires {
				if _, exists := selected[requirement.ID]; !exists {
					continue
				}
				addEdge(requirement.ID, id)
			}
		}
		providers := map[string]string{}
		for _, id := range sortedKeys(selected) {
			module := byID[id]
			if module.Runtime.System == nil {
				continue
			}
			for _, provide := range module.Runtime.System.Provides {
				if previous, exists := providers[provide.Capability]; exists && previous != id {
					// Adapter candidates may share capability names, but only
					// one is selected in this environment.
					prev := byID[previous]
					if prev.Runtime.System == nil || prev.Runtime.System.Adapter == nil {
						return nil, fmt.Errorf("runtime capability %q has multiple providers", provide.Capability)
					}
					continue
				}
				providers[provide.Capability] = id
			}
		}
		for _, id := range sortedKeys(selected) {
			module := byID[id]
			if module.Runtime.System == nil {
				continue
			}
			for _, need := range module.Runtime.System.Needs {
				provider, ok := providers[need.Capability]
				if !ok {
					if need.Optional {
						continue
					}
					return nil, fmt.Errorf("runtime module %q has no provider for capability %q", id, need.Capability)
				}
				addEdge(provider, id)
			}
		}
		for id := range dependents {
			sort.Strings(dependents[id])
		}
		ready := make([]string, 0)
		for _, id := range sortedKeys(indegree) {
			if indegree[id] == 0 {
				ready = append(ready, id)
			}
		}
		sort.Strings(ready)
		order := make([]string, 0, len(selected))
		for len(ready) != 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := ready[0]
			ready = ready[1:]
			order = append(order, id)
			for _, dependent := range dependents[id] {
				indegree[dependent]--
				if indegree[dependent] == 0 {
					ready = append(ready, dependent)
					sort.Strings(ready)
				}
			}
		}
		if len(order) != len(selected) {
			remaining := make([]string, 0)
			for id, degree := range indegree {
				if degree != 0 {
					remaining = append(remaining, id)
				}
			}
			sort.Strings(remaining)
			return nil, fmt.Errorf("runtime dependency cycle in %s among %s", env, strings.Join(remaining, ", "))
		}
		return order, nil
	}
	var orders RuntimeOrders
	var err error
	if orders.Development, err = build("development"); err != nil {
		return RuntimeOrders{}, err
	}
	if orders.Test, err = build("test"); err != nil {
		return RuntimeOrders{}, err
	}
	if orders.Production, err = build("production"); err != nil {
		return RuntimeOrders{}, err
	}
	return orders, nil
}
func validateProfileProviderParity(profile Profile, modules map[string]Manifest) error {
	declared := map[string]struct{}{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		module, ok := modules[id]
		if !ok {
			return fmt.Errorf("profile %s references missing module %q", profile.ID, id)
		}
		for _, slot := range module.Runtime.ProviderSlots {
			declared[slot.ID] = struct{}{}
		}
		for _, requirement := range module.Requires {
			if err := visit(requirement.ID); err != nil {
				return err
			}
		}
		return nil
	}
	for _, member := range profile.Members {
		if err := visit(member); err != nil {
			return err
		}
	}
	want := make([]string, 0, len(declared))
	for id := range declared {
		want = append(want, id)
	}
	sort.Strings(want)
	got := append([]string{}, profile.RequiredProviderSlots...)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		return fmt.Errorf("profile %s required_provider_slots %v do not match member closure %v", profile.ID, got, want)
	}
	for slot := range profile.ProviderDefaults {
		if _, ok := declared[slot]; !ok {
			return fmt.Errorf("profile %s has provider default for undeclared slot %q", profile.ID, slot)
		}
	}
	return nil
}
