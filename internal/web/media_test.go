package web

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onePixelPNG is a real, decodable PNG: http.DetectContentType sniffs the
// signature, so a fixture that only looks like one would prove nothing.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// mediaServer stores uploads in a temp directory so the test can assert that
// a rejected upload leaves nothing behind.
func mediaServer(t *testing.T, id string) (*Server, *http.Cookie, string) {
	t.Helper()
	root := t.TempDir()
	s := integrationServer(t, func(d *Deps) { d.Storage = storagefs.NewDevStore(root) })
	return s, staffUser(t, s, "user_"+id, "org_"+id, identity.RoleAdmin), root
}

// listAllMedia is the "give me everything" page for assertions.
func listAllMedia() sqlc.ListMediaParams {
	return sqlc.ListMediaParams{Lim: 100, Off: 0}
}

// postFormMedia is postForm with no body — the delete buttons post nothing.
func postFormMedia(t *testing.T, s *Server, target string, cookies ...*http.Cookie) (int, http.Header, string) {
	t.Helper()
	return postForm(t, s, target, url.Values{}, cookies...)
}

// uploadFile issues a CSRF-tokened multipart POST.
func uploadFile(t *testing.T, s *Server, target, field, filename, declaredType string, payload []byte, cookies ...*http.Cookie) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename)}
	h["Content-Type"] = []string{declaredType}
	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	token, csrfCookies := csrfFor(t, s)
	req := httptest.NewRequest("POST", target, bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", token)
	for _, c := range append(append([]*http.Cookie{}, csrfCookies...), cookies...) {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// An image uploaded through the admin renders IN the page: the whole point of
// ServeInline, and the reason the type is sniffed rather than trusted.
func TestMediaUploadServesInline(t *testing.T) {
	s, admin, _ := mediaServer(t, "media1")

	code, _ := uploadFile(t, s, "/admin/media", "file", "pixel.png", "image/png", onePixelPNG, admin)
	require.Equal(t, http.StatusOK, code)

	items, err := s.q.ListMedia(t.Context(), listAllMedia())
	require.NoError(t, err)
	require.Len(t, items, 1)
	m := items[0]
	t.Cleanup(func() { _ = s.q.DeleteMedia(t.Context(), m.ID) })
	assert.Equal(t, "image/png", m.ContentType)
	assert.EqualValues(t, len(onePixelPNG), m.SizeBytes, "the sniffed prefix is not lost from the stored object")

	code, hdr, body := serve(t, s, "GET", fmt.Sprintf("/media/%d/pixel.png", m.ID), nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "image/png", hdr.Get("Content-Type"))
	assert.Contains(t, hdr.Get("Content-Disposition"), "inline")
	assert.Contains(t, hdr.Get("Cache-Control"), "immutable")
	assert.Equal(t, onePixelPNG, []byte(body), "bytes in, bytes out")
}

// The client's part header is a claim, not evidence. HTML renamed .png and
// declared image/png must be refused, with no row and no object left behind.
func TestMediaUploadRejectsSniffedNonImage(t *testing.T) {
	s, admin, root := mediaServer(t, "media2")
	evil := []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")

	code, body := uploadFile(t, s, "/admin/media", "file", "evil.png", "image/png", evil, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "PNG")

	items, err := s.q.ListMedia(t.Context(), listAllMedia())
	require.NoError(t, err)
	assert.Empty(t, items, "a rejected upload leaves no row")

	var stored []string
	_ = filepath.Walk(filepath.Join(root, "content"), func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			stored = append(stored, p)
		}
		return nil
	})
	assert.Empty(t, stored, "a rejected upload leaves no object")
}

func TestMediaDeleteRemovesRowAndObject(t *testing.T) {
	s, admin, root := mediaServer(t, "media3")
	code, _ := uploadFile(t, s, "/admin/media", "file", "gone.png", "image/png", onePixelPNG, admin)
	require.Equal(t, http.StatusOK, code)

	items, err := s.q.ListMedia(t.Context(), listAllMedia())
	require.NoError(t, err)
	require.Len(t, items, 1)
	m := items[0]

	code, _, _ = postFormMedia(t, s, fmt.Sprintf("/admin/media/%d/delete", m.ID), admin)
	require.Equal(t, http.StatusOK, code)

	items, err = s.q.ListMedia(t.Context(), listAllMedia())
	require.NoError(t, err)
	assert.Empty(t, items)
	_, statErr := os.Stat(filepath.Join(root, m.StorageKey))
	assert.True(t, os.IsNotExist(statErr), "the object goes with the row")

	code, _, _ = serve(t, s, "GET", fmt.Sprintf("/media/%d/gone.png", m.ID), nil, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestMediaUnknownIDIsNotFound(t *testing.T) {
	s, _, _ := mediaServer(t, "media4")
	for _, target := range []string{"/media/999999/x.png", "/media/nope/x.png"} {
		code, _, _ := serve(t, s, "GET", target, nil, nil)
		assert.Equal(t, http.StatusNotFound, code, target)
	}
}
