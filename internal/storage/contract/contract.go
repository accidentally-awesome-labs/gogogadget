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

// Run exercises the adapter-agnostic Store contract.
func Run(t *testing.T, factory func() storage.Store) {
	t.Helper()
	s := factory()
	key := "orgs/contract/object.bin"
	payload := []byte("contract payload")
	n, err := s.Put(context.Background(), key, "application/octet-stream", bytes.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), n)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, key, "object.bin", "application/octet-stream"))
	require.Contains(t, []int{http.StatusOK, http.StatusSeeOther}, rec.Code)
	require.NoError(t, s.Delete(context.Background(), key))
	rec = httptest.NewRecorder()
	require.Error(t, s.Serve(context.Background(), rec, key, "object.bin", ""))
}
