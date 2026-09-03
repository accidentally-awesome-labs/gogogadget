package modkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// ParseProject decodes and validates canonical project intent JSON.
func ParseProject(data []byte) (Project, error) {
	var project Project
	if err := rejectDuplicateJSONFields(data); err != nil {
		return Project{}, fmt.Errorf("parse project: %w", err)
	}
	if err := decodeStrict(data, &project); err != nil {
		return Project{}, fmt.Errorf("parse project: %w", err)
	}
	if err := requireJSONValue(data, reflect.TypeOf(project), "project"); err != nil {
		return Project{}, fmt.Errorf("parse project: %w", err)
	}
	if err := validateProject(project, true); err != nil {
		return Project{}, err
	}
	return project, nil
}

// MarshalProject validates and emits canonical project intent JSON without
// changing the supplied value or its backing slices.
func MarshalProject(project Project) ([]byte, error) {
	clone := project
	if project.Modules != nil {
		clone.Modules = append(make([]string, 0, len(project.Modules)), project.Modules...)
	}
	if project.Exclude != nil {
		clone.Exclude = append(make([]string, 0, len(project.Exclude)), project.Exclude...)
	}
	// `ports` is deliberately not normalised to an empty object: a project
	// that declares no override must round-trip without gaining the key, so
	// writing intent never rewrites a file into a shape an older reader has
	// never seen.
	sort.Strings(clone.Modules)
	sort.Strings(clone.Exclude)
	if err := validateProject(clone, true); err != nil {
		return nil, err
	}
	return marshalIndented(clone)
}

// ParseLock decodes and validates canonical generated lock JSON. It is the one
// gate every lock reader passes, so the engine-contract refusal lives here: a
// lock written by a newer engine is rejected before any caller can plan,
// generate, or write from it. A lock written by an older engine is normal —
// rebuild then sync is the upgrade order — and passes silently.
func ParseLock(data []byte) (Lock, error) {
	var lock Lock
	if err := rejectDuplicateJSONFields(data); err != nil {
		return Lock{}, fmt.Errorf("parse lock: %w", err)
	}
	if err := decodeStrict(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("parse lock: %w", err)
	}
	if err := requireJSONValue(data, reflect.TypeOf(lock), "lock"); err != nil {
		return Lock{}, fmt.Errorf("parse lock: %w", err)
	}
	if err := EngineContractRefusal(lock.EngineContract); err != nil {
		return Lock{}, err
	}
	if err := validateLock(lock, true); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

// MarshalLock validates and emits canonical lock JSON. The dependency order is
// preserved; only order-insensitive collections are sorted on a deep clone.
func MarshalLock(lock Lock) ([]byte, error) {
	clone, err := cloneLock(lock)
	if err != nil {
		return nil, fmt.Errorf("clone lock: %w", err)
	}
	if clone.Registries == nil {
		clone.Registries = []LockedRegistry{}
	}
	if clone.Snapshots == nil {
		clone.Snapshots = []LockedSnapshot{}
	}
	if clone.Dependencies == nil {
		clone.Dependencies = []LockedDependency{}
	}
	if clone.Providers == nil {
		clone.Providers = map[string]ProviderSelections{}
	}
	if clone.RuntimeOrders.Development == nil {
		clone.RuntimeOrders.Development = append([]string{}, clone.Order...)
	}
	if clone.RuntimeOrders.Test == nil {
		clone.RuntimeOrders.Test = append([]string{}, clone.Order...)
	}
	if clone.RuntimeOrders.Production == nil {
		clone.RuntimeOrders.Production = append([]string{}, clone.Order...)
	}
	for i := range clone.Modules {
		if clone.Modules[i].Manifest.Dependencies.Go == nil {
			clone.Modules[i].Manifest.Dependencies.Go = []GoDependency{}
		}
		if clone.Modules[i].Manifest.Dependencies.Tools == nil {
			clone.Modules[i].Manifest.Dependencies.Tools = []ToolArtifact{}
		}
		if clone.Modules[i].Manifest.Dependencies.Containers == nil {
			clone.Modules[i].Manifest.Dependencies.Containers = []ContainerDependency{}
		}
		for j := range clone.Modules[i].Manifest.Environment {
			if clone.Modules[i].Manifest.Environment[j].Type == "" {
				clone.Modules[i].Manifest.Environment[j].Type = EnvString
			}
		}
	}
	// The lock records the engine that wrote it, so the value is stamped here
	// rather than carried in from a caller: no construction site can claim a
	// contract this binary does not implement.
	clone.EngineContract = EngineContract
	canonicalizeLock(&clone)
	if err := validateLock(clone, true); err != nil {
		return nil, err
	}
	return marshalIndented(clone)
}

func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON data")
	}
	return fmt.Errorf("trailing JSON data: %w", err)
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := walkJSONValue(decoder, first); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON data after %v", token)
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object field is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONValue(decoder, value); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object terminator")
		}
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONValue(decoder, value); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array terminator")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func requireJSONValue(data []byte, typ reflect.Type, field string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("%s must not be null", field)
	}

	// A json.RawMessage is deliberately shape-free: it carries an OpenAPI
	// fragment whose structure the schema, not this walker, defines. Walking it
	// as the byte slice it is would demand a JSON array.
	if typ == reflect.TypeOf(json.RawMessage{}) {
		if !json.Valid(data) {
			return fmt.Errorf("%s is not valid JSON", field)
		}
		return nil
	}

	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return fmt.Errorf("%s must be an object", field)
		}
		for i := range typ.NumField() {
			modelField := typ.Field(i)
			tag := strings.Split(modelField.Tag.Get("json"), ",")
			if len(tag) == 0 || tag[0] == "" || tag[0] == "-" {
				continue
			}
			optional := slices.Contains(tag[1:], "omitempty")
			raw, present := object[tag[0]]
			if !present {
				if optional {
					continue
				}
				return fmt.Errorf("%s field %q is required", field, tag[0])
			}
			if err := requireJSONValue(raw, modelField.Type, field+" "+tag[0]); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil || values == nil {
			return fmt.Errorf("%s must be an array", field)
		}
		for i, value := range values {
			if err := requireJSONValue(value, typ.Elem(), fmt.Sprintf("%s[%d]", field, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func marshalIndented(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func cloneLock(lock Lock) (Lock, error) {
	data, err := json.Marshal(lock)
	if err != nil {
		return Lock{}, err
	}
	var clone Lock
	if err := json.Unmarshal(data, &clone); err != nil {
		return Lock{}, err
	}
	return clone, nil
}

func canonicalizeLock(lock *Lock) {
	sort.Slice(lock.Modules, func(i, j int) bool { return lock.Modules[i].ID < lock.Modules[j].ID })
	for i := range lock.Modules {
		module := &lock.Modules[i]
		sort.Strings(module.RequiredBy)
		canonicalizeManifest(&module.Manifest)
		sort.Slice(module.Files, func(i, j int) bool { return module.Files[i].Path < module.Files[j].Path })
		sort.Slice(module.Migrations, func(i, j int) bool { return module.Migrations[i].ID < module.Migrations[j].ID })
		if module.Pending != nil {
			canonicalizeManifest(&module.Pending.Manifest)
			sort.Slice(module.Pending.Conflicts, func(i, j int) bool {
				return module.Pending.Conflicts[i].Path < module.Pending.Conflicts[j].Path
			})
		}
	}
}

func canonicalizeManifest(manifest *Manifest) {
	sort.Slice(manifest.Requires, func(i, j int) bool { return manifest.Requires[i].ID < manifest.Requires[j].ID })
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Target < manifest.Files[j].Target })
	sort.Slice(manifest.Dependencies.Go, func(i, j int) bool { return manifest.Dependencies.Go[i].Module < manifest.Dependencies.Go[j].Module })
	sort.Slice(manifest.Dependencies.Tools, func(i, j int) bool {
		return manifest.Dependencies.Tools[i].InstallPath < manifest.Dependencies.Tools[j].InstallPath
	})
	sort.Slice(manifest.Dependencies.Containers, func(i, j int) bool {
		return manifest.Dependencies.Containers[i].Name < manifest.Dependencies.Containers[j].Name
	})
	sort.Slice(manifest.Migrations, func(i, j int) bool { return manifest.Migrations[i].ID < manifest.Migrations[j].ID })
	sort.Slice(manifest.Environment, func(i, j int) bool { return manifest.Environment[i].Key < manifest.Environment[j].Key })
	sort.Slice(manifest.Docs, func(i, j int) bool { return manifest.Docs[i].Path < manifest.Docs[j].Path })
	sort.Slice(manifest.Data, func(i, j int) bool {
		left := manifest.Data[i].Table + "\x00" + manifest.Data[i].RowDiscriminator
		right := manifest.Data[j].Table + "\x00" + manifest.Data[j].RowDiscriminator
		return left < right
	})
	canonicalizeClaims(&manifest.Claims)
	canonicalizeRuntime(&manifest.Runtime)
	canonicalizeTests(&manifest.Tests)
	for i := range manifest.Data {
		sort.Strings(manifest.Data[i].ExternalObjects)
		sort.Strings(manifest.Data[i].PersistedJobs)
	}
}
func canonicalizeClaims(claims *NamespaceClaims) {
	sets := []*[]string{
		&claims.Packages, &claims.Routes, &claims.Jobs, &claims.Environment, &claims.I18n,
		&claims.Queries, &claims.OpenAPI, &claims.ContentTypes, &claims.UI, &claims.Assets, &claims.Data,
		&claims.ProviderSlots, &claims.Provisioners, &claims.DatabaseOps, &claims.CLI, &claims.Deploy,
	}
	for _, set := range sets {
		sort.Strings(*set)
	}
}

func canonicalizeRuntime(runtime *RuntimeContributions) {
	if runtime.System != nil {
		sort.Slice(runtime.System.Needs, func(i, j int) bool { return runtime.System.Needs[i].Field < runtime.System.Needs[j].Field })
		sort.Slice(runtime.System.Provides, func(i, j int) bool { return runtime.System.Provides[i].Field < runtime.System.Provides[j].Field })
	}
	sort.Slice(runtime.Routes, func(i, j int) bool { return runtime.Routes[i].ID < runtime.Routes[j].ID })
	sort.Slice(runtime.Jobs, func(i, j int) bool { return runtime.Jobs[i].Kind < runtime.Jobs[j].Kind })
	sort.Slice(runtime.ContentTypes, func(i, j int) bool { return runtime.ContentTypes[i].ID < runtime.ContentTypes[j].ID })
	sort.Slice(runtime.Navigation, func(i, j int) bool { return runtime.Navigation[i].ID < runtime.Navigation[j].ID })
	sort.Slice(runtime.Slots, func(i, j int) bool { return runtime.Slots[i].ID < runtime.Slots[j].ID })
	sort.Slice(runtime.UI, func(i, j int) bool { return runtime.UI[i].Name < runtime.UI[j].Name })
	sort.Slice(runtime.Assets, func(i, j int) bool { return runtime.Assets[i].ID < runtime.Assets[j].ID })
	sort.Slice(runtime.CLI, func(i, j int) bool { return runtime.CLI[i].Name < runtime.CLI[j].Name })
	for i := range runtime.ContentTypes {
		sort.Strings(runtime.ContentTypes[i].Paths)
	}
	for i := range runtime.Navigation {
		sort.Strings(runtime.Navigation[i].Before)
		sort.Strings(runtime.Navigation[i].After)
		sort.Strings(runtime.Navigation[i].Roles)
		sort.Strings(runtime.Navigation[i].Flags)
	}
	for i := range runtime.Slots {
		sort.Strings(runtime.Slots[i].Before)
		sort.Strings(runtime.Slots[i].After)
	}
	for i := range runtime.Assets {
		sort.Strings(runtime.Assets[i].Before)
		sort.Strings(runtime.Assets[i].After)
	}
}

func canonicalizeTests(tests *TestMetadata) {
	sets := []*[]string{
		&tests.GoPackages, &tests.Smoke, &tests.E2E, &tests.Visual, &tests.Accessibility, &tests.Capabilities,
	}
	for _, set := range sets {
		sort.Strings(*set)
	}
}
