// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls. This module provides the http.handler
// capability, which is what the runtime serves.
package web

import (
	"context"
	"fmt"
	"net/http"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/content"
)

// Module is the constructed HTTP surface.
type Module struct {
	Handler http.Handler
	// Server is the concrete surface, exposed so tests and dev-only routes can
	// reach it. Production takes only the handler.
	Server *Server
}

// NewModule assembles the HTTP surface from typed capabilities. Ambient values
// (logger, version) come from the host rather than the dependency graph, so no
// module has to provide them.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	switch {
	case d.Config == nil:
		return nil, fmt.Errorf("server: config dependency is required")
	case d.DB == nil:
		return nil, fmt.Errorf("server: database pool dependency is required")
	case d.Queries == nil:
		return nil, fmt.Errorf("server: queries dependency is required")
	case d.Storage == nil:
		return nil, fmt.Errorf("server: storage store capability is required")
	case d.Flags == nil:
		return nil, fmt.Errorf("server: flags evaluator capability is required")
	case d.Reporter == nil:
		return nil, fmt.Errorf("server: observability reporter capability is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if d.Log == nil {
		d.Log = h.Log()
	}
	if d.Version == "" {
		d.Version = h.Version()
	}
	if d.Docs == nil {
		docs, err := content.LoadDocs(contentfs.FS, d.Config.Production())
		if err != nil {
			return nil, fmt.Errorf("load docs: %w", err)
		}
		d.Docs = docs
	}

	server, err := NewServer(d)
	if err != nil {
		return nil, err
	}
	return &Module{Handler: server.Handler(), Server: server}, nil
}
