package web

import (
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/i18n"
)

// POST /admin/jobs/{id}/requeue — revive a dead-lettered job.
//
// The mutation lives with the workflow that declares its route rather than with
// the page that links to it: the viewer is read-only and can be installed
// without granting anyone the ability to re-run failed work.
func (s *Server) handleAdminJobRequeue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if err := s.q.RequeueDeadJob(ctx, id); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	Toast(w, "success", i18n.T(ctx, "admin.jobs.requeued"))
	Navigate(w, r, "/admin/jobs")
}
