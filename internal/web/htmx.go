package web

import (
	"encoding/json"
	"net/http"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/justinas/nosurf"
)

// Page is the per-request view model every layout receives.
type Page = templates.Page

// IsHX reports whether the request came from htmx at all.
func IsHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// IsBoosted reports whether the request is an hx-boost navigation.
func IsBoosted(r *http.Request) bool { return r.Header.Get("HX-Boosted") == "true" }

// Render applies THE fragment rule: a bare fragment only when HX-Request is
// present AND HX-Boosted is absent; boosted navigations and plain requests get
// the full layout. (Boosted links send HX-Request: true — swapping a
// layout-less fragment into <body> on nav is the classic hx-boost bug.)
func (s *Server) Render(w http.ResponseWriter, r *http.Request, page Page, content templ.Component) {
	page.CSRFToken = nosurf.Token(r)
	page.Path = r.URL.Path
	page.AppURL = s.cfg.AppURL
	page.PostHogKey = s.cfg.PostHogAPIKey
	page.ClerkPublishableKey = s.cfg.ClerkPublishableKey
	page.ClerkFrontendAPIURL = s.cfg.ClerkFrontendAPIURL
	page.Now = s.cfg.Now

	component := content
	if !(IsHX(r) && !IsBoosted(r)) {
		component = wrapLayout(page, content)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		s.log.Error("render", "error", err, "path", r.URL.Path)
	}
}

func wrapLayout(page Page, content templ.Component) templ.Component {
	switch page.Layout {
	case templates.LayoutApp:
		return templates.AppLayout(page, content)
	case templates.LayoutAdmin:
		return templates.AdminLayout(page, content)
	case templates.LayoutDocs:
		return templates.DocsLayout(page, content)
	default:
		return templates.PublicLayout(page, content)
	}
}

// HXRedirect navigates the client via the HX-Redirect response header.
func HXRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
	w.WriteHeader(http.StatusOK)
}

// Redirect does the right thing for both transports: HX-Redirect for htmx
// requests, 303 See Other for plain ones.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if IsHX(r) {
		HXRedirect(w, url)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Toast queues a client toast via HX-Trigger; the listener lives in
// static/app.js (toastRoot).
func Toast(w http.ResponseWriter, typ, message string) {
	payload, _ := json.Marshal(map[string]map[string]string{
		"toast": {"type": typ, "message": message},
	})
	w.Header().Set("HX-Trigger", string(payload))
}

// renderStatus renders a minimal error page for middleware-generated statuses
// (429, CSRF 403) where no handler runs.
func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	w.WriteHeader(status)
	s.Render(w, r, Page{Title: title, Layout: templates.LayoutPublic}, templates.StatusPage(title, detail))
}

// renderError renders the 500 page from the recover middleware.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, detail string) {
	w.WriteHeader(http.StatusInternalServerError)
	s.Render(w, r, Page{Title: "Something went wrong", Layout: templates.LayoutPublic}, templates.ServerError(detail))
}
