package api

import _ "embed"

// OpenAPISpec is the machine-readable contract for /api/v1, embedded so the
// binary always serves the spec that shipped with it (no drift between a
// deployed build and a spec file living elsewhere).
//
// The document is generated from the same route declarations the router is
// built from: every /api/ route must carry exactly one operation, and each
// operation's security and idempotency parameter are derived from that route's
// declared scope and policy. So the contract cannot describe an endpoint that
// is not served, omit one that is, or claim an auth rule middleware does not
// enforce.
//
//go:embed openapi_registry_gen.yaml
var OpenAPISpec []byte
