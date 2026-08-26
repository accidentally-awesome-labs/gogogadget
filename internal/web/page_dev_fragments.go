package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// The interactive catalog fragments behind /dev/ui/{component}/{action}.
//
// Each handler answers with the status, headers and body shape its production
// counterpart uses: 422 carrying a re-rendered field for a rejected form, an
// empty 200 for a row delete, a fragment and never a page. A demo that answered
// 200 where the product answers 422, or that returned a row where the product
// returns nothing, would teach a contract the product does not honour - and the
// reader would only find that out in their own application.
//
// Nothing is stored between requests. Every response is derived from the
// request's own parameters, so two browsers on the same example cannot see each
// other's actions and a reload always returns to the starting point.

// renderDevFragment writes one fragment with an explicit status.
//
// The status is written before the body because a render that fails halfway has
// already written bytes: a WriteHeader afterwards is ignored, and what gets
// logged is the superfluous call rather than the failure that caused it.
func (s *Server) renderDevFragment(w http.ResponseWriter, r *http.Request, status int, fragment templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := fragment.Render(r.Context(), w); err != nil {
		s.log.Error("render", "error", err, "path", r.URL.Path)
	}
}

// POST /dev/ui/toast/show — the toast example.
//
// The kind is passed through rather than validated. An unrecognised kind is a
// documented ui contract - the component renders its neutral variant and marks
// itself data-ui-invalid - and rejecting it here would hide the behaviour a
// reader on this page is looking for.
func (s *Server) handleDevToastShow(w http.ResponseWriter, r *http.Request) {
	s.renderDevFragment(w, r, http.StatusOK, templates.DevToastFragment(r.FormValue("kind")))
}

// POST /dev/ui/copy/confirm — records the reveal and returns the copied row.
//
// A request with no value is refused rather than confirmed: the row would
// otherwise report a copy the server was never told about, which is the one
// thing this example exists to disprove.
func (s *Server) handleDevCopyConfirm(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.FormValue("value")) == "" {
		s.renderDevFragment(w, r, http.StatusUnprocessableEntity, templates.DevCopyRow(false))
		return
	}
	s.renderDevFragment(w, r, http.StatusOK, templates.DevCopyRow(true))
}

// POST /dev/ui/upload/receive — the upload example.
//
// devUploadMaxMemory is well under the global request cap; anything larger
// spills to a temp file, which is the standard library's business and not a
// limit this example is trying to demonstrate.
const devUploadMaxMemory = 1 << 20

func (s *Server) handleDevUploadReceive(w http.ResponseWriter, r *http.Request) {
	// A malformed or file-less submission is a validation failure, not a server
	// fault: it re-renders the field with its error, the same as a rejected
	// type. Answering 500 would teach the reader that a missing file is the
	// server's problem.
	if err := r.ParseMultipartForm(devUploadMaxMemory); err != nil {
		s.renderDevUploadError(w, r, templates.DevUploadState{Reason: templates.DevUploadMissing})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.renderDevUploadError(w, r, templates.DevUploadState{Reason: templates.DevUploadMissing})
		return
	}
	defer file.Close()

	declared := header.Header.Get("Content-Type")
	if !templates.DevUploadAllows(declared) {
		s.renderDevUploadError(w, r, templates.DevUploadState{
			Filename: header.Filename, DeclaredType: declared, Reason: templates.DevUploadUnsupported,
		})
		return
	}
	s.renderDevFragment(w, r, http.StatusOK, templates.DevUploadFragment(templates.DevUploadState{
		Filename: header.Filename, DeclaredType: declared, Size: header.Size,
	}))
}

func (s *Server) renderDevUploadError(w http.ResponseWriter, r *http.Request, state templates.DevUploadState) {
	s.renderDevFragment(w, r, http.StatusUnprocessableEntity, templates.DevUploadFragment(state))
}

// DELETE /dev/ui/row/delete — the row-delete example: 200 with an EMPTY body.
//
// Empty is the whole contract. The trigger targets `closest tr` and swaps
// outerHTML, so htmx replaces the row with whatever comes back - a body here
// would leave the row on screen showing the response instead of removing it.
// The toast rides the HX-Trigger header for the same reason: there is nowhere in
// an empty body to put a message.
func (s *Server) handleDevRowDelete(w http.ResponseWriter, r *http.Request) {
	if !templates.DevTableHasRow(r.URL.Query().Get("row")) {
		s.handleNotFound(w, r)
		return
	}
	Toast(w, "success", "Row deleted")
	w.WriteHeader(http.StatusOK)
}

// GET /dev/ui/table/sort — the sortable table example.
func (s *Server) handleDevTableSort(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	s.renderDevFragment(w, r, http.StatusOK,
		templates.DevTableFragment(query.Get("sort"), ui.SortDirection(query.Get("dir"))))
}

// POST /dev/ui/form/save — the validated form example.
//
// Both outcomes render the same fragment; only the status and the field errors
// differ. That is what makes the 422 useful to copy: the rejected submission
// stays on screen with its values, and the error is attached to the field that
// caused it rather than announced somewhere else on the page.
func (s *Server) handleDevFormSave(w http.ResponseWriter, r *http.Request) {
	state := templates.DevSaveState{
		Name:  strings.TrimSpace(r.FormValue("dev_name")),
		Email: strings.TrimSpace(r.FormValue("dev_email")),
	}
	if state.Name == "" {
		state.NameErr = "Enter a display name."
	}
	// Deliberately not a full address grammar: the server's job in this example
	// is to be the authority, and a rule the reader can predict makes the 422
	// reproducible. A real handler validates as strictly as it must.
	if !strings.Contains(state.Email, "@") {
		state.EmailErr = "Enter an email address, like you@example.com."
	}
	if state.NameErr != "" || state.EmailErr != "" {
		s.renderDevFragment(w, r, http.StatusUnprocessableEntity, templates.DevSaveFragment(state))
		return
	}
	state.Saved = true
	s.renderDevFragment(w, r, http.StatusOK, templates.DevSaveFragment(state))
}

// POST /dev/ui/calendar/select — the date example.
//
// The posted value is parsed and range-checked here even though the input
// carries min and max: the picker writes into that input, and a bound enforced
// only in the browser is not enforced at all.
func (s *Server) handleDevCalendarSelect(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.FormValue("dev_date"))
	if err := devCalendarValid(value); err != "" {
		s.renderDevFragment(w, r, http.StatusUnprocessableEntity,
			templates.DevCalendarFragment(templates.DevCalendarState{Value: "", Err: err}))
		return
	}
	s.renderDevFragment(w, r, http.StatusOK,
		templates.DevCalendarFragment(templates.DevCalendarState{Value: value}))
}

// devCalendarValid returns the message for a rejected date, or empty when the
// date is usable. A rejected value is not echoed back into the input: an input
// holding a date the server refused claims a selection that does not exist.
func devCalendarValid(value string) string {
	if value == "" {
		return "Choose a date."
	}
	if _, err := time.Parse(time.DateOnly, value); err != nil {
		return "Enter a date as YYYY-MM-DD."
	}
	if value < templates.DevCalendarMin || value > templates.DevCalendarMax {
		return "Choose a date in 2026."
	}
	return ""
}

// POST /dev/ui/editor/preview — the Markdown preview example.
//
// Same pipeline as /admin/content/preview, and for the same reason: content.Render
// is the one goldmark instance in the program, it runs without html.WithUnsafe,
// and the escaped HTML it returns is the only string in the system allowed to
// reach templ.Raw. A preview rendered any other way would be reviewing output
// no reader will ever be served.
func (s *Server) handleDevEditorPreview(w http.ResponseWriter, r *http.Request) {
	html, err := content.Render([]byte(r.FormValue(templates.DevEditorField)))
	if err != nil {
		// A fragment route answers with a fragment even when it fails: the
		// response lands inside the preview pane, and a full error page there
		// would nest the site in a box on the page.
		s.log.Error("dev preview", "error", err, "path", r.URL.Path)
		s.renderDevFragment(w, r, http.StatusUnprocessableEntity, templates.DevEditorPreviewError())
		return
	}
	s.renderDevFragment(w, r, http.StatusOK, templates.ContentPreview(html))
}

// GET /dev/ui/overlay/open — the lazily loaded overlay panel.
func (s *Server) handleDevOverlayOpen(w http.ResponseWriter, r *http.Request) {
	if !templates.DevOverlayHasPanel(r.URL.Query().Get("panel")) {
		s.handleNotFound(w, r)
		return
	}
	s.renderDevFragment(w, r, http.StatusOK, templates.DevOverlayPanel())
}
