package web

import (
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// The dev catalog: component references and scenario surfaces.
//
// Every route here is registered only in zero-account mode, which config refuses
// under APP_ENV=production, so none of it can reach a live site. Unknown names
// return the ordinary dev 404 rather than a catalog-specific error: a distinct
// response would let anyone enumerate what exists.

// GET /dev/gallery/{family} — one family's components.
func (s *Server) handleDevGalleryFamily(w http.ResponseWriter, r *http.Request) {
	family := ui.GalleryFamily(r.PathValue("family"))
	// Valid, not Value: normalizing here would render foundations for every
	// typo, and a URL that silently shows a different page teaches the reader
	// the wrong thing.
	if !family.Valid() {
		s.handleNotFound(w, r)
		return
	}
	s.Render(w, r, Page{
		Title:  "Components — " + string(family),
		Layout: templates.LayoutPublic,
	}, templates.FamilyPage(family))
}

// GET /dev/gallery/{family}/{component} — one component's reference.
func (s *Server) handleDevGalleryComponent(w http.ResponseWriter, r *http.Request) {
	family := ui.GalleryFamily(r.PathValue("family"))
	component, ok := ui.ComponentByName(r.PathValue("component"))
	// The family segment has to agree with the component's own family. Accepting
	// any family would make the segment decorative and give every component two
	// URLs to keep in sync.
	if !ok || !family.Valid() || component.Family != family {
		s.handleNotFound(w, r)
		return
	}
	s.Render(w, r, Page{
		Title:  component.Name,
		Layout: templates.LayoutPublic,
	}, templates.ComponentPage(component))
}

// GET /dev/scenarios/{scenario} — one realistic product surface.
func (s *Server) handleDevScenario(w http.ResponseWriter, r *http.Request) {
	scenario, ok := templates.ScenarioBySlug(r.PathValue("scenario"))
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	// A state the scenario does not declare is refused rather than rendered as
	// the default: a URL that looks like it selected something and did not is
	// worse than a missing page.
	state := r.URL.Query().Get("state")
	if !scenario.HasState(state) {
		s.handleNotFound(w, r)
		return
	}
	// The other axes are validated the same way and for the same reason. A URL
	// that looks like it selected rtl and rendered ltr is a reviewer arguing
	// about a screenshot that was never taken.
	context, ok := templates.ScenarioContextFrom(
		state, scenarioPage(r),
		r.URL.Query().Get(templates.TextDirectionKey),
		r.URL.Query().Get("content"),
		r.URL.Query().Get("density"),
	)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	// An app or admin scenario renders the real signed-in shell, which mounts
	// clerk-js and bounces a visitor with no session straight to sign-in - so
	// the scenario would be unreachable in a browser. Rather than fabricate
	// context and render a shell that behaves differently from production, give
	// the visitor the same synthetic dev session /dev/login issues and re-enter
	// through the ordinary middleware chain. The second request is
	// indistinguishable from a real one.
	//
	// The retry is bounded by a marker. Whether the session takes depends on the
	// demo user existing, which it does not on a database that has never been
	// seeded - and an unbounded "set cookie, try again" is an infinite redirect
	// the moment that assumption fails.
	layout := scenario.Layout
	if scenarioNeedsSession(scenario) && identity.ClaimsFrom(r.Context()) == nil {
		if r.URL.Query().Get("session") != "retried" {
			s.setDevSessionCookie(w, "user_demo", "org_demo", "org:admin")
			http.Redirect(w, r, scenarioRetryURL(r), http.StatusSeeOther)
			return
		}
		// The session did not take. Render the scenario rather than looping, in
		// the shell that works without one, and say why the other is missing.
		layout = templates.LayoutPublic
	}
	s.Render(w, r, Page{
		Title:  scenario.Title,
		Layout: layout,
	}, templates.ScenarioPage(scenario, context))
}

// scenarioPage reads the 1-based page a paged scenario is showing. An
// unparseable or out-of-range value clamps to the first page rather than 404ing:
// unlike a state, a page is not a claim about which surface is rendered.
func scenarioPage(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// scenarioRetryURL adds the one-shot marker while preserving the state the
// visitor asked for, so signing in does not silently drop them on default.
func scenarioRetryURL(r *http.Request) string {
	target := *r.URL
	query := target.Query()
	query.Set("session", "retried")
	target.RawQuery = query.Encode()
	return target.RequestURI()
}

// scenarioNeedsSession reports whether this scenario's shell assumes a signed-in
// user. The public shell does not, so a public scenario must not be handed a
// session it never asked for.
func scenarioNeedsSession(scenario templates.Scenario) bool {
	return scenario.Layout == templates.LayoutApp || scenario.Layout == templates.LayoutAdmin
}

// GET|POST|DELETE /dev/ui/{component}/{action} — interactive example fragments.
//
// One route rather than a handler per example: the component and action are
// data, so adding an interactive example is a case here and a demo in the
// gallery, not a new registry entry. Unknown pairs are 404 so a demo wired to a
// typo fails visibly instead of looking like it worked.
func (s *Server) handleDevUIAction(w http.ResponseWriter, r *http.Request) {
	handler, method := s.devUIFragment(r.PathValue("component") + "/" + r.PathValue("action"))
	if handler == nil || method != r.Method {
		s.handleNotFound(w, r)
		return
	}
	handler(w, r)
}

// devUIFragment resolves one example to its handler and the single method it
// answers on.
//
// A table returning the method rather than an arm per example that checks
// r.Method itself: with a dozen examples that check is written a dozen times,
// and the one that gets it wrong accepts GET for a mutation - which the CSRF
// middleware does not cover, because it only guards mutating methods.
func (s *Server) devUIFragment(name string) (http.HandlerFunc, string) {
	switch name {
	case "pagination/page", "search/query":
		return s.renderDevPagerFragment, http.MethodGet
	case "table/sort":
		return s.handleDevTableSort, http.MethodGet
	case "overlay/open":
		return s.handleDevOverlayOpen, http.MethodGet
	case "kanban/move":
		return s.handleDevKanbanMove, http.MethodPost
	case "toast/show":
		return s.handleDevToastShow, http.MethodPost
	case "copy/confirm":
		return s.handleDevCopyConfirm, http.MethodPost
	case "upload/receive":
		return s.handleDevUploadReceive, http.MethodPost
	case "form/save":
		return s.handleDevFormSave, http.MethodPost
	case "calendar/select":
		return s.handleDevCalendarSelect, http.MethodPost
	case "editor/preview":
		return s.handleDevEditorPreview, http.MethodPost
	case "row/delete":
		return s.handleDevRowDelete, http.MethodDelete
	default:
		return nil, ""
	}
}

// renderDevPagerFragment returns just the pager region.
//
// A fragment, not a page: the demo swaps itself, so returning the whole gallery
// would nest the page inside the region it was swapping. The parameters are read
// from the query exactly as a real paged table reads them, which is what makes
// the demo worth looking at.
func (s *Server) renderDevPagerFragment(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		page = 1
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.GalleryPagerBody(page, r.URL.Query().Get("q")).Render(r.Context(), w)
}
