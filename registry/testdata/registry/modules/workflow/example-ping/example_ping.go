package jobs

import "context"

// The job half of the example workflow in registry/testdata. The generated
// dispatcher in internal/jobs/jobs_registry_gen.go calls
// w.defineExamplePing(), so an incorrect handler name in the manifest is a
// compile error on a named generated line rather than a job kind that silently
// never runs.

// ExamplePingPayload is the typed payload Define recovers for the handler, so
// the module never writes an unmarshal.
type ExamplePingPayload struct {
	Note string `json:"note"`
}

// defineExamplePing declares the kind, its schedulability and its attempt
// budget. Three attempts rather than the default eight: the example exists to
// show that a declared budget reaches declaredAttempts in the generated table.
func (w *Worker) defineExamplePing() Definition {
	return Define("example.ping", true, 3, func(ctx context.Context, p ExamplePingPayload) error {
		return nil
	})
}
