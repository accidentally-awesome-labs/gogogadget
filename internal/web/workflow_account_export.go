package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
)

// accountExport is the GDPR self-serve payload: everything the platform
// holds about one user, across orgs.
type accountExport struct {
	ExportedAt    time.Time                 `json:"exported_at"`
	User          sqlc.User                 `json:"user"`
	Memberships   []exportMembership        `json:"memberships"`
	Notifications []sqlc.Notification       `json:"notifications"`
	Audit         []sqlc.ListAuditByUserRow `json:"audit"`
}

type exportMembership struct {
	OrgID   string    `json:"org_id"`
	OrgName string    `json:"org_name"`
	Role    string    `json:"role"`
	Since   time.Time `json:"since"`
}

// GET /app/settings/account/export — JSON download of the user's data.
func (s *Server) handleAccountExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	org := identity.OrgFrom(r.Context())

	orgs, err := s.q.GetOrgsForUser(ctx, user.ClerkUserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	export := accountExport{
		ExportedAt:    s.cfg.Now(),
		User:          *user,
		Memberships:   []exportMembership{},
		Notifications: []sqlc.Notification{},
		Audit:         []sqlc.ListAuditByUserRow{},
	}
	for _, o := range orgs {
		m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: o.ClerkOrgID, ClerkUserID: user.ClerkUserID})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		export.Memberships = append(export.Memberships, exportMembership{
			OrgID: o.ClerkOrgID, OrgName: o.Name, Role: m.Role, Since: m.CreatedAt.Time,
		})
		notes, err := s.q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
			ClerkOrgID: o.ClerkOrgID, ClerkUserID: user.ClerkUserID, Limit: 10000, Offset: 0,
		})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		export.Notifications = append(export.Notifications, notes...)
	}
	export.Audit, err = s.q.ListAuditByUser(ctx, sqlc.ListAuditByUserParams{
		UserID: pgtype.Text{String: user.ClerkUserID, Valid: true}, Lim: 10000,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	body, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgID := ""
	if org != nil {
		orgID = org.ClerkOrgID
	}
	audit.Log(ctx, s.q, orgID, user.ClerkUserID, "account.exported", nil)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="gogogadget-data-export.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
