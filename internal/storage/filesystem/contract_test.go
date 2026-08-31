package filesystem

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/storage"
	storagecontract "github.com/gogogadget/gogogadget/internal/storage/contract"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"testing"
)

func TestFilesystemStoreContract(t *testing.T) {
	storagecontract.Run(t, func() storage.Store { return NewDevStore(t.TempDir()) })
	var _ storage.Store = (*DevStore)(nil)
	s := NewDevStore(t.TempDir())
	n, err := s.Put(context.Background(), "orgs/o/file.txt", "text/plain", io.LimitReader(&zeroReader{}, 5))
	require.NoError(t, err)
	require.Equal(t, int64(5), n)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, "orgs/o/file.txt", "file.txt", "text/plain"))
	require.Equal(t, 200, rec.Code)
	require.NoError(t, s.Delete(context.Background(), "orgs/o/file.txt"))
}

type zeroReader struct{}

func (*zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), io.EOF
}
