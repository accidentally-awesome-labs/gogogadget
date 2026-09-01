// Package identity defines the constructor-free identity provider seam.
package identity

import (
	"context"
	"errors"
)

// Deps are the complete provider-neutral identity capabilities supplied by a
// selected adapter. The seam never reads configuration or selects a provider.
type Deps struct {
	Verifier  Verifier
	Fetcher   UserFetcher
	Deleter   Deleter
	Navigator Navigator
	Webhook   Webhook
}

// Module groups the identity capabilities selected by the runtime graph.
type Module struct {
	Verifier  Verifier
	Fetcher   UserFetcher
	Deleter   Deleter
	Navigator Navigator
	Webhook   Webhook
}

// NewModule validates an explicitly supplied capability set. Provider
// construction and credential selection belong to adapter modules.
func NewModule(_ context.Context, d Deps) (*Module, error) {
	if d.Verifier == nil || d.Fetcher == nil || d.Deleter == nil ||
		d.Navigator == nil || d.Webhook == nil {
		return nil, errors.New("identity: all capabilities are required")
	}
	return &Module{
		Verifier:  d.Verifier,
		Fetcher:   d.Fetcher,
		Deleter:   d.Deleter,
		Navigator: d.Navigator,
		Webhook:   d.Webhook,
	}, nil
}
