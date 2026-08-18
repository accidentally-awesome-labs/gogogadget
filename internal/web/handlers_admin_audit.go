package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/audit — platform-wide paginated audit trail (admin only via adminChain).
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	ctx := r.Context()
	filter := r.URL.Query().Get("q")

	total, err := s.q.CountAuditAll(ctx, filter)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/adminPageSize)), 1)

	rows, err := s.q.ListAuditAll(ctx, sqlc.ListAuditAllParams{
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
		s.Render(w, r, Page{Title: i18n.T(ctx, "admin.audit.title"), Layout: templates.LayoutAdmin},
			templates.AdminAuditTable(rows, s.cfg.Now(), filter, page, totalPages))
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.audit.title"), Layout: templates.LayoutAdmin},
		templates.AdminAuditPage(rows, s.cfg.Now(), filter, page, totalPages))
}
