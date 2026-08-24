package modkit

import (
	"strings"
	"testing"
)

const canonicalProjectJSON = `{
  "schema": 1,
  "registry": {
    "repository": "gogogadget/gogogadget",
    "ref": "main"
  },
  "modules": [
    "component/card",
    "profile/full"
  ],
  "exclude": [
    "component/chart"
  ]
}
`

const canonicalLockJSON = `{
  "schema": 1,
  "registry_commit": "0123456789abcdef0123456789abcdef01234567",
  "order": [
    "element/button"
  ],
  "modules": [
    {
      "id": "element/button",
      "revision": 1,
      "contract": 1,
      "source_commit": "0123456789abcdef0123456789abcdef01234567",
      "reason": "explicit",
      "required_by": [],
      "manifest": {
        "id": "element/button",
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
	if got, want := project.Registry.Repository, "gogogadget/gogogadget"; got != want {
		t.Fatalf("repository = %q, want %q", got, want)
	}
	if got, want := strings.Join(project.Modules, ","), "component/card,profile/full"; got != want {
		t.Fatalf("modules = %q, want %q", got, want)
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "unknown top-level field",
			json: strings.Replace(canonicalProjectJSON, `"schema": 1,`, `"schema": 1, "extra": true,`, 1),
			want: "unknown field",
		},
		{
			name: "duplicate top-level field",
			json: strings.Replace(canonicalProjectJSON, `"schema": 1,`, `"schema": 1, "schema": 1,`, 1),
			want: "duplicate",
		},
		{
			name: "trailing document",
			json: canonicalProjectJSON + "{}",
			want: "trailing",
		},
		{
			name: "missing required field",
			json: `{"schema":1,"registry":{"repository":"gogogadget/gogogadget","ref":"main"},"modules":[]}`,
			want: "exclude",
		},
		{
			name: "unsupported schema",
			json: strings.Replace(canonicalProjectJSON, `"schema": 1`, `"schema": 2`, 1),
			want: "schema",
		},
		{
			name: "unsorted modules",
			json: strings.Replace(canonicalProjectJSON, "\"component/card\",\n    \"profile/full\"", "\"profile/full\",\n    \"component/card\"", 1),
			want: "modules",
		},
		{
			name: "duplicate modules",
			json: strings.Replace(canonicalProjectJSON, `"profile/full"`, `"component/card"`, 1),
			want: "duplicate",
		},
		{
			name: "invalid module id",
			json: strings.Replace(canonicalProjectJSON, `"component/card"`, `"Component/Card"`, 1),
			want: "module",
		},
		{
			name: "exclude requires profile",
			json: strings.Replace(canonicalProjectJSON, "    \"component/card\",\n    \"profile/full\"", "    \"component/card\"", 1),
			want: "exclude",
		},
		{
			name: "arrays are required",
			json: strings.Replace(canonicalProjectJSON, "\"exclude\": [\n    \"component/chart\"\n  ]", "\"exclude\": null", 1),
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
		Schema: 1,
		Registry: ProjectRegistry{
			Repository: "gogogadget/gogogadget",
			Ref:        "main",
		},
		Modules: []string{"profile/full", "component/card"},
		Exclude: []string{"component/chart"},
	}

	got, err := MarshalProject(project)
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	if string(got) != canonicalProjectJSON {
		t.Fatalf("MarshalProject() =\n%s\nwant:\n%s", got, canonicalProjectJSON)
	}
	if got := strings.Join(project.Modules, ","); got != "profile/full,component/card" {
		t.Fatalf("MarshalProject mutated caller modules: %q", got)
	}
}

func TestParseLock(t *testing.T) {
	lock, err := ParseLock([]byte(canonicalLockJSON))
	if err != nil {
		t.Fatalf("ParseLock(canonical): %v", err)
	}
	if got, want := lock.Modules[0].Manifest.ID, "element/button"; got != want {
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
			json: strings.Replace(canonicalLockJSON, `"schema": 1,`, `"schema": 1, "extra": true,`, 1),
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
			json: strings.Replace(canonicalLockJSON, "\"id\": \"element/button\",\n        \"kind\"", "\"id\": \"element/link\",\n        \"kind\"", 1),
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
			json: strings.Replace(canonicalLockJSON, "\"element/button\"\n  ],", "\"element/link\"\n  ],", 1),
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
	if string(got) != canonicalLockJSON {
		t.Fatalf("MarshalLock() =\n%s\nwant:\n%s", got, canonicalLockJSON)
	}
}
