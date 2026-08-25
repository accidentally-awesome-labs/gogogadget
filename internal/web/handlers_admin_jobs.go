package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/jobs — job queue viewer with kind filter (admin only via adminChain).
func (s *Server) handleAdminJobs(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	ctx := r.Context()
	filter := r.URL.Query().Get("q")

	total, err := s.q.CountJobs(ctx, filter)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/adminPageSize)), 1)

	rows, err := s.q.ListJobs(ctx, sqlc.ListJobsParams{
		Filter: filter,
		Off:    int32((page - 1) * adminPageSize),
		Lim:    adminPageSize,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	// Fragment for htmx search/pagination; full page otherwise (the fragment rule).
	if wantsFragment(r) {
		s.Render(w, r, Page{Title: i18n.T(ctx, "admin.jobs.title"), Layout: templates.LayoutAdmin},
			templates.AdminJobsTable(rows, s.cfg.Now(), filter, page, totalPages))
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.jobs.title"), Layout: templates.LayoutAdmin},
		templates.AdminJobsPage(rows, s.cfg.Now(), filter, page, totalPages))
}
