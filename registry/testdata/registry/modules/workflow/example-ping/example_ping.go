package jobs

import "context"

// The job half of the example workflow in registry/testdata. The generated
// dispatcher in internal/jobs/jobs_registry_gen.go calls
// Define("example.ping", true, 3, w.runExamplePing) — kind, schedulability and
// attempt budget all read off the manifest — so an incorrect handler name in
// the declaration is a compile error on a named generated line rather than a
// job kind that silently never runs, and the payload cannot state a budget the
// declaration disagrees with.

// ExamplePingPayload is the typed payload Define recovers for the handler, so
// the module never writes an unmarshal.
type ExamplePingPayload struct {
	Note string `json:"note"`
}

// runExamplePing is the whole module-side of a job kind: one typed handler.
// Three attempts rather than the default eight is declared in the manifest,
// which is where the example shows a declared budget reaching declaredAttempts.
func (w *Worker) runExamplePing(ctx context.Context, p ExamplePingPayload) error {
	return nil
}
