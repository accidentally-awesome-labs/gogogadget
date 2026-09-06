package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	_, _ = s.q.InsertIdentitySubject(t.Context(), sqlc.InsertIdentitySubjectParams{Provider: identity.MockProvider, Subject: id, UserID: id})
	t.Cleanup(func() { _ = s.q.DeleteUser(context.Background(), id) })
}

func seedOrg(t *testing.T, s *Server, id, slug string) {
	_, err := s.q.UpsertOrg(t.Context(), sqlc.UpsertOrgParams{OrgID: id, Name: slug + " Org", Slug: slug})
	require.NoError(t, err)
	_, _ = s.q.InsertIdentityOrganization(t.Context(), sqlc.InsertIdentityOrganizationParams{Provider: identity.MockProvider, Subject: id, OrgID: id})
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

// The shell's own mounts are neutral: an identity adapter that ships widgets
// contributes the live elements from its own slot renderers (asserted against
// the rendered document in internal/identity/clerk/shell), and this project's
// test environment selects the zero-account adapter, which ships none. So the
// shell must render its stable containers and name no provider.
func TestAppShellRendersNeutralMountsAndContentScopedNav(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		// Set even though nothing should read it: a configured key must not
		// resurrect a mount the selected adapter does not provide.
		d.Config.Values["CLERK_PUBLISHABLE_KEY"] = "pk_test_fixture"
	})
	seedMembership(t, s, "user_shell", "org_shell", "org:admin")
	cookie := sessionCookie("user_shell", "org_shell", "org:admin")

	code, _, body := serve(t, s, "GET", "/app", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `id="org-switcher" class="min-h-8"`)
	assert.Contains(t, body, `data-shell-placeholder="org_shell Org"`)
	assert.Contains(t, body, `id="user-button" class="min-h-8 min-w-8"`)
	assert.Contains(t, body, `data-shell-placeholder="U"`)
	// The shell's own bytes name no provider. Scoped to the markers the seam
	// used to emit rather than the whole document: page copy is application
	// content a project edits, and the dashboard checklist still points at a
	// real provider feature by name.
	for _, absent := range []string{
		"clerk-org-slot", "clerk-user-slot", "clerk-publishable-key",
		"clerk.browser.js", "ph-key",
	} {
		assert.NotContains(t, body, absent)
	}
	// Nav swaps ONLY #content. An identity provider's widgets render their
	// dropdown menus as portals appended directly to <body>, so any
	// swap/morph of <body> deletes them and the dropdowns die; Alpine
	// bindings in the shell break the same way. The shell must therefore
	// never be a swap target.
	assert.Contains(t, body, `hx-boost="true"`)
	assert.Contains(t, body, `hx-target="#content"`)
	assert.Contains(t, body, `hx-select="#content"`)
	// htmx 4 drives the swap through the View Transitions API.
	assert.Contains(t, body, `hx-swap="outerHTML transition:true show:top"`)
	assert.NotContains(t, body, `hx-target="body"`)
	assert.NotContains(t, body, "hx-morph-skip")
	// hx-preserve quarantines the element (stash + restore), which is what
	// detached the mounted widgets' listeners originally.
	assert.NotContains(t, body, "hx-preserve")
	// hx-history was removed in htmx 4; hx-history-elt keeps a Back-navigation
	// re-fetch scoped to #content instead of <body>.
	assert.NotContains(t, body, `hx-history="`)
	assert.Contains(t, body, `<main id="content" hx-history-elt="true"`)
}

// hostedNavigator stands in for a hosted identity adapter at the rendered
// layer. internal/web must not import a provider adapter — that is the
// coupling v0.5.0 removed — so the shape a hosted portal answers with is
// spelled here, while each real adapter's own spelling is pinned by its
// contract suite (identity/clerk's TestNavigatorContract and the shared
// identity/contract table).
type hostedNavigator struct{ base string }

func (n hostedNavigator) page(path, returnTo string) (string, error) {
	if returnTo == "" {
		return n.base + path, nil
	}
	return n.base + path + "?redirect_url=" + url.QueryEscape(returnTo), nil
}

func (n hostedNavigator) LoginURL(returnTo string) (string, error) {
	return n.page("/sign-in", returnTo)
}
func (n hostedNavigator) SignupURL(returnTo string) (string, error) {
	return n.page("/sign-up", returnTo)
}
func (n hostedNavigator) LogoutURL(returnTo string) (string, error) {
	return n.page("/sign-out", returnTo)
}
func (n hostedNavigator) AccountURL(returnTo string) (string, error) {
	return n.page("/user", returnTo)
}
func (n hostedNavigator) OrganizationURL(returnTo string) (string, error) {
	return n.page("/organization", returnTo)
}
func (n hostedNavigator) CreateOrganizationURL(returnTo string) (string, error) {
	return n.page("/create-organization", returnTo)
}

// hostedServer is the fixture with a hosted identity adapter selected.
func hostedServer(t *testing.T) (*Server, hostedNavigator) {
	t.Helper()
	nav := hostedNavigator{base: "https://accounts.example.test"}
	return integrationServer(t, func(d *Deps) { d.IdentityNavigator = nav }), nav
}

// With a hosted adapter selected, both settings pages render that adapter's
// own account and organization pages. The web package supplies the return
// target and nothing else: it does not know the path, the parameter name, or
// the escaping.
func TestSettingsRenderTheSelectedProvidersPages(t *testing.T) {
	s, _ := hostedServer(t)
	seedMembership(t, s, "user_portal", "org_portal", "org:admin")
	cookie := sessionCookie("user_portal", "org_portal", "org:admin")

	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `href="https://accounts.example.test/user?redirect_url=http%3A%2F%2Flocalhost%3A18080%2Fapp%2Fsettings%2Faccount"`)
	// The caption is selected from the same value as the link, so the other
	// half of that selection belongs here: with a provider page to point at,
	// the page says a provider manages the profile.
	assert.Contains(t, body, "are managed by your identity provider")
	assert.NotContains(t, body, "You are signed in locally")

	code, _, body = serve(t, s, "GET", "/app/settings/org", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `href="https://accounts.example.test/organization?redirect_url=http%3A%2F%2Flocalhost%3A18080%2Fapp%2Fsettings%2Forg"`)
	assert.Contains(t, body, "are managed by your identity provider")
	assert.NotContains(t, body, "managed in this application")
	assert.Contains(t, body, `aria-current="page"`)
	assert.Contains(t, body, `hx-select="#content"`)
}

// The silent half of the regression, proven at the rendered layer under an
// adapter that manages no profile.
//
// Both pages used to build their link from CLERK_PORTAL_URL read by key. The
// fixture supplies that key — internal/web does not declare it, and the CSP
// registry still reads it — so the pages rendered a hosted provider's URLs
// whatever adapter was selected. They now ask the port; the harness's
// identity.MockNavigator refuses the three self-service destinations, the
// way every zero-account adapter does, and the page renders its explanatory
// copy with no link at all: no foreign host, and no empty href dressed up
// as one.
func TestSettingsRenderNoProviderLinkWhenTheAdapterHasNone(t *testing.T) {
	s := integrationServer(t, nil)
	require.NotEmpty(t, s.cfg.Value("CLERK_PORTAL_URL"),
		"this test is only meaningful while a foreign provider key is configured for the pages to ignore")
	seedMembership(t, s, "user_neutral", "org_neutral", "org:admin")
	cookie := sessionCookie("user_neutral", "org_neutral", "org:admin")

	// What is gone, and — the part this test used to miss — what is said
	// instead. Asserting only the absent link pinned a page that still
	// claimed "managed by your identity provider" while the selected adapter
	// says in as many words that it manages nothing.
	for _, tc := range []struct {
		path, says, saysNot, link string
	}{
		{
			path:    "/app/settings/account",
			says:    "You are signed in locally",
			saysNot: "are managed by your identity provider",
			link:    "Manage your account",
		},
		{
			path:    "/app/settings/org",
			says:    "managed in this application",
			saysNot: "are managed by your identity provider",
			link:    "Manage organization",
		},
	} {
		code, _, body := serve(t, s, "GET", tc.path, nil, nil, cookie)
		require.Equal(t, http.StatusOK, code, tc.path)
		assert.Contains(t, body, tc.says,
			"%s must say what is actually true under the selected adapter", tc.path)
		assert.NotContains(t, body, tc.saysNot,
			"%s must not claim a provider manages what this adapter keeps locally", tc.path)
		assert.NotContains(t, body, tc.link, "%s must render no provider link", tc.path)
		assert.NotContains(t, body, "accounts.example.test",
			"%s must not spell a provider the selected adapter is not", tc.path)
		assert.NotContains(t, body, `href=""`, "%s must not render an empty link", tc.path)
	}
}

func TestRequireAuthWhenNoProviderIsSelected(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.Values["DEV_AUTH_BYPASS"] = "false"
		d.Config.Values["CLERK_SECRET_KEY"] = ""
	})
	code, hdr, _ := serve(t, s, "GET", "/app/settings/account", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/login", hdr.Get("Location"))
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
}

// Zero memberships with a hosted adapter selected → that adapter's own
// create-organization page, carrying an escaped return target. The return
// target is escaped because the adapter escapes it; this branch used to build
// the URL inline and was the one spelling in the tree that did not.
func TestRequireOrgSendsZeroOrgUsersToTheProvidersCreatePage(t *testing.T) {
	s, _ := hostedServer(t)
	seedUser(t, s, "user_m2", "m2@example.com", "M Two")
	const want = "https://accounts.example.test/create-organization?redirect_url=http%3A%2F%2Flocalhost%3A18080%2Fapp"

	code, hdr, _ := serve(t, s, "GET", "/app/settings/account", nil, nil, sessionCookie("user_m2", "", ""))
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, want, hdr.Get("Location"))

	hx := http.Header{"HX-Request": {"true"}, "HX-Boosted": {"true"}}
	code, hdr, _ = serve(t, s, "GET", "/app/settings/account", nil, hx, sessionCookie("user_m2", "", ""))
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, want, hdr.Get("HX-Redirect"))
}

// The loud half of the regression, at the response layer.
//
// This branch used to concatenate CLERK_PORTAL_URL and "/create-organization"
// by hand. With the dev adapter selected and no portal key of its own, that
// produced the relative "/create-organization?redirect_url=…" — a 404 on the
// zero-account path the framework advertises as working from a fresh clone,
// for the one visitor the branch exists to help. The dev adapter has no
// org-creation page and this application serves no such route, so the guard
// now refuses where it can be seen: no Location at all, and a named page.
func TestRequireOrgRefusesVisiblyWhenTheAdapterHasNoCreatePage(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_m3", "m3@example.com", "M Three")
	cookie := sessionCookie("user_m3", "", "")

	code, hdr, body := serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Empty(t, hdr.Get("Location"),
		"no redirect at all, least of all a relative path this application does not serve")
	assert.NotContains(t, body, "create-organization?")
	assert.Contains(t, body, "no create-organization page")

	hx := http.Header{"HX-Request": {"true"}, "HX-Boosted": {"true"}}
	code, hdr, _ = serve(t, s, "GET", "/app/settings/account", nil, hx, cookie)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Empty(t, hdr.Get("HX-Redirect"))
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

	mapping, err := s.q.GetIdentityOrganization(t.Context(), sqlc.GetIdentityOrganizationParams{Provider: identity.MockProvider, Subject: "org_lazy"})
	require.NoError(t, err)
	org, err := s.q.GetOrgByID(t.Context(), mapping.OrgID)
	require.NoError(t, err)
	assert.Equal(t, "org_lazy", org.Slug)
	m, err := s.q.GetMembership(t.Context(), sqlc.GetMembershipParams{OrgID: mapping.OrgID, UserID: "user_lazy"})
	require.NoError(t, err)
	assert.Equal(t, "org:admin", m.Role)
	// A later organization.created webhook corrects the placeholder name.
	payload, headers := orgDelivery("msg_lazy1", "organization.created", "org_lazy", "Real Name", "org_lazy")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	require.Equal(t, http.StatusOK, code)
	org, _ = s.q.GetOrgByID(t.Context(), mapping.OrgID)
	assert.Equal(t, "Real Name", org.Name)
}

// /login, /signup and /logout hand off to whatever the selected identity
// adapter's Navigator answers, with no branch of their own. The handler's job
// is the hand-off; each provider's URL shape is pinned by its own adapter
// suite (identity/clerk's TestNavigatorContract and the shared
// identity/contract table).
func TestAuthRoutesHandOffToTheSelectedAdapter(t *testing.T) {
	s, nav := hostedServer(t)

	code, hdr, _ := serve(t, s, "GET", "/login", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.LoginURL("http://localhost:18080/?after-auth=1")), hdr.Get("Location"))

	code, hdr, _ = serve(t, s, "GET", "/signup", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.SignupURL("")), hdr.Get("Location"))

	// Sign-out clears this application's own cookie before handing off. It is
	// the cookie every request here is read from, so redirecting to a hosted
	// sign-out without clearing it left the visitor signed in locally.
	code, hdr, _ = serve(t, s, "GET", "/logout", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.LogoutURL("http://localhost:18080/")), hdr.Get("Location"))
	assert.Contains(t, hdr.Values("Set-Cookie"), sessionCookieName+"=; Path=/; Max-Age=0",
		"a hosted sign-out must still expire this application's own session cookie")
}

// A zero-account adapter answers sign-in with a route of its own, so the
// login handler no longer reads DEV_AUTH_BYPASS and no longer spells
// /dev/login itself: the adapter that owns that key owns that route. The
// hand-off is asserted against the seam's own double rather than against
// ggg/system/identity-dev, because which adapter is selected for the test
// environment is a provider choice and this claim is true of any of them.
func TestAuthRoutesUseTheSelectedAdaptersOwnPages(t *testing.T) {
	s := integrationServer(t, nil)
	nav := identity.MockNavigator{BaseURL: "http://localhost:18080"}

	code, hdr, _ := serve(t, s, "GET", "/login", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.LoginURL("http://localhost:18080/?after-auth=1")), hdr.Get("Location"))
	assert.Contains(t, hdr.Get("Location"), "return_to=http%3A%2F%2Flocalhost%3A18080%2F%3Fafter-auth%3D1",
		"the handler supplies the return target and the adapter owns the parameter name and the escaping")

	code, hdr, _ = serve(t, s, "GET", "/signup", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.SignupURL("")), hdr.Get("Location"))

	code, hdr, _ = serve(t, s, "GET", "/logout", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, must(nav.LogoutURL("http://localhost:18080/")), hdr.Get("Location"))
}

// Every handler that asks the port refuses visibly when the selected adapter
// has no page for the destination. Each of these used to be a redirect:
// sign-in and sign-up answered the handler's own /login, which is a loop
// straight back into the handler that asked.
//
// The behaviour under test is REFUSAL, and it is selected deliberately: an
// identity.MockNavigator with no BaseURL publishes nothing, which is the
// state of an adapter whose sign-in route is not registered or whose base is
// unconfigured. This used to lean on identitydev.Navigator's zero value
// happening to refuse — a load-bearing accident, in a payload that owns
// neither the adapter nor its zero-value semantics.
//
// Three here plus create-organization in TestRequireOrgRefusesVisibly… and
// the two settings captions makes refusal proven at the rendered layer for
// all six destinations, not four.
func TestAuthHandlersRefuseVisiblyWhenTheAdapterHasNoPage(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.Values["DEV_AUTH_BYPASS"] = "false"
		d.IdentityNavigator = identity.MockNavigator{}
	})

	for _, tc := range []struct{ path, says string }{
		{"/login", "no sign-in page"},
		{"/signup", "no sign-up page"},
		{"/logout", "no sign-out page"},
	} {
		code, hdr, body := serve(t, s, "GET", tc.path, nil, nil)
		assert.Equal(t, http.StatusServiceUnavailable, code, tc.path)
		assert.Empty(t, hdr.Get("Location"), "%s must not redirect anywhere", tc.path)
		assert.Empty(t, hdr.Get("HX-Redirect"), tc.path)
		assert.Contains(t, body, tc.says, tc.path)
	}
}

func must(url string, err error) string {
	if err != nil {
		panic(err)
	}
	return url
}
