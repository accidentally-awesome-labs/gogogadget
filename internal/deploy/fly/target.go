// Package fly identifies the Fly.io deployment integration inside the
// generated runtime. The typed remote workflow is layered on this constructor
// by the deployment slice.
package fly

import "context"

// Target is the deploy-fly deployment target handle.
type Target struct{}

// Deps carries the runtime capabilities the Fly deploy target consumes. None
// are required for genesis.
type Deps struct{}

// NewModule constructs the target with the standard runtime system shape so
// the generated bootstrap can compile it like any other system contribution.
func NewModule(ctx context.Context, _ any, _ Deps) (*Target, error) {
	return &Target{}, ctx.Err()
}
