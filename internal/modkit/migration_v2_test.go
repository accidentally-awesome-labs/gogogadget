package modkit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateSchema1ProjectMapsSelfHostDirectory(t *testing.T) {
	input := []byte(`{"schema":1,"registry":{"repository":".","path":"registry"},"modules":["profile/full","system/config"],"exclude":["component/chart"]}`)
	got, err := MigrateSchema1Project(input)
	if err != nil {
		t.Fatalf("MigrateSchema1Project: %v", err)
	}
	project, err := ParseProject(got)
	if err != nil {
		t.Fatalf("migrated project validation: %v", err)
	}
	if project.Registries[0].Source != "directory" || project.Registries[0].Path != "registry" {
		t.Fatalf("registry = %#v, want project-contained directory registry", project.Registries[0])
	}
	if project.Modules[0] != "ggg/profile/full" || project.Modules[1] != "ggg/system/config" || project.Exclude[0] != "ggg/component/chart" {
		t.Fatalf("scoped ids = %v / %v", project.Modules, project.Exclude)
	}
	if project.Deployment != "" || len(project.Providers) != 0 {
		t.Fatalf("schema1 migration fabricated provider/deployment choices: %#v %q", project.Providers, project.Deployment)
	}
}

func TestMigrateSchema1ProjectRefusesPlaceholderRemoteKey(t *testing.T) {
	input := []byte(`{"schema":1,"registry":{"repository":"gogogadget/gogogadget","ref":"v1","public_key":"core"},"modules":[],"exclude":[]}`)
	if _, err := MigrateSchema1Project(input); err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Fatalf("error = %v, want explicit real public key refusal", err)
	}
}

func TestMigrateSchema1LockPreservesPayloadDigestsAndEmbeddedMetadata(t *testing.T) {
	input := []byte(`{
  "schema":1,
  "registry_commit":"commit-a",
  "registry":{"source":"github","requested_ref":"v1","canonical_module":"github.com/gogogadget/gogogadget","key_fingerprint":"sha256:key"},
  "order":["system/config"],
  "modules":[{"id":"system/config","registry_namespace":"ggg","source_commit":"commit-a","snapshot_sha256":"snapshot-a","revision":1,"contract":1,"reason":"explicit","required_by":[],"manifest":{"id":"system/config","kind":"system","name":"config","revision":1,"contract":1,"title":"Config","description":"Config","requires":["system/apphost"],"files":[{"source":"registry/config.go","target":"internal/config/config.go","class":"go","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},"data":{},"removal_policy":"free"},"files":[],"migrations":[]}],
  "providers":{}
}`)
	got, err := MigrateSchema1Lock(input)
	if err != nil {
		t.Fatalf("MigrateSchema1Lock: %v", err)
	}
	var migrated map[string]any
	if err := json.Unmarshal(got, &migrated); err != nil {
		t.Fatalf("decode migrated lock: %v", err)
	}
	if migrated["schema"] != float64(2) {
		t.Fatalf("schema = %v, want 2", migrated["schema"])
	}
	if migrated["registry_commit"] != "commit-a" {
		t.Fatalf("registry_commit changed: %v", migrated["registry_commit"])
	}
	modules := migrated["modules"].([]any)
	module := modules[0].(map[string]any)
	if module["id"] != "ggg/system/config" || module["snapshot_sha256"] != "snapshot-a" {
		t.Fatalf("module metadata = %#v", module)
	}
	manifest := module["manifest"].(map[string]any)
	if manifest["id"] != "ggg/system/config" {
		t.Fatalf("manifest id = %v", manifest["id"])
	}
	requires := manifest["requires"].([]any)
	requirement := requires[0].(map[string]any)
	if requirement["id"] != "ggg/system/apphost" {
		t.Fatalf("requirement = %#v", requirement)
	}
	file := manifest["files"].([]any)[0].(map[string]any)
	if file["sha256"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("payload digest changed: %v", file["sha256"])
	}
	runtimeOrders := migrated["runtime_orders"].(map[string]any)
	if len(runtimeOrders["development"].([]any)) != 1 || runtimeOrders["development"].([]any)[0] != "ggg/system/config" {
		t.Fatalf("embedded runtime order = %#v", runtimeOrders)
	}
}

func TestMigrateSchema1CommandEmitsEnvelopeAndRollsBackAtomically(t *testing.T) {
	projectBefore := []byte(`{"schema":1,"registry":{"repository":".","path":"registry"},"modules":["profile/full"],"exclude":[]}`)
	lockBefore := []byte(`{"schema":1,"registry_commit":"commit-a","registry":{"source":"directory","requested_ref":"local","canonical_module":"github.com/gogogadget/gogogadget","key_fingerprint":""},"order":[],"modules":[]}`)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFileName), projectBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, LockFileName), lockBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cli := CLI{Root: root, Out: &out}
	if err := cli.Run(context.Background(), []string{"migrate", "schema-1", "--json"}); err != nil {
		t.Fatalf("migrate command: %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v\n%s", err, out.String())
	}
	if !envelope.OK || envelope.Exit != exitOK || envelope.Command != "migrate" {
		t.Fatalf("envelope = %#v", envelope)
	}
	migratedProject, err := os.ReadFile(filepath.Join(root, ProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	migratedLock, err := os.ReadFile(filepath.Join(root, LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseProject(migratedProject); err != nil {
		t.Fatalf("migrated project did not reparse: %v", err)
	}
	if _, err := ParseLock(migratedLock); err != nil {
		t.Fatalf("migrated lock did not reparse: %v", err)
	}
	if string(migratedProject) == string(projectBefore) || string(migratedLock) == string(lockBefore) {
		t.Fatal("migration did not rewrite schema metadata")
	}

	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFileName), projectBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, LockFileName), lockBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	writeCalls := 0
	cli = CLI{Root: root, writeFile: func(name string, data []byte, mode os.FileMode) error {
		writeCalls++
		if strings.HasSuffix(name, LockFileName) {
			return os.ErrPermission
		}
		return os.WriteFile(name, data, mode)
	}}
	err = cli.Run(context.Background(), []string{"migrate", "schema-1"})
	if err == nil || exitOf(t, err) != exitRollback {
		t.Fatalf("rollback error = %v (exit %d)", err, exitOf(t, err))
	}
	projectAfter, _ := os.ReadFile(filepath.Join(root, ProjectFileName))
	if !bytes.Equal(projectBefore, projectAfter) {
		t.Fatalf("project was not restored after lock write failure")
	}
	if writeCalls != 3 {
		t.Fatalf("write calls = %d, want project, lock, restore", writeCalls)
	}
}
