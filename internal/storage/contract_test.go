package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assert.Equal(t, payload, got)
	})
	t.Run("ServeDeliveryContract", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "orgs/org_1/report.txt", "text/plain", bytes.NewReader(payload))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		require.NoError(t, s.Serve(ctx, rec, "orgs/org_1/report.txt", "report.txt", "text/plain"))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	})
	t.Run("ServeInlineDeliveryContract", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "content/deadbeef.png", "image/png", bytes.NewReader([]byte("png")))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		require.NoError(t, s.ServeInline(ctx, rec, "content/deadbeef.png", "image/png"))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), "inline")
	})
	t.Run("DeleteRemovesObject", func(t *testing.T) {
		s := factory(t)
		_, err := s.Put(ctx, "doomed.bin", "application/octet-stream", bytes.NewReader(payload))
		require.NoError(t, err)
		require.NoError(t, s.Delete(ctx, "doomed.bin"))
		_, err = fetchObject(t, s, "doomed.bin", "x", "")
		require.Error(t, err)
	})
}

func fetchObject(t *testing.T, s Store, key, filename, contentType string) ([]byte, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.Serve(context.Background(), rec, key, filename, contentType); err != nil {
		return nil, err
	}
	if rec.Code != http.StatusOK {
		return nil, io.EOF
	}
	return rec.Body.Bytes(), nil
}

type contractStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newContractStore() *contractStore { return &contractStore{objects: map[string][]byte{}} }
func (s *contractStore) Put(_ context.Context, key, _ string, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.objects[key] = b
	s.mu.Unlock()
	return int64(len(b)), nil
}
func (s *contractStore) Serve(_ context.Context, w http.ResponseWriter, key, filename, contentType string) error {
	s.mu.Lock()
	b, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		return os.ErrNotExist
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, err := w.Write(b)
	return err
}
func (s *contractStore) ServeInline(_ context.Context, w http.ResponseWriter, key, contentType string) error {
	s.mu.Lock()
	b, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		return os.ErrNotExist
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	_, err := w.Write(b)
	return err
}
func (s *contractStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}
func TestStoreContract(t *testing.T) {
	runStoreContract(t, func(*testing.T) Store { return newContractStore() })
}
