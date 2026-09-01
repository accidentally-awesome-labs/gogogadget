package modkit

import (
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

	adoptedMigrations, err := scanAdoptableMigrations(root)
	if err != nil {
		return nil, nil, err
	}

	changes := make([]Change, 0, len(payloads))
	for _, payload := range payloads {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		mapping, exists := byID[payload.migration.ID]
		if !exists {
			// Adoption: the migration may already be on disk from before the
			// registry existed. Goose records applied migrations by filename, so
			// re-allocating a number would re-run schema changes that are already
			// applied in every deployed database. Match by content digest, which
			// is exact, and keep the number the migration shipped under.
			if adopted, ok := adoptedMigrations[payload.migration.SHA256]; ok {
				if len(adopted.ambiguousPaths) > 0 {
					return nil, nil, fmt.Errorf(
						"cannot adopt migration %q: %s have identical bytes, so the number it shipped under is ambiguous",
						payload.migration.ID, strings.Join(adopted.ambiguousPaths, ", "))
				}
				if owner, collision := usedPaths[adopted.path]; collision {
					return nil, nil, fmt.Errorf(
						"adopted migration target %q collides with immutable mapping owned by %s", adopted.path, owner)
				}
				mappingValue := LockedMigration{
					ID: payload.migration.ID, Number: adopted.number,
					Path: adopted.path, SHA256: payload.migration.SHA256,
				}
				planned[payload.module] = append(planned[payload.module], mappingValue)
				byID[payload.migration.ID] = existingMigration{module: payload.module, value: mappingValue}
				usedPaths[adopted.path] = payload.module
				change, err := classifyOwnedTarget(
					root, adopted.path, payload.module, payload.migration.Source,
					DestinationMigration, payload.content, false,
				)
				if err != nil {
					return nil, nil, err
				}
				changes = append(changes, change)
				continue
			}
		}
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
		if IsGeneratedOutputPath(migrationPath) {
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

// IsGeneratedOutputPath reports whether a path is build output rather than
// distributed source. Authored module targets must never claim one. Exported
// because the ownership guard needs the same answer the snapshot and the
// emitters use — a second copy of this rule would drift from the one that
// matters.
func IsGeneratedOutputPath(path string) bool {
	return isExternalToolOutput(path) || IsRegistryOwnedOutputPath(path)
}

// isExternalToolOutput covers the outputs templ, sqlc, and Tailwind produce.
// They are generated, but this pipeline never renders them, so the stale sweep
// must not treat their absence from a render as "no longer owned".
func isExternalToolOutput(path string) bool {
	if strings.HasSuffix(path, "_templ.go") {
		return true
	}
	return strings.HasPrefix(path, "internal/db/sqlc/") || path == "static/app.css"
}

// IsRegistryOwnedOutputPath reports whether a path is rendered by GenerateAll.
// This is the set `ggg sync` writes AND, when the selected graph stops rendering
// one, deletes: an aggregate nothing owns still compiles into the project and
// still references renderers the removal deleted. It is therefore the one
// answer to "is this ours", and TestEveryEmittedPathIsRegistryOwned holds the
// emitters to it.
//
// Listed explicitly rather than matched by pattern beyond the `_registry_gen.`
// infix: this predicate is what stops an emitter from overwriting authored
// source and what authorises a delete, so widening it stays deliberate.
func IsRegistryOwnedOutputPath(path string) bool {
	if strings.Contains(path, "_registry_gen.") {
		return true
	}
	switch path {
	case "static/ui-components.js", "static/ui-engines.js",
		".env.example", "content/docs/configuration-reference.md", "content/docs/module-reference.md", "content/docs/component-reference.md",
		"e2e/generated/personas.ts", "e2e/generated/surfaces.ts",
		"internal/web/templates/scenarios_gen.go",
		"internal/web/templates/ui/reference_gen.go":
		return true
	}
	return false
}

// adoptableMigration is one migration already present in the project tree,
// keyed by content digest so adoption is exact rather than name-guessing.
type adoptableMigration struct {
	number int
	path   string
	// ambiguousPaths is non-empty when several on-disk migrations share these
	// bytes, which makes the intended number unknowable.
	ambiguousPaths []string
}

// scanAdoptableMigrations indexes the migrations already on disk by content
// digest. Two files with identical bytes are refused rather than guessed at:
// picking one would silently bind a module to the wrong number forever.
func scanAdoptableMigrations(root string) (map[string]adoptableMigration, error) {
	const directory = "internal/db/migrations"
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]adoptableMigration{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan adoptable migrations: %w", err)
	}

	adoptable := make(map[string]adoptableMigration, len(entries))
	ambiguous := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		name := entry.Name()
		separator := strings.IndexByte(name, '_')
		if separator <= 0 {
			continue
		}
		number, convErr := strconv.Atoi(name[:separator])
		if convErr != nil || number <= 0 {
			continue
		}
		path := directory + "/" + name
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return nil, fmt.Errorf("read adoptable migration %s: %w", path, readErr)
		}
		digest := digestBytes(content)
		if previous, clash := adoptable[digest]; clash {
			// Identical bytes under two numbers: which one a manifest means is
			// unknowable, so neither is adoptable. This is only an error if a
			// manifest actually resolves to this digest, so record it and refuse
			// at the point of harm rather than failing every unrelated plan.
			ambiguous[digest] = append(ambiguous[digest], previous.path, path)
			delete(adoptable, digest)
			continue
		}
		if _, known := ambiguous[digest]; known {
			ambiguous[digest] = append(ambiguous[digest], path)
			continue
		}
		adoptable[digest] = adoptableMigration{number: number, path: path}
	}
	for digest, paths := range ambiguous {
		sort.Strings(paths)
		adoptable[digest] = adoptableMigration{ambiguousPaths: paths}
	}
	return adoptable, nil
}
