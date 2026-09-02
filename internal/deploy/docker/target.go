// Package docker identifies the local Docker Compose deployment integration
// inside the generated runtime. Remote mutation methods are attached by the
// deployment workflow slice; project genesis can select and compile this
// target without shell hooks.
package docker

import "context"

// Target is the deploy-docker deployment target handle.
type Target struct{}

// Deps carries the runtime capabilities the Docker deploy target consumes.
// None are required for genesis.
type Deps struct{}

// NewModule constructs the target with the standard runtime system shape so
// the generated bootstrap can compile it like any other system contribution.
func NewModule(ctx context.Context, _ any, _ Deps) (*Target, error) {
	return &Target{}, ctx.Err()
}
