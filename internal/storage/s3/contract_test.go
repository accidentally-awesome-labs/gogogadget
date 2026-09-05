package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogogadget/gogogadget/internal/storage"
	storagecontract "github.com/gogogadget/gogogadget/internal/storage/contract"
	"github.com/stretchr/testify/require"
)

func TestS3StoreContract(t *testing.T) {
	var _ storage.Store = (*R2Store)(nil)
	backend := newDeterministicS3Backend("contract-bucket")
	defer backend.Close()
	store, err := NewR2Store(context.Background(), "acct", "AKIAEXAMPLE", "secret", "contract-bucket", backend.URL())
	require.NoError(t, err)
	storagecontract.RunWithOptions(t, func() storage.Store { return store }, storagecontract.Options{
		ServeStatus: func(t *testing.T, code int) {
			require.Equal(t, http.StatusSeeOther, code)
		},
		InlineStatus: func(t *testing.T, code int) {
			require.Equal(t, http.StatusSeeOther, code)
		},
		// A presigned GET is where the object actually is, so the contract's
		// byte comparison follows the redirect the client would follow.
		ReadServed: func(t *testing.T, rec *httptest.ResponseRecorder) []byte {
			resp, err := http.Get(rec.Header().Get("Location"))
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			return body
		},
		AssertMissing: func(t *testing.T, s storage.Store, key string) {
			rec := httptest.NewRecorder()
			require.NoError(t, s.Serve(context.Background(), rec, key, "object.bin", ""))
			require.Equal(t, http.StatusSeeOther, rec.Code)
			resp, err := http.Get(rec.Header().Get("Location"))
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusNotFound, resp.StatusCode)
		},
	})
}
