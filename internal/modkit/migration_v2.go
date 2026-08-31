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

// MigrateSchema1Lock rewrites only lock metadata while retaining digest values.
func MigrateSchema1Lock(data []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil { return nil, fmt.Errorf("decode schema 1 lock: %w", err) }
	if schema, ok := root["schema"].(float64); !ok || int(schema) != 1 { return nil, fmt.Errorf("schema 1 lock required") }
	scope := func(id string) string { if strings.Count(id, "/") == 1 { return "ggg/" + id }; return id }
	if order, ok := root["order"].([]any); ok { for i,v := range order { if id,ok:=v.(string);ok {order[i]=scope(id)} } }
	if modules, ok := root["modules"].([]any); ok {
		for _, raw := range modules {
			m,ok:=raw.(map[string]any);if !ok {continue}
			if id,ok:=m["id"].(string);ok {m["id"]=scope(id)}
			if ids,ok:=m["required_by"].([]any);ok {for i,v:=range ids {if id,ok:=v.(string);ok {ids[i]=scope(id)}}}
			if manifest,ok:=m["manifest"].(map[string]any);ok {
				if id,ok:=manifest["id"].(string);ok {manifest["id"]=scope(id)}
				if reqs,ok:=manifest["requires"].([]any);ok {for i,v:=range reqs {if id,ok:=v.(string);ok {reqs[i]=map[string]any{"id":scope(id),"contract":map[string]any{"min":1,"max":1}}}}}
				if _,ok:=manifest["dependencies"];!ok {manifest["dependencies"]=map[string]any{"go":[]any{},"tools":[]any{},"containers":[]any{}}}
			}
			if _,ok:=m["registry_namespace"];!ok {m["registry_namespace"]="ggg"}
			if _,ok:=m["snapshot_sha256"];!ok {m["snapshot_sha256"]=m["source_commit"]}
		}
	}
	root["schema"]=2;root["registries"]=[]any{};root["snapshots"]=[]any{};root["runtime_orders"]=map[string]any{"development":root["order"],"test":root["order"],"production":root["order"]};root["dependencies"]=[]any{}
	return json.MarshalIndent(root, "", "  ")
}
