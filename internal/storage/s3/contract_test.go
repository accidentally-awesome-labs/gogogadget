package s3

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/storage"
	storagecontract "github.com/gogogadget/gogogadget/internal/storage/contract"
	"io"
	"io/fs"
	"net/http"
	"testing"
)

func TestS3StoreContract(t *testing.T) {
	var _ storage.Store = (*R2Store)(nil)
	storagecontract.Run(t, func() storage.Store { return &contractStore{objects: map[string][]byte{}} })
}

type contractStore struct{ objects map[string][]byte }

func (s *contractStore) Put(ctx context.Context, key, _ string, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	s.objects[key] = b
	return int64(len(b)), nil
}
func (s *contractStore) Serve(ctx context.Context, w http.ResponseWriter, key, filename, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, ok := s.objects[key]
	if !ok {
		return fs.ErrNotExist
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Type", contentType)
	_, err := w.Write(b)
	return err
}
func (s *contractStore) ServeInline(ctx context.Context, w http.ResponseWriter, key, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, ok := s.objects[key]
	if !ok {
		return fs.ErrNotExist
	}
	w.Header().Set("Content-Type", contentType)
	_, err := w.Write(b)
	return err
}
func (s *contractStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s.objects, key)
	return nil
}
