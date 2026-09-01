package web

import (
	"encoding/json"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/justinas/nosurf"
)

// Page is the per-request view model every layout receives.
type Page = templates.Page

// ContentTarget is the ONLY element htmx ever swaps during navigation. The
// shell around it (sidebar, topbar, toast root) hosts clerk-js's mounted
// widgets and their body-level dropdown portals, plus Alpine components — all
// of which die if they are swapped. Server-driven navigation must name this
// target explicitly; see the App navigation section of /docs/frontend.
//
// NavSwap is how every navigation swaps it: replace the box, and let the
// browser's View Transitions API cross-fade the change. Opted in per-swap
// rather than through htmx.config.transitions so in-page updates — table
// search, row deletes, the billing poll — stay instant. Templates declare it in
// hx-swap; templates.NavSwap keeps the two spellings from drifting.
// Both are aliases of the ui contract, which owns them because its components
// emit the attributes. Three copies of "#content" is three chances to disagree
// about which element a navigation replaces.
const (
	ContentTarget = ui.NavTarget
	NavSwap       = ui.NavSwap
)

// IsHX reports whether the request came from htmx at all.
func IsHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// IsBoosted reports whether the request is an hx-boost navigation.
func IsBoosted(r *http.Request) bool { return r.Header.Get("HX-Boosted") == "true" }

// IsHistoryRestore reports an htmx cache-miss history re-fetch.
func IsHistoryRestore(r *http.Request) bool {
	return r.Header.Get("HX-History-Restore-Request") == "true"
}

// RequestType returns htmx 4's HX-Request-Type: "full" when the client will
// pick what it needs out of a complete page, "partial" when it wants only the
// fragment. Empty for pre-4.0 clients and hand-rolled fetches.
func RequestType(r *http.Request) string { return r.Header.Get("HX-Request-Type") }

// Target returns htmx 4's HX-Target: the element the client will swap, as
// "tag#id" (e.g. "main#content", "div#table-container") or a bare tag name.
func Target(r *http.Request) string { return r.Header.Get("HX-Target") }

// TargetsContent reports that the client is replacing the whole content box.
// That is a navigation by definition — nothing smaller is being updated — so
// the response must be a complete page.
func TargetsContent(r *http.Request) bool {
	target := Target(r)
	if i := strings.IndexByte(target, '#'); i >= 0 {
		return target[i:] == ContentTarget
	}
	return false
}

// wantsFragment decides layout vs bare fragment.
//
// htmx 4 tells us both what the client will swap (HX-Target) and how much of
// the response it intends to use (HX-Request-Type), so the decision is read off
// the request instead of inferred from HX-Boosted. Two consequences worth
// naming: the classic hx-boost bug (a layout-less fragment swapped into the
// document) becomes structurally impossible, and a request can ask for a full
// page WITHOUT being boosted — which is what our HX-Location navigations do.
//
// Order matters:
//  1. History-restore always needs the complete page: htmx re-fetches the URL
//     and lifts the hx-history-elt out of the response.
//  2. Replacing the content box outright is a navigation, whatever else the
//     request says. (htmx 4.0.0-beta6 reports HX-Request-Type from the
//     hx-select ATTRIBUTE only, so a select passed through the ajax API — an
//     HX-Location payload — still arrives labelled "partial".)
//  3. Otherwise honour the client's stated intent, falling back to the 2.x
//     boosted heuristic for pre-4.0 clients.
func wantsFragment(r *http.Request) bool {
	if !IsHX(r) || IsHistoryRestore(r) || TargetsContent(r) {
		return false
	}
	switch RequestType(r) {
	case "full":
		return false
	case "partial":
		return true
	}
	return !IsBoosted(r) // pre-4.0 clients
}

// Render returns a bare fragment only for htmx requests that asked for one.
// Plain, full-page, and history-restore requests receive a full layout.
func (s *Server) Render(w http.ResponseWriter, r *http.Request, page Page, content templ.Component) {
	page.CSRFToken = nosurf.Token(r)
	page.Path = r.URL.Path
	page.AppURL = s.cfg.AppURL
	page.PostHogKey = s.cfg.Value("POSTHOG_API_KEY")
	page.ClerkPublishableKey = s.cfg.ClerkPublishableKey
	page.ClerkFrontendAPIURL = s.cfg.ClerkFrontendAPIURL
	// Active platform banner for the authed shells (nil-safe elsewhere).
	if page.Layout == templates.LayoutApp || page.Layout == templates.LayoutAdmin {
		page.Announcement = s.currentAnnouncement(r.Context())
	}
	page.Now = s.cfg.Now
	// Layout identity context, when the guard chain populated it.
	page.User = identity.UserFrom(r.Context())
	page.Org = identity.OrgFrom(r.Context())
	page.Plan = identity.PlanFrom(r.Context())
	page.Sub = identity.SubFrom(r.Context())
	page.Impersonator = identity.ImpersonatorFrom(r.Context())
	page.Theme = resolveTheme(r, page.User)
	// Indexable surfaces only. /app and /admin are behind auth, so a crawler
	// never sees them and canonical/hreflang on them would be noise.
	if page.Layout == templates.LayoutPublic || page.Layout == templates.LayoutDocs {
		page.Canonical = s.canonicalFor(r)
		page.Alternates = s.alternatesFor(r)
	}

	if page.Layout == templates.LayoutAdmin {
		r = r.WithContext(templates.WithAdminWrite(r.Context(), identity.IsAdmin(page.User)))
	}

	// Feature gates flow into templates via the request context (SettingsTabs
	// hides the Webhooks tab when its flag is off for this org).
	if page.Org != nil && s.flags != nil {
		r = r.WithContext(templates.WithWebhooksEnabled(r.Context(), s.flags.Enabled(r.Context(), page.Org.OrgID, "webhooks")))
	}

	component := content
	if !wantsFragment(r) {
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

// HXRedirect forces a full browser navigation via the HX-Redirect response
// header. Correct when the destination is another origin, or when the whole
// document must be rebuilt (auth boundary, layout change) — it costs a page
// load, which re-initializes clerk-js.
func HXRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
	w.WriteHeader(http.StatusOK)
}

// Redirect is the hard-navigation form: HX-Redirect for htmx requests, 303 See
// Other for plain ones. Use Navigate instead for in-app destinations.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if IsHX(r) {
		HXRedirect(w, url)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Navigate is the soft-navigation form for in-app destinations: htmx clients
// get HX-Location, which re-fetches the destination over AJAX and swaps its
// ContentTarget into ours. History is pushed, but the shell — and every clerk-js
// widget and Alpine component living in it — is never touched, so there is no
// re-mount flash. Plain clients get a 303.
//
// target/select are mandatory: HX-Location swaps <body> by default, which would
// destroy the very widgets this app takes care to keep mounted. Naming the
// content box also makes the request self-describing — wantsFragment reads
// HX-Target and returns the complete page, whose <title> and <meta> htmx then
// applies to the document.
//
// The swap mirrors what a nav link does, transition included, so a
// server-driven navigation is indistinguishable from a clicked one.
//
// Pair this with Toast, never FlashToast: the document is not reloaded, so a
// sessionStorage flash would sit unread until the next real page load.
func Navigate(w http.ResponseWriter, r *http.Request, url string) {
	if !IsHX(r) {
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"path":   url,
		"target": ContentTarget,
		"select": ContentTarget,
		"swap":   NavSwap,
	})
	w.Header().Set("HX-Location", string(payload))
	w.WriteHeader(http.StatusOK)
}

// Toast queues a client toast via HX-Trigger for NON-navigating responses
// (fragment swaps, row deletes). The listener lives in static/app.js.
func Toast(w http.ResponseWriter, typ, message string) {
	setToast(w, typ, message, false)
}

// FlashToast toasts across an HX-Redirect (full browser navigation): the
// client stores it in sessionStorage and shows it after the page loads.
func FlashToast(w http.ResponseWriter, typ, message string) {
	setToast(w, typ, message, true)
}

func setToast(w http.ResponseWriter, typ, message string, flash bool) {
	payload, _ := json.Marshal(map[string]map[string]any{
		"toast": {"type": typ, "message": message, "flash": flash},
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
