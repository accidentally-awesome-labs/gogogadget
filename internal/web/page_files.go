package web

import (
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const filesPageSize = 20

// filesData assembles the list view model (also used to re-render on a
// rejected upload).
func (s *Server) filesData(r *http.Request, orgID string, page int, limitHit bool) (templates.FilesData, error) {
	ctx := r.Context()
	total, err := s.q.CountFilesByOrg(ctx, orgID)
	if err != nil {
		return templates.FilesData{}, err
	}
	totalPages := max(int((total+filesPageSize-1)/filesPageSize), 1)
	files, err := s.q.ListFilesByOrg(ctx, sqlc.ListFilesByOrgParams{
		ClerkOrgID: orgID, Limit: filesPageSize, Offset: int32((page - 1) * filesPageSize),
	})
	if err != nil {
		return templates.FilesData{}, err
	}
	used, err := s.q.SumBytesByOrg(ctx, orgID)
	if err != nil {
		return templates.FilesData{}, err
	}
	return templates.FilesData{
		Files: files, Page: page, TotalPages: totalPages,
		Plan: identity.PlanFrom(ctx), UsedBytes: used, LimitHit: limitHit,
	}, nil
}

// GET /app/files — list page.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	d, err := s.filesData(r, org.ClerkOrgID, page, false)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	if wantsFragment(r) {
		s.Render(w, r, Page{Title: "Files", Layout: templates.LayoutApp}, templates.FilesTable(d))
		return
	}
	s.Render(w, r, Page{Title: "Files", Layout: templates.LayoutApp}, templates.FilesPage(d))
}
