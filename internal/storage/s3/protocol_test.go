package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestR2StoreHealthReportsProviderSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		ok   bool
	}{{"success", http.StatusOK, true}, {"failure", http.StatusUnauthorized, false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodHead, r.Method)
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			store, err := NewR2Store(context.Background(), "acct", "key", "secret", "bucket", srv.URL)
			require.NoError(t, err)
			err = store.Health(context.Background())
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type deterministicS3Backend struct {
	server  *httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
	bucket  string
}

func newDeterministicS3Backend(bucket string) *deterministicS3Backend {
	backend := &deterministicS3Backend{objects: map[string][]byte{}, bucket: bucket}
	backend.server = httptest.NewServer(http.HandlerFunc(backend.handle))
	return backend
}

func (b *deterministicS3Backend) Close()      { b.server.Close() }
func (b *deterministicS3Backend) URL() string { return b.server.URL }

func (b *deterministicS3Backend) handle(w http.ResponseWriter, r *http.Request) {
	prefix := "/" + b.bucket + "/"
	if r.URL.Path == "/"+b.bucket && r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, prefix)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		body, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case http.MethodDelete:
		delete(b.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
