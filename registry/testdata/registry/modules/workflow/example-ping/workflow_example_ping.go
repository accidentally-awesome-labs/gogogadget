package web

import "net/http"

// The transport half of the example workflow in registry/testdata. A workflow
// module owns its mutation handler, its job definition, its query fragment, its
// table declaration and its migration; this file is only the HTTP edge.
//
// The response is 202 with no body on purpose: the point of the example is the
// declarations the manifest carries, and inventing persistence here would make
// the fixture depend on a database the validator never connects to.
func (s *Server) handleExamplePing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
}
