package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
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

// serveStatic serves the embedded static/ tree.
//
// Vendored files are pinned by sha256 at vendor time and their names change
// when the content does, so they get immutable caching. app.css and app.js
// are NOT content-addressed: they ship inside the binary and change with
// every deploy while their URL stays the same, so they must revalidate.
// "no-cache" means "ask first", not "do not store". A max-age here would
// leave browsers running the previous release's JavaScript against the new
// server for its duration — which breaks precisely the changes that move
// client and server together.
//
// Revalidation needs a validator, and embedded files have no modification
// time (io/fs reports the zero time), so http.FileServerFS cannot emit
// Last-Modified and every "ask first" would re-download the whole file.
// Hashing the tree once at startup gives each file a content ETag; net/http
// checks If-None-Match against it and answers 304.
func (s *Server) serveStatic() http.Handler {
	sub, err := fs.Sub(staticfs.FS, ".")
	if err != nil {
		panic(err)
	}
	etags := contentETags(sub)
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.HasPrefix(p, "vendor/") || strings.HasPrefix(p, "fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, no-cache")
		}
		if tag, ok := etags[p]; ok {
			w.Header().Set("ETag", tag)
		}
		// Clone before rewriting so accessLog sees the original path.
		r2 := r.Clone(r.Context())
		r2.URL = cloneURL(r.URL)
		r2.URL.Path = p
		if p == "" || strings.HasSuffix(p, "/") {
			s.handleNotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r2)
	})
}

// contentETags hashes every embedded file once at construction. The tree is
// a handful of files that never change at run time, so this is a startup cost
// measured in microseconds, paid to make revalidation a 304 instead of a
// re-download.
func contentETags(fsys fs.FS) map[string]string {
	out := map[string]string{}
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out[path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return out
}

func cloneURL(u *url.URL) *url.URL {
	v := *u
	return &v
}

// faviconRedirect keeps legacy /favicon.ico requests on the SVG mark.
func (s *Server) faviconRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
}
