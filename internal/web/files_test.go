package web

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadBody builds a multipart/form-data body with one file field.
func uploadBody(t *testing.T, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

func fileIDFromList(t *testing.T, body string) int64 {
	t.Helper()
	m := regexp.MustCompile(`file-(\d+)`).FindStringSubmatch(body)
	require.Len(t, m, 2, "a file row must be present")
	id, err := strconv.ParseInt(m[1], 10, 64)
	require.NoError(t, err)
	return id
}

func fileParamsFor(orgID, filename string, size int64) sqlc.InsertFileParams {
	return sqlc.InsertFileParams{
		ClerkOrgID: orgID, UploaderUserID: "user_seed", Filename: filename,
		ContentType: "application/octet-stream", SizeBytes: size,
		StorageKey: storage.NewKey(orgID, filename),
	}
}

func TestFileUploadListDownloadDelete(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_files", "org_files", "org:admin")
	cookie := sessionCookie("user_files", "org_files", "org:admin")

	// Upload (multipart POST needs CSRF like every mutating form).
	token, csrfCookies := csrfFor(t, s)
	body, ctype := uploadBody(t, "hello.txt", "byte-identical payload")
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", ctype)
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/app/files", body.Bytes(), h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code, "upload succeeds (Navigate response)")

	// Listed.
	code, _, listBody := serve(t, s, "GET", "/app/files", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, listBody, "hello.txt")
	assert.Contains(t, listBody, `data-testid="file-row"`)

	// Download returns the exact bytes with attachment disposition.
	id := fileIDFromList(t, listBody)
	req := httptest.NewRequest(http.MethodGet, "/app/files/"+strconv.FormatInt(id, 10), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	b, _ := io.ReadAll(rec.Body)
	assert.Equal(t, "byte-identical payload", string(b))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")

	// Row delete: 200 empty (htmx removes the row).
	h2 := http.Header{}
	h2.Set("X-CSRF-Token", token)
	code, _, delBody := serve(t, s, "DELETE", "/app/files/"+strconv.FormatInt(id, 10), nil, h2, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, delBody)

	code, _, listBody = serve(t, s, "GET", "/app/files", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, listBody, "hello.txt")
}

func TestFileUploadQuotaRejected(t *testing.T) {
	// Free plan = 50 MB. Seed usage one byte under the cap; any upload tips
	// it over and must re-render with the upgrade CTA (422).
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_quota", "org_quota", "org:admin")

	// Seed usage exactly AT the cap: even one more byte must be rejected.
	_, err := s.q.InsertFile(t.Context(), fileParamsFor("org_quota", "big.bin", 50*1024*1024))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM files WHERE clerk_org_id = 'org_quota'")
	})

	cookie := sessionCookie("user_quota", "org_quota", "org:admin")
	token, csrfCookies := csrfFor(t, s)
	body, ctype := uploadBody(t, "over.txt", "x")
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", ctype)
	h.Set("HX-Request", "true")
	code, _, resBody := serve(t, s, "POST", "/app/files", body.Bytes(), h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusUnprocessableEntity, code, "over-quota upload → 422")
	assert.Contains(t, resBody, `data-testid="storage-limit"`, "upgrade CTA rendered")
}

func TestFileCrossOrgIs404(t *testing.T) {
	s := integrationServer(t, nil)
	seedOrg(t, s, "org_other", "org_other")

	f, err := s.q.InsertFile(t.Context(), fileParamsFor("org_other", "secret.txt", 3))
	require.NoError(t, err)

	// A member of a DIFFERENT org gets 404, never the bytes.
	cookie := sessionCookie("user_x", "org_x", "org:admin")
	req := httptest.NewRequest(http.MethodGet, "/app/files/"+strconv.FormatInt(f.ID, 10), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFilesPageShowsMeterOnFreePlan(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_meter", "org_meter", "org:admin")
	cookie := sessionCookie("user_meter", "org_meter", "org:admin")
	code, _, body := serve(t, s, "GET", "/app/files", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="storage-meter"`)
	assert.True(t, strings.Contains(body, "50"), "plan cap rendered")
}

// The pager swaps innerMorph into #table-container, whose wrapper FilesTable
// must keep because the upload targets it with outerHTML. The GET fragment is
// therefore a different renderer — the box's contents — or every page click
// leaves a second element carrying the same id behind.
func TestFilesFragmentDoesNotRepeatItsWrapper(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_ffrag", "org_ffrag", "org:admin")
	cookie := sessionCookie("user_ffrag", "org_ffrag", "org:admin")

	h := http.Header{}
	h.Set("HX-Request", "true")
	code, _, fragment := serve(t, s, "GET", "/app/files", nil, h, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, fragment, `data-testid="files-table"`, "the fragment is the table")
	assert.NotContains(t, fragment, `id="table-container"`,
		"the innerMorph target's wrapper belongs to the page, not to its own fragment")

	code, _, page := serve(t, s, "GET", "/app/files", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, strings.Count(page, `id="table-container"`), "exactly one swap target")
	// The upload's own outerHTML target, by contrast, must carry it.
	assert.Contains(t, page, `hx-target="#table-container"`)
}

func TestFileDeleteIsGatedByAnInPageDialog(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_fcfm", "org_fcfm", "org:admin")
	f, err := s.q.InsertFile(t.Context(), fileParamsFor("org_fcfm", "gated.txt", 12))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM files WHERE clerk_org_id = 'org_fcfm'")
	})

	id := strconv.FormatInt(f.ID, 10)
	code, _, body := serve(t, s, "GET", "/app/files", nil, nil, sessionCookie("user_fcfm", "org_fcfm", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "hx-confirm",
		"this page must not fall back to window.confirm; the repo-wide ban is "+
			"TestNoProductionTemplateFallsBackToWindowConfirm in internal/web/templates")
	assert.Contains(t, body, `data-ui="confirm-action"`)
	assert.Contains(t, body, `id="file-delete-`+id+`"`, "one dialog per row, addressable by id")
	// Unchanged delete contract: the request only moved onto the confirm button.
	assert.Contains(t, body, `hx-delete="/app/files/`+id+`"`)
	assert.Contains(t, body, `hx-target="closest tr"`)
	assert.Contains(t, body, `hx-swap="outerHTML"`)
}

func TestServerDefaultsToDevStore(t *testing.T) {
	s := integrationServer(t, nil)
	_, ok := s.store.(*storage.DevStore)
	assert.True(t, ok, "unconfigured server uses DevStore, never nil")
}
