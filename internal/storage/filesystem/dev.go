package filesystem

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DevStore is the zero-account Store: objects live on disk under root
// (tmp/uploads in dev), keyed by the same orgs/{org}/{hex} layout. Served
// inline from disk with attachment disposition.
type DevStore struct {
	root string
}

func NewDevStore(root string) *DevStore {
	return &DevStore{root: root}
}

// safePath maps a storage key to a path under root, rejecting traversal.
func (s *DevStore) safePath(key string) (string, error) {
	clean := filepath.Clean("/" + key) // absolute → ".." cannot climb out
	p := filepath.Join(s.root, clean)
	if !strings.HasPrefix(p, filepath.Clean(s.root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("devstore: invalid key %q", key)
	}
	return p, nil
}

func (s *DevStore) Put(_ context.Context, key, _ string, r io.Reader) (int64, error) {
	p, err := s.safePath(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func (s *DevStore) Serve(_ context.Context, w http.ResponseWriter, key, filename, contentType string) error {
	p, err := s.safePath(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizedFilename(filename)+`"`)
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, f)
	return err
}

// ServeInline streams the object for in-page rendering. The disposition is
// inline and carries no filename: this path is reachable only for content
// media whose type was sniffed from the bytes at upload.
func (s *DevStore) ServeInline(_ context.Context, w http.ResponseWriter, key, contentType string) error {
	p, err := s.safePath(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, f)
	return err
}

func (s *DevStore) Delete(_ context.Context, key string) error {
	p, err := s.safePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sanitizedFilename strips control chars and quotes from the download name.
func sanitizedFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		name = "download"
	}
	return name
}
