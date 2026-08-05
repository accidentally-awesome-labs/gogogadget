package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

const adminPageSize = 20

// GET /admin — site stats + recent signups.
func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.q.CountUsers(ctx, "")
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgs, err := s.q.CountOrgs(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	activeSubs, err := s.q.CountActiveSubscriptions(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	revRows, err := s.q.ListRevenueSubscriptions(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	signups, err := s.q.ListUsers(ctx, sqlc.ListUsersParams{Column1: "", Limit: 10, Offset: 0})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Admin", Layout: templates.LayoutAdmin}, templates.AdminHome(templates.AdminHomeData{
		TotalUsers: users, TotalOrgs: orgs, ActiveSubs: activeSubs,
		MRR: billing.MRR(revRows), RecentSignups: signups, Now: s.cfg.Now(),
	}))
}

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

// POST /admin/users/{id}/disable — toggle disabled_at; the target's next
// request hits RequireNotDisabled → 403.
func (s *Server) handleAdminUserDisable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := identity.UserFrom(r.Context())
	id := r.PathValue("id")
	user, err := s.q.GetUserByClerkID(ctx, id)
	if err != nil {
		s.handleNotFound(w, r)
		return
	}
	var disabledAt pgtype.Timestamptz
	action := "admin.user_disabled"
	if !user.DisabledAt.Valid {
		disabledAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	} else {
		action = "admin.user_enabled"
	}
	if err := s.q.SetUserDisabled(ctx, sqlc.SetUserDisabledParams{ClerkUserID: id, DisabledAt: disabledAt}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, "", admin.ClerkUserID, action, map[string]any{"target": id, "email": string(user.Email)})
	if disabledAt.Valid {
		Toast(w, "success", string(user.Email)+" disabled")
	} else {
		Toast(w, "success", string(user.Email)+" re-enabled")
	}
	Navigate(w, r, "/admin/users")
}

// GET /admin/orgs — member counts + plan badges.
func (s *Server) handleAdminOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.q.ListOrgsWithStats(r.Context(), sqlc.ListOrgsWithStatsParams{Limit: 100, Offset: 0})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Organizations", Layout: templates.LayoutAdmin},
		templates.AdminOrgsPage(templates.AdminOrgsData{Orgs: orgs}))
}
