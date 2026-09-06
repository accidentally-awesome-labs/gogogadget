package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
)

// MockStore is the storage seam's own test double: a real, in-memory Store
// that round-trips bytes, so a payload proves an upload landed and a delete
// removed it without naming an adapter package. It ships with the seam for
// the same reason billing.MockClient does — an adapter is a per-environment
// provider selection, and a test payload that constructs one compiles only
// while that selection holds.
//
// It is the store the seam's own contract test already used, promoted out of
// the test file so consumers can reach it, plus the inventory a payload
// needs. Inspect through Keys and Object rather than walking a directory:
// "no object was left behind" is a claim about the store, and a test that
// walks a temp directory can only make it about one implementation.
type MockStore struct {
	// PutErr, when non-nil, fails every write, so a payload can drive the
	// storage-failure path (which must leave no row pointing at nothing).
	PutErr error
	// DeleteErr, when non-nil, fails every delete.
	DeleteErr error

	mu      sync.Mutex
	objects map[string]mockObject
}

type mockObject struct {
	body        []byte
	contentType string
}

func NewMockStore() *MockStore { return &MockStore{objects: map[string]mockObject{}} }

func (s *MockStore) Put(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.PutErr != nil {
		return 0, s.PutErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		// No byte count beside a failed write, and nothing servable at the
		// key: the seam contract holds a store to both.
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = map[string]mockObject{}
	}
	s.objects[key] = mockObject{body: append([]byte(nil), body...), contentType: contentType}
	return int64(len(body)), nil
}

func (s *MockStore) Serve(_ context.Context, w http.ResponseWriter, key, filename, contentType string) error {
	object, ok := s.load(key)
	if !ok {
		return fmt.Errorf("mock store: %q: %w", key, os.ErrNotExist)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(object.body)
	return err
}

func (s *MockStore) ServeInline(_ context.Context, w http.ResponseWriter, key, contentType string) error {
	object, ok := s.load(key)
	if !ok {
		return fmt.Errorf("mock store: %q: %w", key, os.ErrNotExist)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(object.body)
	return err
}

func (s *MockStore) Delete(_ context.Context, key string) error {
	if s.DeleteErr != nil {
		return s.DeleteErr
	}
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

// Object returns the stored bytes for a key.
func (s *MockStore) Object(key string) ([]byte, bool) {
	object, ok := s.load(key)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), object.body...), true
}

// Keys returns every stored key, sorted.
func (s *MockStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for key := range s.objects {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (s *MockStore) load(key string) (mockObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[key]
	return object, ok
}

var _ Store = (*MockStore)(nil)
