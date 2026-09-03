package modkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MigrateSchema1Project converts legacy project metadata to schema 2. Remote
// migrations require the real pinned public key; a placeholder such as "core"
// would make the resulting project unverifiable, so it is rejected. A
// project-contained legacy path is migrated to an unsigned directory source.
func MigrateSchema1Project(data []byte) ([]byte, error) {
	var legacy struct {
		Schema   int `json:"schema"`
		Registry struct {
			Repository string `json:"repository"`
			Ref        string `json:"ref"`
			Path       string `json:"path"`
			PublicKey  string `json:"public_key"`
		} `json:"registry"`
		Modules []string `json:"modules"`
		Exclude []string `json:"exclude"`
	}
	if err := decodeStrict(data, &legacy); err != nil {
		return nil, fmt.Errorf("decode schema 1 project: %w", err)
	}
	if legacy.Schema != 1 {
		return nil, fmt.Errorf("schema 1 project required")
	}
	scope := func(id string) string {
		if strings.Count(id, "/") == 1 {
			return "ggg/" + id
		}
		return id
	}
	registry := ProjectRegistry{Namespace: "ggg"}
	if legacy.Registry.Path != "" || legacy.Registry.Repository == "." {
		registry.Source, registry.Path = "directory", legacy.Registry.Path
		if registry.Path == "" {
			registry.Path = "."
		}
		if strings.HasPrefix(registry.Path, "/") || strings.Contains(registry.Path, "..") {
			return nil, fmt.Errorf("legacy registry path must be project-contained")
		}
	} else {
		if legacy.Registry.Repository == "" || legacy.Registry.Ref == "" {
			return nil, fmt.Errorf("schema 1 remote registry repository and ref are required")
		}
		if legacy.Registry.PublicKey == "" || legacy.Registry.PublicKey == "core" {
			return nil, fmt.Errorf("schema 1 remote registry public_key is required for migration")
		}
		registry.Source, registry.Repository, registry.Ref, registry.PublicKey = "github", legacy.Registry.Repository, legacy.Registry.Ref, legacy.Registry.PublicKey
	}
	out := Project{Schema: 2, Registries: []ProjectRegistry{registry}, Modules: make([]string, len(legacy.Modules)), Exclude: make([]string, len(legacy.Exclude)), Providers: map[string]ProviderSelections{}, Deployment: ""}
	for i, id := range legacy.Modules {
		out.Modules[i] = scope(id)
	}
	for i, id := range legacy.Exclude {
		out.Exclude[i] = scope(id)
	}
	if err := validateProject(out, true); err != nil {
		return nil, fmt.Errorf("migrated project: %w", err)
	}
	return MarshalProject(out)
}

func MigrateSchema1(project []byte) ([]byte, error) { return MigrateSchema1Project(project) }

// MigrateSchema1Lock rewrites lock metadata without fabricating snapshot
// digests. A schema-1 lock lacking a snapshot digest cannot be safely upgraded
// because source commits and archive bytes are different identities.
func MigrateSchema1Lock(data []byte) ([]byte, error) {
	var root map[string]any
	if err := decodeStrict(data, &root); err != nil {
		return nil, fmt.Errorf("decode schema 1 lock: %w", err)
	}
	if schema, ok := root["schema"].(float64); !ok || int(schema) != 1 {
		return nil, fmt.Errorf("schema 1 lock required")
	}
	scope := func(id string) string {
		if strings.Count(id, "/") == 1 {
			return "ggg/" + id
		}
		return id
	}
	if order, ok := root["order"].([]any); ok {
		for i, v := range order {
			if id, ok := v.(string); ok {
				order[i] = scope(id)
			}
		}
	}
	if modules, ok := root["modules"].([]any); ok {
		for _, raw := range modules {
			m, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("schema 1 lock module is invalid")
			}
			if id, ok := m["id"].(string); ok {
				m["id"] = scope(id)
			}
			if ids, ok := m["required_by"].([]any); ok {
				for i, v := range ids {
					if id, ok := v.(string); ok {
						ids[i] = scope(id)
					}
				}
			}
			if manifest, ok := m["manifest"].(map[string]any); ok {
				if id, ok := manifest["id"].(string); ok {
					manifest["id"] = scope(id)
				}
				if reqs, ok := manifest["requires"].([]any); ok {
					for i, v := range reqs {
						if id, ok := v.(string); ok {
							reqs[i] = map[string]any{"id": scope(id), "contract": map[string]any{"min": 1, "max": 1}}
						}
					}
				}
				if _, ok := manifest["dependencies"]; !ok {
					manifest["dependencies"] = map[string]any{"go": []any{}, "tools": []any{}, "containers": []any{}}
				}
			}
			if _, ok := m["registry_namespace"]; !ok {
				m["registry_namespace"] = "ggg"
			}
			if _, ok := m["snapshot_sha256"]; !ok {
				return nil, fmt.Errorf("schema 1 lock module %q has no snapshot_sha256; re-resolve before migration", m["id"])
			}
		}
	}
	if _, ok := root["registry_commit"]; !ok {
		return nil, fmt.Errorf("schema 1 lock registry_commit is required")
	}
	if old, ok := root["registry"].(map[string]any); ok {
		registry := map[string]any{"namespace": "ggg"}
		for _, key := range []string{"source", "requested_ref", "canonical_module", "key_fingerprint"} {
			if v, exists := old[key]; exists {
				registry[key] = v
			}
		}
		root["registries"] = []any{registry}
		delete(root, "registry")
	}
	if _, ok := root["registries"]; !ok {
		root["registries"] = []any{}
	}
	if _, ok := root["snapshots"]; !ok {
		root["snapshots"] = []any{}
	}
	order, _ := root["order"].([]any)
	root["schema"] = 2
	// The migrating binary is the writer, so it records its own contract.
	root["engine_contract"] = EngineContract
	root["runtime_orders"] = map[string]any{"development": order, "test": order, "production": order}
	root["dependencies"] = []any{}
	root["providers"] = map[string]any{}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// sameJSON is used by migration tests to assert payload bytes are untouched.
func sameJSON(a, b []byte) bool { return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) }
