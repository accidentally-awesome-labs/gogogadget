package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const activityPageSize = 20

// GET /app/activity — org-scoped paginated audit trail.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	ctx := r.Context()
	orgParam := sqlcText(org.OrgID)

	total, err := s.q.CountAuditByOrg(ctx, orgParam)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/activityPageSize)), 1)

	rows, err := s.q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{
		OrgID: orgParam,
		Limit:      activityPageSize,
		Offset:     int32((page - 1) * activityPageSize),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	// Fragment for htmx pagination; full page otherwise (the fragment rule).
	if wantsFragment(r) {
		s.Render(w, r, Page{Title: "Activity", Layout: templates.LayoutApp},
			templates.ActivityTable(rows, s.cfg.Now(), page, totalPages))
		return
	}
	s.Render(w, r, Page{Title: "Activity", Layout: templates.LayoutApp},
		templates.ActivityPage(rows, s.cfg.Now(), page, totalPages))
}
