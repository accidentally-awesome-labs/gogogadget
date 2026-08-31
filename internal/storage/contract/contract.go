package contract

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/stretchr/testify/require"
)

// Options describes provider-specific HTTP semantics while keeping the
// operation sequence shared by every adapter.
type Options struct {
	ServeStatus   func(*testing.T, int)
	InlineStatus  func(*testing.T, int)
	AssertMissing func(*testing.T, storage.Store, string)
}

// Run exercises the filesystem-style Store contract.
func Run(t *testing.T, factory func() storage.Store) {
	RunWithOptions(t, factory, Options{})
}

// RunWithOptions exercises the adapter-agnostic Store contract with explicit
// response/missing-key behavior for redirecting providers such as S3.
func RunWithOptions(t *testing.T, factory func() storage.Store, options Options) {
	t.Helper()
	s := factory()
	key := "orgs/contract/object.bin"
	payload := []byte("contract payload")
	n, err := s.Put(context.Background(), key, "application/octet-stream", bytes.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), n)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, key, "object.bin", "application/octet-stream"))
	if options.ServeStatus != nil {
		options.ServeStatus(t, rec.Code)
	} else {
		require.Contains(t, []int{http.StatusOK, http.StatusSeeOther}, rec.Code)
	}
	rec = httptest.NewRecorder()
	require.NoError(t, s.ServeInline(context.Background(), rec, key, "application/octet-stream"))
	if options.InlineStatus != nil {
		options.InlineStatus(t, rec.Code)
	} else {
		require.Contains(t, []int{http.StatusOK, http.StatusSeeOther}, rec.Code)
	}
	require.NoError(t, s.Delete(context.Background(), key))
	if options.AssertMissing != nil {
		options.AssertMissing(t, s, key)
		return
	}
	rec = httptest.NewRecorder()
	require.Error(t, s.Serve(context.Background(), rec, key, "object.bin", ""))
}
