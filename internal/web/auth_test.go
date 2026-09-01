package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedUser(t *testing.T, s *Server, id, email, name string) {
	t.Helper()
	_, err := s.q.UpsertUser(t.Context(), sqlc.UpsertUserParams{UserID: id, Email: email, Name: name})
	require.NoError(t, err)
	_, _ = s.q.InsertIdentitySubject(t.Context(), sqlc.InsertIdentitySubjectParams{Provider: "dev", Subject: id, UserID: id})
	t.Cleanup(func() { _ = s.q.DeleteUser(context.Background(), id) })
}

func seedOrg(t *testing.T, s *Server, id, slug string) {
	_, err := s.q.UpsertOrg(t.Context(), sqlc.UpsertOrgParams{OrgID: id, Name: slug + " Org", Slug: slug})
	require.NoError(t, err)
	_, _ = s.q.InsertIdentityOrganization(t.Context(), sqlc.InsertIdentityOrganizationParams{Provider: "dev", Subject: id, OrgID: id})
	t.Cleanup(func() { _ = s.q.DeleteOrg(context.Background(), id) })
}

func TestRequireAuthRedirectsAnonymous(t *testing.T) {
	s := integrationServer(t, nil)

	// Plain request → 303 /login.
	code, hdr, _ := serve(t, s, "GET", "/app/settings/account", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/login", hdr.Get("Location"))

	// HX request → 401 + HX-Redirect (never a 303 htmx would follow blindly).
	h := http.Header{}
	h.Set("HX-Request", "true")
	code, hdr, _ = serve(t, s, "GET", "/app/settings/account", nil, h)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "/login", hdr.Get("HX-Redirect"))
}

func TestRequireAuthAcceptsValidSession(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_a1", "a1@example.com", "A One")
	seedOrg(t, s, "org_a1", "a1")
	require.NoError(t, s.q.UpsertMembership(t.Context(), sqlc.UpsertMembershipParams{OrgID: "org_a1", UserID: "user_a1", Role: "org:admin"}))

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, sessionCookie("user_a1", "org_a1", "org:admin"))
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "a1@example.com")
}

func TestAppShellRendersClerkMountsAndContentScopedNav(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.ClerkPublishableKey = "pk_test_fixture"
	})
	seedMembership(t, s, "user_shell", "org_shell", "org:admin")
	cookie := sessionCookie("user_shell", "org_shell", "org:admin")

	code, _, body := serve(t, s, "GET", "/app", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `id="org-switcher" class="clerk-org-slot min-h-8"`)
	assert.Contains(t, body, `data-clerk-placeholder="org_shell Org"`)
	assert.Contains(t, body, `id="user-button" class="clerk-user-slot min-h-8 min-w-8"`)
	assert.Contains(t, body, `data-clerk-placeholder="U"`)
	assert.NotContains(t, body, " else data-clerk-placeholder")
	// Nav swaps ONLY #content. clerk-js renders its dropdown menus as portals
	// appended directly to <body>, so any swap/morph of <body> deletes them and
	// the dropdowns die; Alpine bindings in the shell break the same way. The
	// shell must therefore never be a swap target.
	assert.Contains(t, body, `hx-boost="true"`)
	assert.Contains(t, body, `hx-target="#content"`)
	assert.Contains(t, body, `hx-select="#content"`)
	// htmx 4 drives the swap through the View Transitions API.
	assert.Contains(t, body, `hx-swap="outerHTML transition:true show:top"`)
	assert.NotContains(t, body, `hx-target="body"`)
	assert.NotContains(t, body, "hx-morph-skip")
	// hx-preserve quarantines the element (stash + restore), which is what
	// detached clerk's listeners originally.
	assert.NotContains(t, body, "hx-preserve")
	// hx-history was removed in htmx 4; hx-history-elt keeps a Back-navigation
	// re-fetch scoped to #content instead of <body>.
	assert.NotContains(t, body, `hx-history="`)
	assert.Contains(t, body, `<main id="content" hx-history-elt="true"`)

	s.cfg.ClerkPublishableKey = ""
	code, _, body = serve(t, s, "GET", "/app", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `id="org-switcher" class="min-h-8"`)
	assert.Contains(t, body, `id="user-button" class="min-h-8 min-w-8"`)
	assert.NotContains(t, body, "clerk-org-slot")
	assert.NotContains(t, body, "clerk-user-slot")
}

func TestSettingsUseCurrentClerkAccountPortalLinks(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_portal", "org_portal", "org:admin")
	cookie := sessionCookie("user_portal", "org_portal", "org:admin")

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `href="https://accounts.example.test/user?redirect_url=http%3A%2F%2Flocalhost%3A18080%2Fapp%2Fsettings%2Faccount"`)

	code, _, body = serve(t, s, "GET", "/app/settings/org", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `href="https://accounts.example.test/organization?redirect_url=http%3A%2F%2Flocalhost%3A18080%2Fapp%2Fsettings%2Forg"`)
	assert.Contains(t, body, `aria-current="page"`)
	assert.Contains(t, body, `hx-select="#content"`)
}

func TestSettingsHideClerkLinksWhenAccountPortalIsUnconfigured(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.ClerkPortalURL = ""
	})
	seedMembership(t, s, "user_no_portal", "org_no_portal", "org:admin")
	cookie := sessionCookie("user_no_portal", "org_no_portal", "org:admin")

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Manage your account")

	code, _, body = serve(t, s, "GET", "/app/settings/org", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Manage organization")
}

func TestRequireAuth503WhenUnconfigured(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.DevAuthBypass = false
		d.Config.ClerkSecretKey = ""
	})
	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "Auth not configured")
	assert.Contains(t, body, "/docs/authentication")
}

func TestRequireNotDisabled(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_d1", "d1@example.com", "D One")
	seedOrg(t, s, "org_d1", "d1")
	require.NoError(t, s.q.UpsertMembership(t.Context(), sqlc.UpsertMembershipParams{OrgID: "org_d1", UserID: "user_d1", Role: "org:admin"}))

	now := time.Now()
	require.NoError(t, s.q.SetUserDisabled(t.Context(), sqlc.SetUserDisabledParams{UserID: "user_d1", DisabledAt: pgtype.Timestamptz{Time: now, Valid: true}}))

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, sessionCookie("user_d1", "org_d1", "org:admin"))
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, body, "Account disabled")
}

func TestRequireOrgSelectsOrCreates(t *testing.T) {
	s := integrationServer(t, nil)

	// Memberships but no active org → SelectOrg page lists them.
	seedUser(t, s, "user_m1", "m1@example.com", "M One")
	seedOrg(t, s, "org_m1", "m1")
	require.NoError(t, s.q.UpsertMembership(t.Context(), sqlc.UpsertMembershipParams{OrgID: "org_m1", UserID: "user_m1", Role: "org:member"}))

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, sessionCookie("user_m1", "", ""))
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Choose an organization")
	assert.Contains(t, body, "m1 Org")

	// Boosted navigation leaves the app shell before showing SelectOrg.
	hx := http.Header{"HX-Request": {"true"}, "HX-Boosted": {"true"}}
	code, hdr, body := serve(t, s, "GET", "/app/settings/account", nil, hx, sessionCookie("user_m1", "", ""))
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "/app/settings/account", hdr.Get("HX-Redirect"))
	assert.Empty(t, body)

	// Zero memberships → redirect to the portal's create-organization.
	seedUser(t, s, "user_m2", "m2@example.com", "M Two")
	code, hdr, _ = serve(t, s, "GET", "/app/settings/account", nil, nil, sessionCookie("user_m2", "", ""))
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "https://accounts.example.test/create-organization?redirect_url=http://localhost:18080/app", hdr.Get("Location"))

	code, hdr, _ = serve(t, s, "GET", "/app/settings/account", nil, hx, sessionCookie("user_m2", "", ""))
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "https://accounts.example.test/create-organization?redirect_url=http://localhost:18080/app", hdr.Get("HX-Redirect"))
}

func TestRequireOrgRedirectsBoostedSyncInterstitial(t *testing.T) {
	s := integrationServer(t, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest("GET", "/app", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Boosted", "true")
	ctx := identity.WithClaims(req.Context(), &identity.Claims{UserID: "user_sync", OrgID: "org_sync"})
	ctx = identity.WithUser(ctx, &sqlc.User{UserID: "user_sync"})
	rec := httptest.NewRecorder()

	s.requireOrg(next).ServeHTTP(rec, req.WithContext(ctx))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/app", rec.Header().Get("HX-Redirect"))
	assert.Empty(t, rec.Body.String())
}

func TestRequireAdmin(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_adm", "adm@example.com", "Adm")
	seedOrg(t, s, "org_adm", "adm")
	require.NoError(t, s.q.UpsertMembership(t.Context(), sqlc.UpsertMembershipParams{OrgID: "org_adm", UserID: "user_adm", Role: "org:admin"}))

	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := s.requireStaff(probe)

	// Non-admin → 403.
	req := httptest.NewRequest("GET", "/admin", nil)
	ctx := identity.WithUser(req.Context(), &sqlc.User{UserID: "user_adm", AdminRole: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Support reads the admin area too — the write boundary is a separate guard.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(identity.WithUser(req.Context(), &sqlc.User{UserID: "user_adm", AdminRole: identity.RoleSupport})))
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Admin → pass.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(identity.WithUser(req.Context(), &sqlc.User{UserID: "user_adm", AdminRole: identity.RoleAdmin})))
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestLoadPlanDefaultsFree(t *testing.T) {
	s := integrationServer(t, nil)
	org := &sqlc.Org{OrgID: "org_plan_none"}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := identity.PlanFrom(r.Context())
		assert.Equal(t, "free", p.Key)
		w.WriteHeader(204)
	})
	req := httptest.NewRequest("GET", "/app", nil)
	req = req.WithContext(identity.WithOrg(req.Context(), org))
	rec := httptest.NewRecorder()
	s.loadPlan(probe).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestLazyOrgSync(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_lazy", "lazy@example.com", "Lazy")
	// No org row, no membership: claims alone must seed the mirror.
	code, _, body := serve(t, s, "GET", "/app", nil, nil, sessionCookie("user_lazy", "org_lazy", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Dashboard")

	org, err := s.q.GetOrgByID(t.Context(), "org_lazy")
	require.NoError(t, err)
	assert.Equal(t, "org_lazy", org.Slug)
	m, err := s.q.GetMembership(t.Context(), sqlc.GetMembershipParams{OrgID: "org_lazy", UserID: "user_lazy"})
	require.NoError(t, err)
	assert.Equal(t, "org:admin", m.Role)

	// A later organization.created webhook corrects the placeholder name.
	payload := []byte(`{"type": "organization.created", "data": {"id": "org_lazy", "name": "Real Name", "slug": "org_lazy"}}`)
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, "msg_lazy1", payload))
	require.Equal(t, http.StatusOK, code)
	org, _ = s.q.GetOrgByID(t.Context(), "org_lazy")
	assert.Equal(t, "Real Name", org.Name)
}

func TestLoginRedirectRoutes(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.DevAuthBypass = false
		d.Config.ClerkSecretKey = "sk_test_x"
	})
	code, hdr, _ := serve(t, s, "GET", "/login", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "https://accounts.example.test/sign-in?redirect_url=http://localhost:18080/?after-auth=1", hdr.Get("Location"))

	code, hdr, _ = serve(t, s, "GET", "/signup", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Contains(t, hdr.Get("Location"), "/sign-up")

	code, hdr, _ = serve(t, s, "GET", "/logout", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Contains(t, hdr.Get("Location"), "/sign-out")
}
