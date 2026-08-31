package modkit

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MigrateSchema1Project converts legacy project metadata to schema 2. It does
// not inspect or rewrite registry payloads, so authored bytes and their
// digests remain unchanged. Callers must journal the returned bytes.
func MigrateSchema1Project(data []byte) ([]byte, error) {
	var legacy struct {
		Schema int `json:"schema"`
		Registry struct { Repository string `json:"repository"`; Ref string `json:"ref"` } `json:"registry"`
		Modules []string `json:"modules"`
		Exclude []string `json:"exclude"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil { return nil, fmt.Errorf("decode schema 1 project: %w", err) }
	if legacy.Schema != 1 { return nil, fmt.Errorf("schema 1 project required") }
	if legacy.Registry.Repository == "" || legacy.Registry.Ref == "" { return nil, fmt.Errorf("schema 1 registry repository and ref are required") }
	scope := func(id string) string { if strings.Count(id, "/") == 1 { return "ggg/" + id }; return id }
	registries := []ProjectRegistry{{Namespace:"ggg", Source:"github", Repository:legacy.Registry.Repository, Ref:legacy.Registry.Ref, PublicKey:"core"}}
	out := Project{Schema:2, Registries:registries, Modules:make([]string,len(legacy.Modules)), Exclude:make([]string,len(legacy.Exclude)), Providers:map[string]ProviderSelections{}, Deployment:""}
	for i,id := range legacy.Modules { out.Modules[i]=scope(id) }
	for i,id := range legacy.Exclude { out.Exclude[i]=scope(id) }
	return MarshalProject(out)
}

// MigrateSchema1 is the explicit project migration entry point. Lock metadata
// migration is intentionally separate so a caller can journal both files and
// preserve the original manifest/payload digest bytes.
func MigrateSchema1(project []byte) ([]byte, error) { return MigrateSchema1Project(project) }
