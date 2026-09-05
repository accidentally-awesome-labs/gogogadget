package contract

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	// ReadServed returns the object bytes a Serve/ServeInline response
	// delivers to the client. A streaming adapter writes them into the
	// recorder; a redirecting adapter (S3 presigned GET) answers 303 and the
	// bytes are behind Location, so the adapter says how to reach them.
	ReadServed func(*testing.T, *httptest.ResponseRecorder) []byte
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
	// Durability: a Put that reported a byte count must be readable back,
	// byte for byte. Nothing asserted this, and a store that reports a
	// successful write for bytes that never landed passes every other step
	// in this sequence — which is exactly how a lost export object reached
	// a 500 on the download instead of an error at the write.
	require.Equal(t, payload, readServed(t, options, rec), "Serve must yield the bytes Put reported")
	rec = httptest.NewRecorder()
	require.NoError(t, s.ServeInline(context.Background(), rec, key, "application/octet-stream"))
	if options.InlineStatus != nil {
		options.InlineStatus(t, rec.Code)
	} else {
		require.Contains(t, []int{http.StatusOK, http.StatusSeeOther}, rec.Code)
	}
	require.Equal(t, payload, readServed(t, options, rec), "ServeInline must yield the bytes Put reported")

	// The same contract read from the failing side: a Put that could not
	// consume its whole source reports the error and NO byte count, and
	// leaves nothing servable at the key. A count returned beside a failed
	// write is the defect above with the sign flipped.
	failing := "orgs/contract/truncated.bin"
	short, err := s.Put(context.Background(), failing, "application/octet-stream",
		io.MultiReader(bytes.NewReader(payload), errReader{}))
	require.Error(t, err, "Put must report a source that failed mid-stream")
	require.Zero(t, short, "a failed Put must report no byte count")
	assertMissing(t, options, s, failing)

	require.NoError(t, s.Delete(context.Background(), key))
	assertMissing(t, options, s, key)
}

func readServed(t *testing.T, options Options, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	if options.ReadServed != nil {
		return options.ReadServed(t, rec)
	}
	return rec.Body.Bytes()
}

func assertMissing(t *testing.T, options Options, s storage.Store, key string) {
	t.Helper()
	if options.AssertMissing != nil {
		options.AssertMissing(t, s, key)
		return
	}
	rec := httptest.NewRecorder()
	require.Error(t, s.Serve(context.Background(), rec, key, "object.bin", ""))
}

// errReader fails on the first read, standing in for a source that dies
// part-way through an upload.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("contract: source failed") }
