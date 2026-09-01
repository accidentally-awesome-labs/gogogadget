package web

import (
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

func TestAdminEmailGrantsRoleOnFirstSessionSight(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.AdminEmail = "user_first_admin@gogogadget.dev" })
	code, _, _ := serve(t, s, http.MethodGet, "/app", nil, nil, sessionCookie("user_first_admin", "org_first_admin", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	mapping, err := s.q.GetIdentitySubject(t.Context(), sqlc.GetIdentitySubjectParams{Provider: "dev", Subject: "user_first_admin"})
	require.NoError(t, err)
	user, err := s.q.GetUserByID(t.Context(), mapping.UserID)
	require.NoError(t, err)
	require.Equal(t, "admin", user.AdminRole)
}
