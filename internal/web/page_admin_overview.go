package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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
