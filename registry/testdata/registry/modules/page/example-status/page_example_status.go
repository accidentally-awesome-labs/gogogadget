package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// The static-page half of the example closure in registry/testdata. It is a
// normal page module: one handler method on *Server, one templ page, its own
// translations, and a declared route the generated route table wires up. The
// module adds no central-file edit anywhere — that is the property
// `ggg registry validate` exists to prove.
//
// The canonical import prefix above is rewritten to the target module path when
// this payload is installed into a derivative whose go.mod differs, which is why
// the manifest marks the file rewrite_module.
func (s *Server) handleExampleStatus(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "Example status", Layout: templates.LayoutPublic}, templates.ExampleStatus())
}
