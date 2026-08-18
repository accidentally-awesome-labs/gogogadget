package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeBucket = "mybucket"

// runStoreContract is the Store seam contract: every implementation must pass
// the same behavioral cases. Impl-specific concerns (path-style addressing,
// SigV4 headers, traversal containment, filename sanitization) stay in
// per-impl tests in storage_test.go.
func runStoreContract(t *testing.T, factory func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()
	payload := []byte("hello bytes \x00\x01\xf4 binary")

	t.Run("PutReturnsSize", func(t *testing.T) {
		s := factory(t)
		n, err := s.Put(ctx, "orgs/org_1/size.bin", "application/octet-stream", bytes.NewReader(payload))
		require.NoError(t, err)
		assert.Equal(t, int64(len(payload)), n)
	})

	t.Run("PutFetchRoundTrip", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "orgs/org_1/doc.txt", "text/plain", bytes.NewReader(payload))
		require.NoError(t, err)
		got, err := fetchObject(t, s, "orgs/org_1/doc.txt", "doc.txt", "text/plain")
		require.NoError(t, err)
		assert.Equal(t, payload, got, "bytes in = bytes out")
	})

	t.Run("ServeDeliveryContract", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "orgs/org_1/report.txt", "text/plain", bytes.NewReader(payload))
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		require.NoError(t, s.Serve(ctx, rec, "orgs/org_1/report.txt", "report.txt", "text/plain"))
		switch rec.Code {
		case http.StatusOK:
			// Direct delivery: user uploads are never rendered inline.
			disp := rec.Header().Get("Content-Disposition")
			assert.Contains(t, disp, "attachment")
			assert.Contains(t, disp, `filename="report.txt"`)
			assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
		case http.StatusSeeOther:
			// Redirect delivery: the target must address the stored object.
			assert.Contains(t, rec.Header().Get("Location"), "orgs/org_1/report.txt")
		default:
			t.Fatalf("Serve: unexpected status %d", rec.Code)
		}
	})

	t.Run("DeleteRemovesObject", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "orgs/org_1/doomed.bin", "application/octet-stream", bytes.NewReader(payload))
		require.NoError(t, err)
		require.NoError(t, s.Delete(ctx, "orgs/org_1/doomed.bin"))
		_, err = fetchObject(t, s, "orgs/org_1/doomed.bin", "doomed.bin", "application/octet-stream")
		require.Error(t, err, "deleted object must not be retrievable")
	})

	t.Run("MissingKeyNotFound", func(t *testing.T) {
		s := factory(t)
		_, err := fetchObject(t, s, "orgs/org_1/never-put.bin", "x.bin", "")
		require.Error(t, err, "missing key must surface as a not-found error")
	})
}

// fetchObject resolves an object's bytes through the Store delivery contract:
// a direct 200 stream, or a 303 redirect whose target is fetched. Any non-200
// final status is an error, unifying not-found across impls (DevStore errors
// inside Serve; R2 presigns offline, so the 404 surfaces at the redirect
// target).
func fetchObject(t *testing.T, s Store, key, filename, contentType string) ([]byte, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.Serve(context.Background(), rec, key, filename, contentType); err != nil {
		return nil, err
	}
	switch rec.Code {
	case http.StatusOK:
		return rec.Body.Bytes(), nil
	case http.StatusSeeOther:
		resp, err := http.Get(rec.Header().Get("Location")) //nolint:bodyclose // closed below
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("redirect target: %s", resp.Status)
		}
		return io.ReadAll(resp.Body)
	default:
		return nil, fmt.Errorf("serve: unexpected status %d", rec.Code)
	}
}

// --- stateful fake S3 endpoint ---

type fakeObject struct {
	body        []byte
	contentType string
}

type fakeS3Request struct {
	method, path, auth string
}

// fakeS3 is an in-memory S3 endpoint: PUT stores, GET serves or
// NoSuchKey-404s, DELETE removes. Auth headers are recorded, not verified.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string]fakeObject
	last    fakeS3Request
}

func newFakeS3(t *testing.T) (*httptest.Server, *fakeS3) {
	t.Helper()
	f := &fakeS3{objects: make(map[string]fakeObject)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.last = fakeS3Request{r.Method, r.URL.Path, r.Header.Get("Authorization")}
		key := strings.TrimPrefix(r.URL.Path, "/"+fakeBucket+"/")
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			f.objects[key] = fakeObject{body: body, contentType: r.Header.Get("Content-Type")}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `<?xml version="1.0"?><PutObjectOutput></PutObjectOutput>`)
		case http.MethodGet:
			obj, ok := f.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`)
				return
			}
			w.Header().Set("Content-Type", obj.contentType)
			_, _ = w.Write(obj.body)
		case http.MethodDelete:
			delete(f.objects, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, f
}

func (f *fakeS3) lastRequest() fakeS3Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *fakeS3) object(key string) (fakeObject, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	return o, ok
}

func TestDevStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		return NewDevStore(t.TempDir())
	})
}

func TestR2StoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		srv, _ := newFakeS3(t)
		s, err := NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", fakeBucket, srv.URL)
		require.NoError(t, err)
		return s
	})
}
