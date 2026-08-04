package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5"
)

// Session cookie name: Clerk's own __session JWT (~60s, refreshed by clerk-js).
const sessionCookieName = "__session"

// authEnabled reports whether any real auth path exists (Clerk configured, or
// the dev/test bypass). When false, /app routes 503 "not configured".
func (s *Server) authEnabled() bool {
	return s.cfg.ClerkConfigured() || s.cfg.DevAuthBypass
}

// sessionLoad is the chain's identity step: extract the __session cookie
// (absent → continue unauthenticated), verify, lazy-upsert the local users
// mirror row when missing, and set ctxUser/ctxClaims (+ ctxOrg when the claims
// carry an active org).
func (s *Server) sessionLoad(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.verifier == nil {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := s.verifier.Verify(r.Context(), cookie.Value)
		if err != nil {
			// Invalid/expired token → treat as unauthenticated; RequireAuth redirects.
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		user, err := s.q.GetUserByClerkID(ctx, claims.UserID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			profile, ferr := s.fetcher.Fetch(ctx, claims.UserID)
			if ferr != nil {
				s.log.Error("identity sync fetch", "error", ferr, "user_id", claims.UserID)
				s.renderStatus(w, r, http.StatusServiceUnavailable, "Identity sync in progress", "We hit a snag syncing your account — refresh to retry.")
				return
			}
			user, err = s.q.UpsertUser(ctx, sqlc.UpsertUserParams{
				ClerkUserID: claims.UserID, Email: profile.Email, Name: profile.Name, AvatarUrl: profile.AvatarURL,
			})
			if err != nil {
				s.log.Error("identity sync upsert", "error", err)
				s.renderStatus(w, r, http.StatusServiceUnavailable, "Identity sync in progress", "We hit a snag syncing your account — refresh to retry.")
				return
			}
			// ADMIN_EMAIL grant on first sight of the configured address.
			if s.cfg.AdminEmail != "" && strings.EqualFold(profile.Email, s.cfg.AdminEmail) {
				if err := s.q.SetUserAdminByEmail(ctx, sqlc.SetUserAdminByEmailParams{Email: profile.Email, IsAdmin: true}); err == nil {
					user.IsAdmin = true
				}
			}
		case err != nil:
			s.log.Error("identity load user", "error", err)
			s.renderStatus(w, r, http.StatusServiceUnavailable, "Identity sync in progress", "We hit a snag syncing your account — refresh to retry.")
			return
		}

		ctx = identity.WithUser(ctx, &user)
		ctx = identity.WithClaims(ctx, claims)
		if claims.OrgID != "" {
			org, oerr := s.q.GetOrgByClerkID(ctx, claims.OrgID)
			switch {
			case oerr == nil:
				ctx = identity.WithOrg(ctx, &org)
			case errors.Is(oerr, pgx.ErrNoRows):
				// Webhook lag/failure self-heal: claims carry org_id/slug/role,
				// enough to seed the mirror. The organization.created webhook
				// corrects the name on arrival (UpsertOrg overwrites).
				org, oerr = s.q.UpsertOrg(ctx, sqlc.UpsertOrgParams{
					ClerkOrgID: claims.OrgID, Name: claims.OrgSlug, Slug: claims.OrgSlug,
				})
				if oerr == nil {
					if err := s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{
						ClerkOrgID: claims.OrgID, ClerkUserID: claims.UserID, Role: claims.OrgRole,
					}); err != nil {
						s.log.Warn("lazy membership sync", "error", err)
					}
					ctx = identity.WithOrg(ctx, &org)
				} else {
					s.log.Error("lazy org sync", "error", oerr)
				}
			default:
				s.log.Error("identity load org", "error", oerr)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAuth: anonymous → 303 /login (HX → 401 + HX-Redirect).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled() {
			w.WriteHeader(http.StatusServiceUnavailable)
			s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
			return
		}
		if identity.UserFrom(r.Context()) == nil {
			if IsHX(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireNotDisabled: disabled users get the Disabled page with 403.
func (s *Server) requireNotDisabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := identity.UserFrom(r.Context()); u != nil && u.DisabledAt.Valid {
			w.WriteHeader(http.StatusForbidden)
			s.Render(w, r, Page{Title: "Account disabled", Layout: templates.LayoutPublic}, templates.Disabled())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireOrg: no active org in claims → query mirror memberships. ≥1 → render
// SelectOrg; 0 → redirect to Clerk's hosted create-organization (an invited
// teammate must never be told to found a competing org).
func (s *Server) requireOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := identity.ClaimsFrom(r.Context())
		user := identity.UserFrom(r.Context())
		if claims == nil || user == nil {
			s.renderError(w, r, "requireOrg without requireAuth")
			return
		}
		if claims.OrgID == "" {
			orgs, err := s.q.GetOrgsForUser(r.Context(), claims.UserID)
			if err != nil {
				s.renderError(w, r, err.Error())
				return
			}
			if len(orgs) == 0 {
				target := s.cfg.ClerkPortalURL + "/create-organization?redirect_url=" + s.cfg.AppURL + "/app"
				Redirect(w, r, target)
				return
			}
			s.Render(w, r, Page{Title: "Choose an organization", Layout: templates.LayoutPublic},
				templates.SelectOrg(orgs, s.cfg.DevAuthBypass && !s.cfg.ClerkConfigured()))
			return
		}
		if identity.OrgFrom(r.Context()) == nil {
			// Claims carry an org the mirror hasn't synced yet (webhook in flight).
			s.renderStatus(w, r, http.StatusServiceUnavailable, "Organization sync in progress", "Your organization is being set up — refresh to retry.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadPlan sets ctxSub + ctxPlan after requireOrg.
func (s *Server) loadPlan(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := identity.OrgFrom(r.Context())
		if org == nil {
			s.renderError(w, r, "loadPlan without requireOrg")
			return
		}
		ctx := r.Context()
		sub, err := s.q.GetSubscriptionByOrg(ctx, org.ClerkOrgID)
		if err == nil {
			ctx = identity.WithSub(ctx, &sub)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			s.renderError(w, r, err.Error())
			return
		}
		plan := billing.CurrentPlan(ctx, s.q, org.ClerkOrgID, s.cfg.Now())
		next.ServeHTTP(w, r.WithContext(identity.WithPlan(ctx, plan)))
	})
}

// requireAdmin: site admins only (is_admin via ADMIN_EMAIL).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := identity.UserFrom(r.Context())
		if u == nil || !u.IsAdmin {
			s.renderStatus(w, r, http.StatusForbidden, "Forbidden", "This area is restricted to site administrators.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// appChain is the standard /app guard sequence (order is load-bearing).
func (s *Server) appChain(h http.Handler) http.Handler {
	return s.requireAuth(s.requireNotDisabled(s.requireOrg(s.loadPlan(h))))
}

// adminChain adds RequireAdmin for /admin/*.
func (s *Server) adminChain(h http.Handler) http.Handler {
	return s.requireAuth(s.requireNotDisabled(s.requireOrg(s.loadPlan(s.requireAdmin(h)))))
}
