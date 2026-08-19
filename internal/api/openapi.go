package api

import _ "embed"

// OpenAPISpec is the machine-readable contract for /api/v1, embedded so the
// binary always serves the spec that shipped with it (no drift between a
// deployed build and a spec file living elsewhere). The parity test in
// internal/web keeps it honest against the registered routes.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
