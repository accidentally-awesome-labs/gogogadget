package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"github.com/justinas/nosurf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The CSRF token reaches the browser twice - an inherited request header htmx
// reads, and a hidden field inside every unsafe form - because neither path can
// serve the other's client. A fragment request has no form to draw a field from;
// a submit made with no JavaScript has no htmx to set a header. These tests hold
// both paths open at once, because "the token is published" was true of the
// header alone for months while every no-script POST was refused with 403.

// The field ui.Form renders and the field the middleware reads must be the same
// name. ui is the presentation leaf and cannot import nosurf, so the two
// spellings are pinned together here, where both are visible.
func TestFormFieldIsTheOneNosurfReads(t *testing.T) {
	assert.Equal(t, nosurf.FormFieldName, ui.CSRFFieldName)
}

var hiddenCSRFFieldRe = regexp.MustCompile(
	`<input type="hidden" name="` + ui.CSRFFieldName + `" value="([^"]+)">`)

// normalizeCSRF replaces every rendered token with a fixed placeholder, for the
// tests that compare two renders of the same page byte for byte.
//
// nosurf masks the real token with a fresh one-time pad on every render - that
// is the BREACH mitigation, and it means two *correct* renders differ in exactly
// these bytes and nowhere else. Normalising is therefore the honest fix; making
// the token stable would trade a defence for a convenience.
func normalizeCSRF(html string) string {
	return csrfFieldValueRe.ReplaceAllString(html, `name="`+ui.CSRFFieldName+`" value="TOKEN"`)
}

var csrfFieldValueRe = regexp.MustCompile(`name="` + ui.CSRFFieldName + `" value="[^"]*"`)

// noScriptToken returns the hidden field's value and the cookie it belongs to:
// exactly what a browser with no JavaScript carries away from a page render.
func noScriptToken(t *testing.T, s *Server, target string) (string, []*http.Cookie) {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	m := hiddenCSRFFieldRe.FindStringSubmatch(rec.Body.String())
	require.Len(t, m, 2, "an unsafe form must render a hidden %s field", ui.CSRFFieldName)
	return m[1], rec.Result().Cookies()
}

// The no-script path: the token travels in the body, no header is set, and the
// mutation is accepted. Asserting the echoed value rather than only the status
// is what separates "the check passed" from "the handler ran".
func TestNoScriptFormPostIsAccepted(t *testing.T) {
	s := integrationServer(t, nil)
	token, cookies := noScriptToken(t, s, "/dev/gallery")

	form := url.Values{"dev_date": {"2026-03-04"}, ui.CSRFFieldName: {token}}
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}

	code, _, body := serve(t, s, http.MethodPost, "/dev/ui/calendar/select",
		[]byte(form.Encode()), h, cookies...)

	require.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.Contains(t, body, "The server has 2026-03-04.")
}

// The scripted path over the same token family, with the header and no field.
// nosurf prefers the header, so the two paths coexist rather than compete - and
// a change that makes the field authoritative would fail here.
func TestScriptedHeaderPostIsStillAccepted(t *testing.T) {
	s := integrationServer(t, nil)
	token, cookies := noScriptToken(t, s, "/dev/gallery")

	h := http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
		"HX-Request":   {"true"},
		"X-CSRF-Token": {token},
	}

	code, _, body := serve(t, s, http.MethodPost, "/dev/ui/calendar/select",
		[]byte(url.Values{"dev_date": {"2026-03-04"}}.Encode()), h, cookies...)

	require.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.Contains(t, body, "The server has 2026-03-04.")
}

// A POST carrying neither is still refused. Without this the two tests above
// would pass just as happily against a middleware that stopped checking.
func TestFormPostWithoutATokenIsRefused(t *testing.T) {
	s := integrationServer(t, nil)
	_, cookies := noScriptToken(t, s, "/dev/gallery")

	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}

	code, _, _ := serve(t, s, http.MethodPost, "/dev/ui/calendar/select",
		[]byte(url.Values{"dev_date": {"2026-03-04"}}.Encode()), h, cookies...)

	require.Equal(t, http.StatusForbidden, code)
}
