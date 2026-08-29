package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The interactive catalog examples are only worth having if they answer exactly
// like the handlers they demonstrate. These tests pin the three things a reader
// would copy: the status code, that the body is a fragment rather than a page,
// and the one behaviour that makes each example true - an empty body for a row
// delete, a 422 carrying the field error for a rejected submission, a single
// aria-sort for a sorted table, and a preview that never returns live markup.

// devFragment issues one example request through the full stack with the CSRF
// token a browser would carry. The fragment routes are not exempt, so a mutation
// with no token is rejected by the middleware and proves nothing about the
// handler behind it.
func devFragment(t *testing.T, s *Server, method, target string, form url.Values) (int, http.Header, string) {
	t.Helper()
	if method == http.MethodPost {
		return postForm(t, s, target, form)
	}
	h := http.Header{"HX-Request": {"true"}}
	var cookies []*http.Cookie
	if method != http.MethodGet {
		token, csrfCookies := csrfFor(t, s)
		h.Set("X-CSRF-Token", token)
		cookies = csrfCookies
	}
	return serve(t, s, method, target, nil, h, cookies...)
}

// devFragmentActions is every interactive example and the single method it
// answers on.
func devFragmentActions() map[string]string {
	return map[string]string{
		"toast/show":      http.MethodPost,
		"copy/confirm":    http.MethodPost,
		"upload/receive":  http.MethodPost,
		"form/save":       http.MethodPost,
		"calendar/select": http.MethodPost,
		"editor/preview":  http.MethodPost,
		"row/delete":      http.MethodDelete,
		"table/sort":      http.MethodGet,
		"overlay/open":    http.MethodGet,
	}
}

// A toast is announced according to how much it matters. Reading a failure the
// user must act on and a save they already expected the same way trains them to
// ignore both.
func TestDevFragmentToastAnnouncesBySeverity(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/toast/show", url.Values{"kind": {"success"}})
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html", "a swap target must receive a fragment, never a page")
	assert.Contains(t, body, `role="status"`)
	assert.Contains(t, body, "Project saved.")
	// The id is what lets innerMorph replace the toast instead of stacking one.
	assert.Contains(t, body, `id="dev-toast"`)

	code, _, body = devFragment(t, s, http.MethodPost, "/dev/ui/toast/show", url.Values{"kind": {"danger"}})
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `role="alert"`)
}

// The copied state is the server's answer, so it must not be given for a request
// that named nothing to copy.
func TestDevFragmentCopyConfirmsOnlyWhatItWasTold(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/copy/confirm",
		url.Values{"value": {"ggg_live_9f2c41d8a7b3"}})
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `data-testid="dev-copy-recorded"`)
	// outerMorph replaces the row with itself, so the id has to come back.
	assert.Contains(t, body, `id="dev-copy-row"`)

	code, _, body = devFragment(t, s, http.MethodPost, "/dev/ui/copy/confirm", url.Values{"value": {"   "}})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.NotContains(t, body, `data-testid="dev-copy-recorded"`)
}

// An accepted upload answers with the file row; a refused one answers 422 with
// the error attached to the field, which is the element the input's
// aria-describedby points at.
func TestDevFragmentUploadRejectsUnsupportedDeclaredType(t *testing.T) {
	s := integrationServer(t, nil)

	code, body := uploadFile(t, s, "/dev/ui/upload/receive", "file", "diagram.png", "image/png", []byte("bytes"))
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `data-testid="dev-upload-row"`)
	assert.Contains(t, body, "diagram.png")

	code, body = uploadFile(t, s, "/dev/ui/upload/receive", "file", "payload.html", "text/html", []byte("<script>"))
	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.NotContains(t, body, "<html lang", "a 422 is still a fragment")
	assert.Contains(t, body, `data-testid="form-error"`)
	assert.Contains(t, body, `aria-invalid="true"`)
	// A file input cannot be repopulated from markup, so being told which file
	// was refused is the only way the user knows which one to replace.
	assert.Contains(t, body, "payload.html")
	assert.NotContains(t, body, `data-testid="dev-upload-row"`)
}

// Browsers append parameters to a declared type. Comparing the whole header
// value refuses a plain text file for stating its encoding.
func TestDevFragmentUploadIgnoresContentTypeParameters(t *testing.T) {
	s := integrationServer(t, nil)

	code, body := uploadFile(t, s, "/dev/ui/upload/receive", "file", "notes.txt",
		"text/plain; charset=utf-8", []byte("notes"))

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="dev-upload-row"`)
}

// A submission with no file is a validation failure, not a server fault.
func TestDevFragmentUploadWithoutAFileIsAValidationFailure(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/upload/receive", url.Values{})

	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, `data-testid="form-error"`)
	assert.Contains(t, body, "Choose a file")
}

// The row delete is the one contract an example can get subtly wrong and still
// look right: hx-swap="outerHTML" against `closest tr` replaces the row with the
// response body, so anything in that body leaves the row on screen.
func TestDevFragmentRowDeleteReturnsAnEmptyBody(t *testing.T) {
	s := integrationServer(t, nil)

	code, hdr, body := devFragment(t, s, http.MethodDelete, "/dev/ui/row/delete?row=apollo", nil)

	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, body, "a body here would render inside the row instead of removing it")
	// The message rides the header for exactly that reason: an empty body has
	// nowhere to put one.
	assert.Contains(t, hdr.Get("HX-Trigger"), "Row deleted")
}

// A row that was never in the fixture 404s. Answering an empty 200 would make
// the delete look like it worked when all it proved is that htmx removes
// whatever it targeted.
func TestDevFragmentRowDeleteRejectsAnUnknownRow(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := devFragment(t, s, http.MethodDelete, "/dev/ui/row/delete?row=nonsense", nil)

	assert.Equal(t, http.StatusNotFound, code)
}

// Exactly one column may claim to be the sorted one, and the rows have to be in
// the order it claims.
func TestDevFragmentTableSortMarksOneColumnAndOrdersRows(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodGet, "/dev/ui/table/sort?sort=name&dir=asc", nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Equal(t, 1, strings.Count(body, "aria-sort="),
		"aria-sort on a second column tells a screen-reader user the table is ordered two ways at once")
	assert.Contains(t, body, `aria-sort="ascending"`)
	assert.Less(t, strings.Index(body, "Apollo"), strings.Index(body, "Dorado"))

	_, _, body = devFragment(t, s, http.MethodGet, "/dev/ui/table/sort?sort=name&dir=desc", nil)
	assert.Contains(t, body, `aria-sort="descending"`)
	assert.Less(t, strings.Index(body, "Dorado"), strings.Index(body, "Apollo"))

	// A numeric column orders by value, not by the digits as text: 1,204 sorts
	// above 87 only if the sort reads the number.
	_, _, body = devFragment(t, s, http.MethodGet, "/dev/ui/table/sort?sort=runs&dir=desc", nil)
	assert.Equal(t, 1, strings.Count(body, "aria-sort="))
	assert.Less(t, strings.Index(body, "Apollo"), strings.Index(body, "Borealis"))
}

// An unsortable key leaves the fixture order alone and claims nothing. A table
// that is not sorted must not say that it is.
func TestDevFragmentTableSortIgnoresAnUnknownKey(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodGet, "/dev/ui/table/sort?sort=owner&dir=desc", nil)

	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, 0, strings.Count(body, "aria-sort="))
	assert.Less(t, strings.Index(body, "Dorado"), strings.Index(body, "Apollo"),
		"the fixture order, not a sorted one")
}

// A rejected form comes back with 422, the error on the field that caused it,
// and every value the user already got right.
func TestDevFragmentFormSaveRejectionKeepsTheSubmission(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/form/save",
		url.Values{"dev_name": {"Apollo"}, "dev_email": {"not-an-address"}})

	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `data-testid="form-error"`)
	assert.Contains(t, body, `aria-describedby="dev_email-error"`,
		"the error has to be the element the control describes itself with")
	assert.Contains(t, body, `value="Apollo"`, "a form that clears itself makes the user retype what was fine")
	assert.Contains(t, body, `value="not-an-address"`)
	assert.NotContains(t, body, `data-testid="dev-save-notice"`)
}

func TestDevFragmentFormSaveAcceptsAValidSubmission(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/form/save",
		url.Values{"dev_name": {"Apollo"}, "dev_email": {"ada@example.com"}})

	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `data-testid="dev-save-notice"`)
	assert.NotContains(t, body, `data-testid="form-error"`)
}

// The fragment echoes the value the SERVER holds, which is the only way to tell
// a completed round trip from a picker that wrote into the input locally.
func TestDevFragmentCalendarEchoesTheStoredValue(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/calendar/select",
		url.Values{"dev_date": {"2026-03-04"}})

	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `value="2026-03-04"`)
	assert.Contains(t, body, "The server has 2026-03-04.")
}

// min and max on the input are advisory: the picker writes into that input, so
// the server checks the window again.
func TestDevFragmentCalendarRejectsUnusableDates(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/calendar/select",
		url.Values{"dev_date": {"04/03/2026"}})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, `data-testid="form-error"`)
	assert.NotContains(t, body, `value="04/03/2026"`,
		"a refused value sitting in the input claims a selection the server does not have")

	code, _, body = devFragment(t, s, http.MethodPost, "/dev/ui/calendar/select",
		url.Values{"dev_date": {"2025-12-31"}})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "Choose a date in 2026.")
}

// The preview is the product's own goldmark instance, which runs without
// html.WithUnsafe. Markdown renders; HTML the author typed does not reach the
// page at all.
func TestDevFragmentEditorPreviewNeverReturnsLiveMarkup(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodPost, "/dev/ui/editor/preview",
		url.Values{"dev_body_md": {"**Bold** body\n\n<img src=x onerror=alert(1)>\n"}})

	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, "<strong>Bold</strong>", "the same pipeline the published page uses")
	assert.NotContains(t, body, "<img", "an img element here is a rendered payload")
	assert.NotContains(t, body, "onerror")
	assert.Contains(t, body, "raw HTML omitted",
		"goldmark drops the raw HTML and says so; a preview that escaped it as text would still be safe, one that rendered it would not")
}

// The panel loads into the drawer that is already open, so it is content and not
// a second dialog.
func TestDevFragmentOverlayReturnsThePanelBody(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := devFragment(t, s, http.MethodGet, "/dev/ui/overlay/open?panel=filters", nil)

	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `data-testid="dev-overlay-loaded"`)
	assert.NotContains(t, body, "<dialog", "nesting a dialog inside the open one traps focus in the wrong layer")
}

// A panel name the example does not serve 404s: the response claims to be a
// particular panel, so answering with a different one would make the parameter
// decorative and hide the typo that produced it.
func TestDevFragmentOverlayRejectsAnUnknownPanel(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := devFragment(t, s, http.MethodGet, "/dev/ui/overlay/open?panel=nonsense", nil)

	assert.Equal(t, http.StatusNotFound, code)
}

// Each example answers on one method. A mutation reachable by GET is outside
// what the CSRF middleware guards, because it only checks mutating methods.
func TestDevFragmentActionsAnswerOnOneMethodOnly(t *testing.T) {
	s := integrationServer(t, nil)

	for action, declared := range devFragmentActions() {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			if method == declared {
				continue
			}
			code, _, _ := devFragment(t, s, method, "/dev/ui/"+action, url.Values{})
			assert.Equal(t, http.StatusNotFound, code, "%s %s must not be served", method, action)
		}
	}
}

// renderDevExample renders one example's resting state, which is what the
// reference page embeds.
func renderDevExample(t *testing.T, example templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, example.Render(t.Context(), &buf))
	return buf.String()
}

// The resting state is half the contract: a handler that answers correctly is
// useless if the control that calls it posts to the wrong target or swaps the
// wrong way, and the reader copies the markup, not the handler.
func TestDevFragmentExamplesCarryTheProductionWiring(t *testing.T) {
	toast := renderDevExample(t, templates.DevToastExample())
	assert.Contains(t, toast, `hx-post="/dev/ui/toast/show"`)
	assert.Contains(t, toast, `hx-target="#dev-toast-region"`)
	assert.Contains(t, toast, `hx-swap="innerMorph"`)
	assert.NotContains(t, toast, `aria-live`,
		"the region must not be a live region: the toast it receives already is one")

	copyRow := renderDevExample(t, templates.DevCopyExample())
	assert.Contains(t, copyRow, `hx-post="/dev/ui/copy/confirm"`)
	assert.Contains(t, copyRow, `hx-swap="outerMorph"`, "the row replaces itself")
	assert.Contains(t, copyRow, `x-data="copy"`, "the clipboard write stays in the browser")

	table := renderDevExample(t, templates.DevTableExample("name", ui.SortAsc))
	// The exact production row-delete contract.
	assert.Contains(t, table, `hx-delete="/dev/ui/row/delete?row=apollo"`)
	assert.Contains(t, table, `hx-confirm="Delete Apollo? This cannot be undone."`)
	assert.Contains(t, table, `hx-target="closest tr"`)
	assert.Contains(t, table, `hx-swap="outerHTML"`)
	// And the sort links, which morph the table in place.
	assert.Contains(t, table, `hx-get="/dev/ui/table/sort?sort=name&amp;dir=desc"`)
	assert.Contains(t, table, `hx-swap="innerMorph"`)
	assert.Equal(t, 4, strings.Count(table, `<tr id="dev-row-`),
		"morph matches rows by id; without one it moves cell text between rows instead")

	for name, form := range map[string]string{
		"save":     renderDevExample(t, templates.DevSaveExample()),
		"upload":   renderDevExample(t, templates.DevUploadExample()),
		"calendar": renderDevExample(t, templates.DevCalendarExample()),
		"editor":   renderDevExample(t, templates.DevEditorExample()),
	} {
		assert.Contains(t, form, "novalidate", "%s: the browser must not block the server's rules", name)
		assert.Contains(t, form, `hx-disable="this"`, "%s: a second click must not repost", name)
		assert.Contains(t, form, "htmx-indicator", "%s: an in-flight request needs a spinner", name)
	}

	upload := renderDevExample(t, templates.DevUploadExample())
	assert.Contains(t, upload, `hx-encoding="multipart/form-data"`,
		"without the encoding htmx posts the field name and no bytes")

	editor := renderDevExample(t, templates.DevEditorExample())
	assert.Contains(t, editor, `hx-post="/dev/ui/editor/preview"`)
	assert.Contains(t, editor, "delay:400ms", "a preview per keystroke is a request per keystroke")
	assert.Contains(t, editor, "&lt;img src=x onerror=alert(1)&gt;",
		"the sample lives in the textarea as text, so the page cannot execute it before the server sees it")

	overlay := renderDevExample(t, templates.DevOverlayExample())
	assert.Contains(t, overlay, `hx-get="/dev/ui/overlay/open?panel=filters"`)
	assert.Contains(t, overlay, `hx-target="#dev-overlay-panel"`)
	assert.Contains(t, overlay, `x-data="uiDialog"`, "the platform supplies the modal, not this example")
}
