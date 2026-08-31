package storage_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/gogogadget/gogogadget/internal/storage/filesystem"
	s3store "github.com/gogogadget/gogogadget/internal/storage/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilesystemStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := filesystem.NewDevStore(root)
	ctx := context.Background()

	n, err := s.Put(ctx, "orgs/org_1/abc.txt", "text/plain", strings.NewReader("hello bytes"))
	require.NoError(t, err)
	assert.Equal(t, int64(11), n)

	// Served with attachment disposition and matching bytes.
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(ctx, rec, "orgs/org_1/abc.txt", "report.txt", "text/plain"))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `attachment; filename="report.txt"`, rec.Header().Get("Content-Disposition"))
	assert.Equal(t, "hello bytes", rec.Body.String())

	require.NoError(t, s.Delete(ctx, "orgs/org_1/abc.txt"))
	rec = httptest.NewRecorder()
	err = s.Serve(ctx, rec, "orgs/org_1/abc.txt", "x", "text/plain")
	require.Error(t, err, "deleted object must not be servable")
}

func TestFilesystemStoreContainsTraversal(t *testing.T) {
	root := t.TempDir()
	s := filesystem.NewDevStore(root)
	ctx := context.Background()

	// "../" cannot escape the root: Clean("/"+key) flattens it INSIDE root.
	_, err := s.Put(ctx, "../../etc/passwd", "text/plain", strings.NewReader("x"))
	require.NoError(t, err, "traversal is contained, not rejected")
	require.FileExists(t, filepath.Join(root, "etc", "passwd"), "object landed inside the sandbox")

	// No object ever resolves outside root.
	rec := httptest.NewRecorder()
	_ = s.Serve(ctx, rec, "orgs/../../../../etc/passwd", "x", "")
	assert.NotContains(t, rec.Header().Get("Content-Disposition"), "etc", "no root escape")
}
func TestFilesystemStoreFilenamesSanitized(t *testing.T) {
	s := filesystem.NewDevStore(t.TempDir())
	ctx := context.Background()
	_, err := s.Put(ctx, "k", "text/plain", strings.NewReader("x"))
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(ctx, rec, "k", `evil"; }.txt`, "text/plain"))
	disp := rec.Header().Get("Content-Disposition")
	assert.NotContains(t, disp, `"evil"`)
	assert.Contains(t, disp, "attachment")
}

func TestNewKeyShape(t *testing.T) {
	k := storage.NewKey("org_42", "Quarterly Report FINAL v2.pdf")
	dir, base := filepath.Split(k)
	assert.Equal(t, "orgs/org_42/", dir)
	// base = 32 hex chars + original ext (case preserved from the regexp).
	assert.Regexp(t, `^[0-9a-f]{32}\.pdf$`, base)
	k2 := storage.NewKey("org_42", "no-extension")
	_, base2 := filepath.Split(k2)
	assert.Regexp(t, `^[0-9a-f]{32}$`, base2)
	k3 := storage.NewKey("org_42", ".hidden-longextensionfile")
	_, base3 := filepath.Split(k3)
	assert.NotContains(t, base3, "hidden", "weird extensions are dropped, not glued")
}

// TestR2StorePutAgainstFakeEndpoint drives PutObject at an httptest server
// masquerading as S3 and asserts path-style addressing.
func TestR2StorePutAgainstFakeEndpoint(t *testing.T) {
	var gotPath, gotMethod, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><PutObjectOutput></PutObjectOutput>`))
	}))
	t.Cleanup(srv.Close)

	s, err := s3store.NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", "mybucket", srv.URL)
	require.NoError(t, err)

	n, err := s.Put(context.Background(), "orgs/o1/k1.bin", "application/octet-stream", strings.NewReader("PAYLOAD"))
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/mybucket/orgs/o1/k1.bin", gotPath, "path-style addressing")
	assert.Equal(t, "PAYLOAD", gotBody)
	assert.NotEmpty(t, gotAuth, "SigV4 authorization header present")
}

func TestR2StoreServeRedirectsToPresignedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, err := s3store.NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", "mybucket", srv.URL)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, "orgs/o1/k1.bin", "f.bin", ""))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, srv.URL+"/mybucket/orgs/o1/k1.bin", "presigned URL points at the bucket object")
	assert.Contains(t, loc, "X-Amz-Signature=", "URL carries a signature")
	assert.Contains(t, loc, "X-Amz-Expires=900", "15 minute lifetime")
}
