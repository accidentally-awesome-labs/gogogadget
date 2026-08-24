package modkit

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type migrationPayload struct {
	module    string
	migration ManifestMigration
	content   []byte
}

type existingMigration struct {
	module string
	value  LockedMigration
}

func planMigrations(ctx context.Context, root string, registryFS fs.FS, modules []Manifest, existing Lock, hasLock bool) (map[string][]LockedMigration, []Change, error) {
	planned := make(map[string][]LockedMigration, len(modules))
	selected := make(map[string]struct{}, len(modules))
	authoredTargets := map[string]string{
		"go.mod":               "reserved",
		"gogogadget.json":      "reserved",
		"gogogadget.lock.json": "reserved",
	}
	for _, module := range modules {
		planned[module.ID] = []LockedMigration{}
		selected[module.ID] = struct{}{}
		for _, file := range module.Files {
			authoredTargets[file.Target] = module.ID
		}
	}

	byID := make(map[string]existingMigration)
	usedPaths := make(map[string]string)
	maxNumber := 0
	if hasLock {
		for _, module := range existing.Modules {
			for _, migration := range module.Migrations {
				byID[migration.ID] = existingMigration{module: module.ID, value: migration}
				usedPaths[migration.Path] = module.ID
				if migration.Number > maxNumber {
					maxNumber = migration.Number
				}
				if _, keep := selected[module.ID]; keep {
					if owner, collision := authoredTargets[migration.Path]; collision {
						return nil, nil, fmt.Errorf("migration target %q collides with authored target owned by %s", migration.Path, owner)
					}
					planned[module.ID] = append(planned[module.ID], migration)
				}
			}
		}
	}
	diskMax, err := scanMigrationNumbers(root)
	if err != nil {
		return nil, nil, err
	}
	if diskMax > maxNumber {
		maxNumber = diskMax
	}

	payloads := make([]migrationPayload, 0)
	for _, module := range modules {
		for _, migration := range module.Migrations {
			// Neutralization and teardown migrations are removal-time
			// payloads: they are verified and materialized only when a
			// drain-required module is removed, never at install time.
			if migration.Kind != MigrationImmutable {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			content, err := fs.ReadFile(registryFS, migration.Source)
			if err != nil {
				return nil, nil, fmt.Errorf("module %s migration payload %s: %w", module.ID, migration.Source, err)
			}
			if digestBytes(content) != migration.SHA256 {
				return nil, nil, fmt.Errorf("module %s migration payload %s sha256 mismatch", module.ID, migration.Source)
			}
			payloads = append(payloads, migrationPayload{module: module.ID, migration: migration, content: append([]byte(nil), content...)})
		}
	}
	sort.Slice(payloads, func(i, j int) bool {
		if payloads[i].module != payloads[j].module {
			return payloads[i].module < payloads[j].module
		}
		return payloads[i].migration.ID < payloads[j].migration.ID
	})

	changes := make([]Change, 0, len(payloads))
	for _, payload := range payloads {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		mapping, exists := byID[payload.migration.ID]
		if exists {
			if mapping.module != payload.module {
				return nil, nil, fmt.Errorf("migration id %q is immutably owned by %s, not %s", payload.migration.ID, mapping.module, payload.module)
			}
			if mapping.value.SHA256 != payload.migration.SHA256 {
				return nil, nil, fmt.Errorf("migration %q payload changed after immutable allocation", payload.migration.ID)
			}
			if owner, collision := authoredTargets[mapping.value.Path]; collision {
				return nil, nil, fmt.Errorf("migration target %q collides with authored target owned by %s", mapping.value.Path, owner)
			}
			change, err := classifyOwnedTarget(root, mapping.value.Path, payload.module, payload.migration.Source, DestinationMigration, payload.content, false)
			if err != nil {
				return nil, nil, err
			}
			changes = append(changes, change)
			continue
		}

		maxNumber++
		migrationPath := fmt.Sprintf("internal/db/migrations/%04d_%s.sql", maxNumber, sanitizeMigrationID(payload.migration.ID))
		if owner, collision := authoredTargets[migrationPath]; collision {
			return nil, nil, fmt.Errorf("migration target %q collides with authored target owned by %s", migrationPath, owner)
		}
		if owner, collision := usedPaths[migrationPath]; collision {
			return nil, nil, fmt.Errorf("migration target %q collides with immutable mapping owned by %s", migrationPath, owner)
		}
		if isGeneratedOutput(migrationPath) {
			return nil, nil, fmt.Errorf("migration target %q is a generated output and cannot be authored", migrationPath)
		}
		mappingValue := LockedMigration{
			ID: payload.migration.ID, Number: maxNumber, Path: migrationPath, SHA256: payload.migration.SHA256,
		}
		planned[payload.module] = append(planned[payload.module], mappingValue)
		byID[payload.migration.ID] = existingMigration{module: payload.module, value: mappingValue}
		usedPaths[migrationPath] = payload.module
		change, err := classifyOwnedTarget(root, migrationPath, payload.module, payload.migration.Source, DestinationMigration, payload.content, false)
		if err != nil {
			return nil, nil, err
		}
		changes = append(changes, change)
	}
	for module := range planned {
		sort.Slice(planned[module], func(i, j int) bool { return planned[module][i].ID < planned[module][j].ID })
	}
	return planned, changes, nil
}

func scanMigrationNumbers(root string) (int, error) {
	const directory = "internal/db/migrations"
	info, missing, err := lstatProjectPath(root, directory)
	if err != nil {
		return 0, fmt.Errorf("scan migrations: %w", err)
	}
	if missing {
		return 0, nil
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("migration directory %s is not a directory", directory)
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
	if err != nil {
		return 0, fmt.Errorf("scan migrations: %w", err)
	}
	maximum := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		separator := strings.IndexByte(name, '_')
		if separator <= 0 || !strings.HasSuffix(name, ".sql") {
			continue
		}
		number, err := strconv.Atoi(name[:separator])
		if err != nil || number <= 0 {
			continue
		}
		if number > maximum {
			maximum = number
		}
	}
	return maximum, nil
}

func sanitizeMigrationID(id string) string {
	var result strings.Builder
	underscore := false
	for _, r := range strings.ToLower(id) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			result.WriteRune(r)
			underscore = false
			continue
		}
		if result.Len() != 0 && !underscore {
			result.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(result.String(), "_")
}

// isGeneratedOutput reports whether a target path is a tool-owned generated
// output. Authored module targets must never claim these.
func isGeneratedOutput(path string) bool {
	if strings.HasSuffix(path, "_templ.go") || strings.Contains(path, "_registry_gen.") {
		return true
	}
	if strings.HasPrefix(path, "internal/db/sqlc/") || path == "static/app.css" || path == "static/ui-components.js" {
		return true
	}
	return false
}
