package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// The seam's own double runs the seam's own contract. The in-memory store
// this file used to define privately IS that double now (internal/storage/
// mock.go): consumers in other packages needed it, and every one of them was
// reaching for the filesystem ADAPTER instead — a per-environment provider
// selection pinned into test payloads of eight different modules.
func TestStoreContract(t *testing.T) {
	runStoreContract(t, func(*testing.T) Store { return NewMockStore() })
}

// The inventory consumers assert through. "A rejected upload left nothing
// behind" is a claim about the store, and this is the only way to make it
// without knowing an adapter's storage layout.
func TestMockStoreInventory(t *testing.T) {
	ctx := context.Background()
	s := NewMockStore()
	assert.Empty(t, s.Keys())

	_, err := s.Put(ctx, "orgs/org_1/a.txt", "text/plain", bytes.NewReader([]byte("alpha")))
	require.NoError(t, err)
	_, err = s.Put(ctx, "content/b.png", "image/png", bytes.NewReader([]byte("png")))
	require.NoError(t, err)
	assert.Equal(t, []string{"content/b.png", "orgs/org_1/a.txt"}, s.Keys(), "sorted, so assertions are stable")

	body, ok := s.Object("orgs/org_1/a.txt")
	require.True(t, ok)
	assert.Equal(t, []byte("alpha"), body)

	require.NoError(t, s.Delete(ctx, "orgs/org_1/a.txt"))
	_, ok = s.Object("orgs/org_1/a.txt")
	assert.False(t, ok, "a deleted object leaves the inventory")
}

// The injectable failures, which the export jobs need: a write that fails
// must report the error and leave nothing servable at the key.
func TestMockStoreInjectedFailures(t *testing.T) {
	ctx := context.Background()
	s := NewMockStore()
	s.PutErr = os.ErrPermission
	n, err := s.Put(ctx, "doomed.bin", "application/octet-stream", bytes.NewReader([]byte("x")))
	require.ErrorIs(t, err, os.ErrPermission)
	assert.Zero(t, n, "a failed Put reports no byte count")
	assert.Empty(t, s.Keys())

	s.PutErr = nil
	_, err = s.Put(ctx, "kept.bin", "application/octet-stream", bytes.NewReader([]byte("x")))
	require.NoError(t, err)
	s.DeleteErr = os.ErrPermission
	require.ErrorIs(t, s.Delete(ctx, "kept.bin"), os.ErrPermission)
	assert.Equal(t, []string{"kept.bin"}, s.Keys(), "a failed delete keeps the object")
}
