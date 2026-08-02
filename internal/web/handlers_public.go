package web

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	staticfs "github.com/gogogadget/gogogadget/static"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{
		Title:       "Ship your SaaS this weekend",
		Description: "The production-grade Go + HTMX SaaS boilerplate: auth, teams, billing, email, admin, blog, and docs out of the box.",
		Layout:      templates.LayoutPublic,
	}, templates.Home(billing.Plans))
}

func (s *Server) handlePricing(w http.ResponseWriter, r *http.Request) {
	authed := identity.UserFrom(r.Context()) != nil && identity.OrgFrom(r.Context()) != nil
	currentPlan := ""
	if authed {
		currentPlan = billing.CurrentPlan(r.Context(), s.q, identity.OrgFrom(r.Context()).ClerkOrgID, s.cfg.Now()).Key
	}
	s.Render(w, r, Page{
		Title:       "Pricing",
		Description: "Simple pricing that scales with you. Start free, upgrade when you outgrow it.",
		Layout:      templates.LayoutPublic,
	}, templates.Pricing(billing.Plans, authed, currentPlan))
}

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "Terms of Service", Layout: templates.LayoutPublic}, templates.LegalTerms())
}

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "Privacy Policy", Layout: templates.LayoutPublic}, templates.LegalPrivacy())
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.Render(w, r, Page{Title: "Page not found", Layout: templates.LayoutPublic}, templates.NotFound())
}

// serveStatic serves the embedded static/ tree. Vendored files are pinned by
// sha256 at vendor time, so they get immutable caching; app.css/app.js revalidate.
func (s *Server) serveStatic() http.Handler {
	sub, err := fs.Sub(staticfs.FS, ".")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.HasPrefix(p, "vendor/") || strings.HasPrefix(p, "fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		r.URL.Path = p
		// Defeat FileServer's redirect for directory index.
		if p == "" || strings.HasSuffix(p, "/") {
			s.handleNotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// faviconRedirect keeps legacy /favicon.ico requests on the SVG mark.
func (s *Server) faviconRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
}
