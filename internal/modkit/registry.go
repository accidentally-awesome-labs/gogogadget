package modkit

import (
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"
)

// CatalogKind is the closed set of registry index kinds.
type CatalogKind string

const (
	CatalogElement   CatalogKind = "element"
	CatalogComponent CatalogKind = "component"
	CatalogPage      CatalogKind = "page"
	CatalogWorkflow  CatalogKind = "workflow"
	CatalogSystem    CatalogKind = "system"
	CatalogProfile   CatalogKind = "profile"
)

// RegistryRoot names every catalog index in canonical load order.
type RegistryRoot struct {
	Schema   int      `json:"schema"`
	Includes []string `json:"includes"`
}

// CatalogIndex explicitly lists every document belonging to one kind.
type CatalogIndex struct {
	Schema int         `json:"schema"`
	Kind   CatalogKind `json:"kind"`
	Items  []string    `json:"items"`
}

// ModuleDocument is the published envelope for one installable manifest.
type ModuleDocument struct {
	Schema int      `json:"schema"`
	Module Manifest `json:"module"`
}

// Profile is an ordered intent expansion over installable module IDs.
type Profile struct {
	ID          string      `json:"id"`
	Kind        CatalogKind `json:"kind"`
	Name        string      `json:"name"`
	Revision    int         `json:"revision"`
	Contract    int         `json:"contract"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Members     []string    `json:"members"`
}

// ProfileDocument is the published envelope for one profile.
type ProfileDocument struct {
	Schema  int     `json:"schema"`
	Profile Profile `json:"profile"`
}

// Catalog is the fully validated production registry.
type Catalog struct {
	Modules  []Manifest
	Profiles []Profile
}

var catalogIncludes = []struct {
	path string
	kind CatalogKind
}{
	{"registry/elements.json", CatalogElement},
	{"registry/components.json", CatalogComponent},
	{"registry/pages.json", CatalogPage},
	{"registry/workflows.json", CatalogWorkflow},
	{"registry/systems.json", CatalogSystem},
	{"registry/profiles.json", CatalogProfile},
}

// LoadCatalog loads only registry.json, its canonical explicit indexes, and the
// documents explicitly named by those indexes.
func LoadCatalog(fsys fs.FS) (Catalog, error) {
	var catalog Catalog

	var root RegistryRoot
	if err := readCatalogJSON(fsys, "registry.json", &root); err != nil {
		return catalog, err
	}
	if root.Schema != 1 {
		return catalog, fmt.Errorf("registry.json schema must be 1")
	}
	if root.Includes == nil {
		return catalog, fmt.Errorf("registry.json includes array is required")
	}
	if len(root.Includes) != len(catalogIncludes) {
		return catalog, fmt.Errorf("registry.json includes must equal the canonical index list in order")
	}
	for i, include := range catalogIncludes {
		if root.Includes[i] != include.path {
			return catalog, fmt.Errorf("registry.json includes must equal the canonical index list in order")
		}
	}

	identities := make(map[string]string)
	modules := make(map[string]struct{})
	for _, include := range catalogIncludes {
		var index CatalogIndex
		if err := readCatalogJSON(fsys, include.path, &index); err != nil {
			return Catalog{}, err
		}
		if index.Schema != 1 {
			return Catalog{}, fmt.Errorf("%s schema must be 1", include.path)
		}
		if index.Kind != include.kind {
			return Catalog{}, fmt.Errorf("%s kind must be %q", include.path, include.kind)
		}
		if index.Items == nil {
			return Catalog{}, fmt.Errorf("%s items array is required", include.path)
		}
		if err := validateStringSet(include.path+" items", index.Items, true, func(item string) error {
			return validateCatalogItemPath(include.kind, item)
		}); err != nil {
			return Catalog{}, err
		}

		for _, item := range index.Items {
			if include.kind == CatalogProfile {
				var document ProfileDocument
				if err := readCatalogJSON(fsys, item, &document); err != nil {
					return Catalog{}, err
				}
				if document.Schema != 1 {
					return Catalog{}, fmt.Errorf("%s schema must be 1", item)
				}
				if err := validateProfile(document.Profile); err != nil {
					return Catalog{}, fmt.Errorf("%s: %w", item, err)
				}
				if previous, exists := identities[document.Profile.ID]; exists {
					return Catalog{}, fmt.Errorf("duplicate catalog id %q in %s and %s", document.Profile.ID, previous, item)
				}
				identities[document.Profile.ID] = item
				catalog.Profiles = append(catalog.Profiles, document.Profile)
				continue
			}

			var document ModuleDocument
			if err := readCatalogJSON(fsys, item, &document); err != nil {
				return Catalog{}, err
			}
			if document.Schema != 1 {
				return Catalog{}, fmt.Errorf("%s schema must be 1", item)
			}
			if err := validateManifest(document.Module, true); err != nil {
				return Catalog{}, fmt.Errorf("%s: %w", item, err)
			}
			if CatalogKind(document.Module.Kind) != include.kind {
				return Catalog{}, fmt.Errorf("%s module kind %q does not match index kind %q", item, document.Module.Kind, include.kind)
			}
			if document.Module.TestOnly {
				return Catalog{}, fmt.Errorf("%s: test_only modules are not allowed in production indexes", item)
			}
			if previous, exists := identities[document.Module.ID]; exists {
				return Catalog{}, fmt.Errorf("duplicate catalog id %q in %s and %s", document.Module.ID, previous, item)
			}
			identities[document.Module.ID] = item
			modules[document.Module.ID] = struct{}{}
			catalog.Modules = append(catalog.Modules, document.Module)
		}
	}

	for _, profile := range catalog.Profiles {
		for _, member := range profile.Members {
			if _, exists := modules[member]; !exists {
				return Catalog{}, fmt.Errorf("profile %q member %q is not present in the catalog", profile.ID, member)
			}
		}
	}

	sort.Slice(catalog.Modules, func(i, j int) bool { return catalog.Modules[i].ID < catalog.Modules[j].ID })
	sort.Slice(catalog.Profiles, func(i, j int) bool { return catalog.Profiles[i].ID < catalog.Profiles[j].ID })
	return catalog, nil
}

func readCatalogJSON(fsys fs.FS, name string, dst any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	if err := decodeStrict(data, dst); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	if err := requireJSONValue(data, reflect.TypeOf(dst), name); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}

func validateCatalogItemPath(kind CatalogKind, item string) error {
	if err := validateSafePath(item); err != nil {
		return err
	}
	if !fs.ValidPath(item) || path.Ext(item) != ".json" {
		return fmt.Errorf("must be a safe relative JSON path")
	}
	prefix := "registry/modules/" + string(kind) + "/"
	if kind == CatalogProfile {
		prefix = "registry/profiles/"
	}
	if !strings.HasPrefix(item, prefix) || len(item) == len(prefix) {
		return fmt.Errorf("path %q must stay under %s", item, prefix)
	}
	return nil
}

func validateProfile(profile Profile) error {
	kind, name, ok := splitModuleID(profile.ID)
	if !ok || kind != string(CatalogProfile) {
		return fmt.Errorf("profile id %q is invalid", profile.ID)
	}
	if profile.Kind != CatalogProfile {
		return fmt.Errorf("profile kind must be %q", CatalogProfile)
	}
	if !validKebab(profile.Name) || name != profile.Name || profile.ID != string(CatalogProfile)+"/"+profile.Name {
		return fmt.Errorf("profile identity does not match id, kind, and name")
	}
	if profile.Revision <= 0 {
		return fmt.Errorf("profile revision must be positive")
	}
	if profile.Contract <= 0 {
		return fmt.Errorf("profile contract must be positive")
	}
	if strings.TrimSpace(profile.Title) == "" {
		return fmt.Errorf("profile title must be non-empty")
	}
	if strings.TrimSpace(profile.Description) == "" {
		return fmt.Errorf("profile description must be non-empty")
	}
	if profile.Members == nil {
		return fmt.Errorf("profile members array is required")
	}
	if err := validateStringSet("profile members", profile.Members, true, validateInstallableModuleID); err != nil {
		return err
	}
	return nil
}
