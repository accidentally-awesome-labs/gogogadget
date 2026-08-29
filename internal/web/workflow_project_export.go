package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
)

// POST /app/projects/export — enqueue the CSV export job; the notification
// carries the download link when it completes.
func (s *Server) handleProjectsExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	if err := jobs.Enqueue(ctx, s.q, jobs.KindExportProjectsCSV, jobs.ExportProjectsPayload{
		OrgID: org.ClerkOrgID, UserID: user.ClerkUserID,
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "projects.exported", nil)
	Toast(w, "success", "Export started — you'll get a notification when it's ready")
	w.WriteHeader(http.StatusOK)
}
