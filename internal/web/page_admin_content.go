package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const contentPageSize = 20

// GET /admin/content — one table across every registered type. kind is a
// query parameter, so a newly declared type needs no routing change.
func (s *Server) handleAdminContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	if kind != "" {
		if _, ok := s.types.Get(kind); !ok {
			s.handleNotFound(w, r)
			return
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total, err := s.q.CountEntriesAdmin(ctx, sqlc.CountEntriesAdminParams{Kind: kind, Filter: query})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/contentPageSize)), 1)
	items, err := s.q.ListEntriesAdmin(ctx, sqlc.ListEntriesAdminParams{
		Kind: kind, Filter: query, Lim: contentPageSize, Off: int32((page - 1) * contentPageSize),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d := templates.ContentListData{
		Items: items, Types: s.types.All(), Kind: kind, Query: query,
		Page: page, TotalPages: totalPages, Now: s.cfg.Now,
	}
	pageData := Page{Title: i18n.T(ctx, "admin.content.title"), Layout: templates.LayoutAdmin}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.AdminContentTable(d))
		return
	}
	s.Render(w, r, pageData, templates.AdminContentPage(d))
}
