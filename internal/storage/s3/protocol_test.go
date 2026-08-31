package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestR2StorePutUsesPathStyleAndSigV4(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, err := NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", "mybucket", srv.URL)
	require.NoError(t, err)
	n, err := s.Put(context.Background(), "orgs/o1/k1.bin", "application/octet-stream", strings.NewReader("PAYLOAD"))
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	assert.Equal(t, "/mybucket/orgs/o1/k1.bin", gotPath)
	assert.Equal(t, "PAYLOAD", gotBody)
	assert.NotEmpty(t, gotAuth)
}

func TestR2StoreServeRedirectsToPresignedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	s, err := NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", "mybucket", srv.URL)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, "orgs/o1/k1.bin", "f.bin", ""))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, srv.URL+"/mybucket/orgs/o1/k1.bin")
	assert.Contains(t, loc, "X-Amz-Signature=")
	assert.Contains(t, loc, "X-Amz-Expires=900")
}
