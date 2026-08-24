package modkit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		for _, member := range profile.Members {
			module := moduleByID[member]
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

	roots := sortedKeys(selected)
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
		for _, dependency := range module.Requires {
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
	for _, id := range roots {
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
		for _, dependency := range modules[id].Requires {
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

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
