package web

import (
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

// GET /app/settings/billing
func (s *Server) handleSettingsBilling(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	plan := identity.PlanFrom(ctx)
	sub := identity.SubFrom(ctx)

	// Success race: redirected back from checkout before the webhook landed →
	// render a polling fragment instead of asserting on immediate state.
	processing := r.URL.Query().Get("success") == "1" && (sub == nil || sub.Status == "incomplete")

	count, err := s.q.CountProjectsByOrg(ctx, org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	usedBytes, err := s.q.SumBytesByOrg(ctx, org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	meterUsage := map[string]int64{}
	if len(plan.Meters) > 0 {
		monthStart := time.Date(s.cfg.Now().Year(), s.cfg.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
		for _, m := range plan.Meters {
			v, err := s.q.SumUsageByNameSince(ctx, sqlc.SumUsageByNameSinceParams{
				OrgID: org.OrgID, Name: m.Key, CreatedAt: pgtype.Timestamptz{Time: monthStart, Valid: true},
			})
			if err != nil {
				s.renderError(w, r, err.Error())
				return
			}
			meterUsage[m.Key] = v
		}
	}
	s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp}, templates.SettingsBilling(templates.BillingData{
		Plan: plan, Sub: sub, ProjectCount: count, UsedStorageBytes: usedBytes, MeterUsage: meterUsage,
		Plans: s.billingCatalog.All(), Processing: processing,
		Success: r.URL.Query().Get("success") == "1",
	}))
}
