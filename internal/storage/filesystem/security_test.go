package filesystem

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevStoreRoundTripAndHeaders(t *testing.T) {
	s := NewDevStore(t.TempDir())
	n, err := s.Put(context.Background(), "orgs/org_1/abc.txt", "text/plain", strings.NewReader("hello bytes"))
	require.NoError(t, err)
	assert.Equal(t, int64(11), n)
	rec := httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, "orgs/org_1/abc.txt", "report.txt", "text/plain"))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `attachment; filename="report.txt"`, rec.Header().Get("Content-Disposition"))
	assert.Equal(t, "hello bytes", rec.Body.String())
	require.NoError(t, s.Delete(context.Background(), "orgs/org_1/abc.txt"))
	require.Error(t, s.Serve(context.Background(), httptest.NewRecorder(), "orgs/org_1/abc.txt", "x", "text/plain"))
}

func TestDevStoreContainsTraversalAndSanitizesFilename(t *testing.T) {
	root := t.TempDir()
	s := NewDevStore(root)
	_, err := s.Put(context.Background(), "../../etc/passwd", "text/plain", strings.NewReader("x"))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "etc", "passwd"))
	rec := httptest.NewRecorder()
	_ = s.Serve(context.Background(), rec, "orgs/../../../../etc/passwd", "x", "")
	assert.NotContains(t, rec.Header().Get("Content-Disposition"), "etc")
	_, err = s.Put(context.Background(), "k", "text/plain", strings.NewReader("x"))
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	require.NoError(t, s.Serve(context.Background(), rec, "k", `evil"; }.txt`, "text/plain"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.NotContains(t, rec.Header().Get("Content-Disposition"), `"evil"`)
}
