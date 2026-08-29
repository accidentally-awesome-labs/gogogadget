package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/users — search + pagination.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total, err := s.q.CountUsers(ctx, q)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/adminPageSize)), 1)
	users, err := s.q.ListUsers(ctx, sqlc.ListUsersParams{Column1: q, Limit: adminPageSize, Offset: int32((page - 1) * adminPageSize)})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d := templates.AdminUsersData{Users: users, Query: q, Page: page, TotalPages: totalPages}
	pageData := Page{Title: "Users", Layout: templates.LayoutAdmin}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.AdminUsersTable(d))
		return
	}
	s.Render(w, r, pageData, templates.AdminUsersPage(d))
}
