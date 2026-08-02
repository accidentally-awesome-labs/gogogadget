package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

// GET /app/settings/api — token management. Any org member may create/revoke
// org tokens (boilerplate default; tightening to org:admin is one middleware
// line — see README).
func (s *Server) handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.q.ListAPITokensByOrg(r.Context(), identity.OrgFrom(r.Context()).ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "API tokens", Layout: templates.LayoutApp},
		templates.SettingsAPI(templates.APITokensData{Tokens: tokens}))
}

// POST /app/settings/api/tokens — create; plaintext shown once.
func (s *Server) handleAPITokenCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())

	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	scope := r.FormValue("scope")
	d := templates.APITokensData{}
	if name == "" || len(name) > 80 {
		d.NameErr = "Name is required (max 80 characters)."
	}
	if scope != "read" && scope != "write" {
		d.NameErr = "Scope must be read or write."
	}
	if d.NameErr != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.renderAPITokensSection(w, r, d)
		return
	}

	plaintext, hash, err := api.GenerateToken()
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	if _, err := s.q.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{
		ClerkOrgID: org.ClerkOrgID, Name: name, TokenHash: hash, Scope: scope,
		ExpiresAt: pgtype.Timestamptz{},
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	Toast(w, "success", "Token created")
	d.NewToken = plaintext
	s.renderAPITokensSection(w, r, d)
}

func (s *Server) renderAPITokensSection(w http.ResponseWriter, r *http.Request, d templates.APITokensData) {
	tokens, err := s.q.ListAPITokensByOrg(r.Context(), identity.OrgFrom(r.Context()).ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d.Tokens = tokens
	s.Render(w, r, Page{Title: "API tokens", Layout: templates.LayoutApp}, templates.APITokensSection(d))
}

// DELETE /app/settings/api/tokens/{id} — revoke; row swap removal.
func (s *Server) handleAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleNotFound(w, r)
		return
	}
	if err := s.q.RevokeAPIToken(r.Context(), sqlc.RevokeAPITokenParams{
		ID: id, ClerkOrgID: identity.OrgFrom(r.Context()).ClerkOrgID,
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	Toast(w, "success", "Token revoked")
	w.WriteHeader(http.StatusOK)
}
