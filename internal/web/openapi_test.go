package web

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The spec is hand-written (no generator dependency), so a test — not good
// intentions — keeps it in step with the router. Same shape as the docs
// inventory check: scan the source of truth, compare sets, fail on drift.

type openAPIDoc struct {
	OpenAPI string                                  `yaml:"openapi"`
	Info    struct{ Title, Version string }         `yaml:"info"`
	Paths   map[string]map[string]any               `yaml:"paths"`
	Comps   struct{ Schemas map[string]any }        `yaml:"components"`
}

func loadSpec(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	require.NoError(t, yaml.Unmarshal(api.OpenAPISpec, &doc), "embedded spec must be valid YAML")
	return doc
}

// registeredAPIRoutes scans routes.go for the /api/v1 patterns the mux is
// given. Go's ServeMux exposes no route list, and the source IS the registry.
func registeredAPIRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^"]*)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out = append(out, strings.ToLower(m[1])+" "+m[2])
	}
	sort.Strings(out)
	return out
}

func specRoutes(t *testing.T, doc openAPIDoc) []string {
	t.Helper()
	var out []string
	for path, ops := range doc.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "put", "patch", "delete":
				out = append(out, method+" "+path)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestOpenAPISpecIsValidAndComplete(t *testing.T) {
	doc := loadSpec(t)
	assert.Equal(t, "3.1.0", doc.OpenAPI)
	assert.NotEmpty(t, doc.Info.Title)
	assert.NotEmpty(t, doc.Info.Version)
	for _, schema := range []string{"Project", "ChatMessage", "Error"} {
		assert.Contains(t, doc.Comps.Schemas, schema)
	}
}

func TestOpenAPISpecMatchesRegisteredRoutes(t *testing.T) {
	doc := loadSpec(t)
	assert.Equal(t, registeredAPIRoutes(t), specRoutes(t, doc),
		"every /api/v1 route must be documented and every documented path must be routed")
}

func TestOpenAPISpecServed(t *testing.T) {
	s := integrationServer(t, nil)

	// Unauthenticated: tooling reads the contract before holding a token.
	code, header, body := serve(t, s, "GET", "/api/v1/openapi.yaml", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, header.Get("Content-Type"), "application/yaml")
	assert.Contains(t, body, "openapi: 3.1.0")
	assert.Equal(t, string(api.OpenAPISpec), body, "served bytes are the embedded spec")
}

// The documented 200 shape must be the shape the handler actually returns —
// including NOT leaking internal columns (search_tsv lives on the sqlc row).
func TestOpenAPIProjectShapeMatchesHandler(t *testing.T) {
	s := integrationServer(t, nil)
	seedOrg(t, s, "org_spec", "Spec Org")
	_, err := s.q.CreateProject(t.Context(), sqlc.CreateProjectParams{ClerkOrgID: "org_spec", Name: "Spec project"})
	require.NoError(t, err)
	token := seedAPIToken(t, s, "org_spec", "read")

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	code, _, body := serve(t, s, "GET", "/api/v1/projects", nil, h)
	require.Equal(t, http.StatusOK, code)

	var envelope struct {
		Projects []map[string]any `json:"projects"`
		Limit    int              `json:"limit"`
		Offset   int              `json:"offset"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	require.Len(t, envelope.Projects, 1)

	documented := documentedProperties(t, "Project")
	for field := range envelope.Projects[0] {
		assert.Contains(t, documented, field, "undocumented field %q in the API payload", field)
	}
	for _, field := range documented {
		assert.Contains(t, envelope.Projects[0], field, "documented field %q missing from the payload", field)
	}
	assert.NotContains(t, envelope.Projects[0], "search_tsv", "internal FTS column must never be public")
}

func TestOpenAPIErrorShapeMatchesHandler(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/api/v1/projects", nil, nil) // no token
	require.Equal(t, http.StatusUnauthorized, code)

	var payload map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	require.Contains(t, payload, "error")
	assert.Contains(t, payload["error"], "code")
	assert.Contains(t, payload["error"], "message")
	assert.Len(t, payload, 1, "error envelope carries exactly one top-level key")
}

// documentedProperties reads the required property names of a component
// schema straight out of the spec.
func documentedProperties(t *testing.T, schema string) []string {
	t.Helper()
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(api.OpenAPISpec, &doc))
	s, ok := doc.Components.Schemas[schema]
	require.True(t, ok, "schema %s in spec", schema)
	out := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// seedAPIToken mints a token straight through the api helpers (the UI path
// is covered by api_test.go; this test only needs a valid credential).
func seedAPIToken(t *testing.T, s *Server, orgID, scope string) string {
	t.Helper()
	plaintext, hash, err := api.GenerateToken()
	require.NoError(t, err)
	_, err = s.q.InsertAPIToken(t.Context(), sqlc.InsertAPITokenParams{
		ClerkOrgID: orgID, Name: "spec-test", TokenHash: hash, Scope: scope,
	})
	require.NoError(t, err)
	return plaintext
}
