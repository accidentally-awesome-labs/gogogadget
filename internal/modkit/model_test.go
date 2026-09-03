package modkit

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

const canonicalProjectJSON = `{
  "schema": 2,
  "registries": [{"namespace":"ggg","source":"github","repository":"gogogadget/gogogadget","ref":"main","public_key":"A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}],
  "modules": ["ggg/component/card","ggg/profile/full"],
  "exclude": ["ggg/component/chart"],
  "providers": {},
  "deployment": ""
}
`

const canonicalLockJSON = `{
  "schema": 2,
  "registry_commit": "0123456789abcdef0123456789abcdef01234567",
  "registries": [],
  "snapshots": [],
  "runtime_orders": {"development":["ggg/element/button"],"test":["ggg/element/button"],"production":["ggg/element/button"]},
  "dependencies": [],
  "order": [
    "ggg/element/button"
  ],
  "modules": [
    {
      "id": "ggg/element/button",
      "revision": 1,
      "contract": 1,
      "registry_namespace": "ggg",
      "snapshot_sha256": "0123456789abcdef0123456789abcdef01234567",
      "source_commit": "0123456789abcdef0123456789abcdef01234567",
      "reason": "explicit",
      "required_by": [],
      "manifest": {
        "id": "ggg/element/button",
        "kind": "element",
        "name": "button",
        "revision": 1,
        "contract": 1,
        "title": "Button",
        "description": "Typed button renderer.",
        "requires": [],
        "files": [
          {
            "source": "registry/modules/element/button/button.templ",
            "target": "internal/web/templates/ui/button.templ",
            "class": "templ",
            "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "rewrite_module": true,
            "contract": true
          }
        ],
        "claims": {},
        "runtime": {},
        "migrations": [],
        "environment": [],
        "docs": [],
        "tests": {},
        "data": [],
        "dependencies": {"go":[],"tools":[],"containers":[]},
        "removal_policy": "free"
      },
      "files": [
        {
          "path": "internal/web/templates/ui/button.templ",
          "source": "registry/modules/element/button/button.templ",
          "base_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "local_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          "state": "modified"
        }
      ],
      "migrations": []
    }
  ]
}
`

func TestParseProject(t *testing.T) {
	project, err := ParseProject([]byte(canonicalProjectJSON))
	if err != nil {
		t.Fatalf("ParseProject(canonical): %v", err)
	}
	if got, want := project.Registries[0].Repository, "gogogadget/gogogadget"; got != want {
		t.Fatalf("repository = %q, want %q", got, want)
	}
	if got, want := strings.Join(project.Modules, ","), "ggg/component/card,ggg/profile/full"; got != want {
		t.Fatalf("modules = %q, want %q", got, want)
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "unknown top-level field",
			json: strings.Replace(canonicalProjectJSON, `"schema": 2,`, `"schema": 2, "extra": true,`, 1),
			want: "unknown field",
		},
		{
			name: "duplicate top-level field",
			json: strings.Replace(canonicalProjectJSON, `"schema": 2,`, `"schema": 2, "schema": 2,`, 1),
			want: "duplicate",
		},
		{
			name: "trailing document",
			json: canonicalProjectJSON + "{}",
			want: "trailing",
		},
		{
			name: "missing required field",
			json: `{"schema":2,"registries":[],"modules":[],"providers":{},"deployment":""}`,
			want: "exclude",
		},
		{
			name: "unsupported schema",
			json: strings.Replace(canonicalProjectJSON, `"schema": 2`, `"schema": 3`, 1),
			want: "schema",
		},
		{
			name: "unsorted modules",
			json: strings.Replace(canonicalProjectJSON, "\"ggg/component/card\",\"ggg/profile/full\"", "\"ggg/profile/full\",\"ggg/component/card\"", 1),
			want: "modules",
		},
		{
			name: "duplicate modules",
			json: strings.Replace(canonicalProjectJSON, `"ggg/profile/full"`, `"ggg/component/card"`, 1),
			want: "duplicate",
		},
		{
			name: "invalid module id",
			json: strings.Replace(canonicalProjectJSON, `"ggg/component/card"`, `"Component/Card"`, 1),
			want: "module",
		},
		{
			name: "exclude requires profile",
			json: strings.Replace(canonicalProjectJSON, "\"ggg/component/card\",\"ggg/profile/full\"", "\"ggg/component/card\"", 1),
			want: "exclude",
		},
		{
			name: "arrays are required",
			json: strings.Replace(canonicalProjectJSON, "\"exclude\": [\"ggg/component/chart\"]", "\"exclude\": null", 1),
			want: "exclude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProject([]byte(tt.json))
			if err == nil {
				t.Fatal("ParseProject returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestMarshalProjectCanonical(t *testing.T) {
	project := Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/profile/full", "ggg/component/card"},
		Exclude: []string{"ggg/component/chart"},
	}

	got, err := MarshalProject(project)
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	if _, err := ParseProject(got); err != nil {
		t.Fatalf("MarshalProject output is not parseable: %v", err)
	}
	if got := strings.Join(project.Modules, ","); got != "ggg/profile/full,ggg/component/card" {
		t.Fatalf("MarshalProject mutated caller modules: %q", got)
	}
}

func TestParseLock(t *testing.T) {
	lock, err := ParseLock([]byte(canonicalLockJSON))
	if err != nil {
		t.Fatalf("ParseLock(canonical): %v", err)
	}
	if got, want := lock.Modules[0].Manifest.ID, "ggg/element/button"; got != want {
		t.Fatalf("manifest id = %q, want %q", got, want)
	}
	if got, want := lock.Modules[0].Files[0].State, FileModified; got != want {
		t.Fatalf("file state = %q, want %q", got, want)
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "unknown field",
			json: strings.Replace(canonicalLockJSON, `"schema": 2,`, `"schema": 2, "extra": true,`, 1),
			want: "unknown field",
		},
		{
			name: "duplicate nested field",
			json: strings.Replace(canonicalLockJSON, `"revision": 1,`, `"revision": 1, "revision": 1,`, 1),
			want: "duplicate",
		},
		{
			name: "trailing document",
			json: canonicalLockJSON + "{}",
			want: "trailing",
		},
		{
			name: "manifest identity mismatch",
			json: strings.Replace(canonicalLockJSON, "\"id\": \"ggg/element/button\",\n        \"kind\"", "\"id\": \"ggg/element/link\",\n        \"kind\"", 1),
			want: "manifest",
		},
		{
			name: "invalid digest",
			json: strings.Replace(canonicalLockJSON, `"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"sha256": "bad"`, 1),
			want: "sha256",
		},
		{
			name: "invalid file state",
			json: strings.Replace(canonicalLockJSON, `"state": "modified"`, `"state": "dirty"`, 1),
			want: "state",
		},
		{
			name: "unsafe target path",
			json: strings.Replace(canonicalLockJSON, `"path": "internal/web/templates/ui/button.templ"`, `"path": "../button.templ"`, 1),
			want: "path",
		},
		{
			name: "order must cover modules",
			json: strings.Replace(canonicalLockJSON, "\"ggg/element/button\"\n  ],", "\"ggg/element/link\"\n  ],", 1),
			want: "order",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLock([]byte(tt.json))
			if err == nil {
				t.Fatal("ParseLock returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestMarshalLockCanonical(t *testing.T) {
	lock, err := ParseLock([]byte(canonicalLockJSON))
	if err != nil {
		t.Fatalf("ParseLock(canonical): %v", err)
	}

	got, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock: %v", err)
	}
	if _, err := ParseLock(got); err != nil {
		t.Fatalf("MarshalLock output is not parseable: %v", err)
	}
}

// The engine contract is asymmetric on purpose. A lock written by a newer
// engine assumes capabilities, generators, or invariants this binary does not
// implement, so reading it produces a confident wrong answer — the stale
// `bin/ggg` that reported a missing runtime.health provider on a healthy tree.
// A lock written by an older engine is the normal upgrade order (rebuild, then
// sync) and must pass silently, including a lock predating the field.
func TestParseLockRefusesOnlyANewerEngineContract(t *testing.T) {
	withContract := func(value string) []byte {
		return []byte(strings.Replace(canonicalLockJSON,
			`"schema": 2,`, `"schema": 2,`+"\n  "+`"engine_contract": `+value+`,`, 1))
	}

	t.Run("absent proceeds", func(t *testing.T) {
		lock, err := ParseLock([]byte(canonicalLockJSON))
		if err != nil {
			t.Fatalf("ParseLock(no engine_contract): %v", err)
		}
		if lock.EngineContract != 0 {
			t.Fatalf("EngineContract = %d, want 0 for a lock predating the field", lock.EngineContract)
		}
	})

	t.Run("older proceeds", func(t *testing.T) {
		if _, err := ParseLock(withContract("1")); err != nil {
			t.Fatalf("ParseLock(engine_contract 1): %v", err)
		}
	})

	t.Run("equal proceeds", func(t *testing.T) {
		lock, err := ParseLock(withContract(strconv.Itoa(EngineContract)))
		if err != nil {
			t.Fatalf("ParseLock(engine_contract %d): %v", EngineContract, err)
		}
		if lock.EngineContract != EngineContract {
			t.Fatalf("EngineContract = %d, want %d", lock.EngineContract, EngineContract)
		}
	})

	t.Run("newer refuses with the remedy", func(t *testing.T) {
		_, err := ParseLock(withContract(strconv.Itoa(EngineContract + 1)))
		var stale EngineContractError
		if !errors.As(err, &stale) {
			t.Fatalf("ParseLock error = %v, want EngineContractError", err)
		}
		if stale.Lock != EngineContract+1 || stale.Binary != EngineContract {
			t.Fatalf("EngineContractError = %+v, want lock %d binary %d", stale, EngineContract+1, EngineContract)
		}
		if stale.ExitCode() != ExitRefusal {
			t.Fatalf("ExitCode = %d, want %d", stale.ExitCode(), ExitRefusal)
		}
		for _, want := range []string{
			LockFileName,
			"engine contract " + strconv.Itoa(EngineContract+1),
			"contract " + strconv.Itoa(EngineContract),
			"go build -o bin/ggg ./cmd/ggg",
			"make setup",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q is missing %q", err, want)
			}
		}
	})
}

// A written lock always records this binary's contract: the value is stamped by
// MarshalLock, so no construction site can claim a contract it does not
// implement, and the recorded value never lags the engine that produced it.
func TestMarshalLockStampsThisEnginesContract(t *testing.T) {
	lock, err := ParseLock([]byte(canonicalLockJSON))
	if err != nil {
		t.Fatalf("ParseLock(canonical): %v", err)
	}
	lock.EngineContract = 1
	data, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock: %v", err)
	}
	if !strings.Contains(string(data), `"engine_contract": `+strconv.Itoa(EngineContract)+`,`) {
		t.Fatalf("marshalled lock does not record engine_contract %d:\n%s", EngineContract, data)
	}
	reparsed, err := ParseLock(data)
	if err != nil {
		t.Fatalf("ParseLock(marshalled): %v", err)
	}
	if reparsed.EngineContract != EngineContract {
		t.Fatalf("round-tripped EngineContract = %d, want %d", reparsed.EngineContract, EngineContract)
	}
}
