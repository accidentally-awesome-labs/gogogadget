package modkit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func testArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:       "pax_global_header",
		Typeflag:   tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": testCommitA},
	}); err != nil {
		t.Fatalf("tar global PAX header: %v", err)
	}
	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := io.WriteString(tarWriter, content); err != nil {
			t.Fatalf("tar content %s: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return compressed.Bytes()
}
func signedGitHubArchive(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		writeTestFile(t, root, name, []byte(data))
	}
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	if _, err := WriteSignedRegistrySnapshot(root, private); err != nil {
		t.Fatalf("WriteSignedRegistrySnapshot: %v", err)
	}
	published := map[string]string{}
	if err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || name == "." {
			return nil
		}
		data, err := fs.ReadFile(os.DirFS(root), name)
		if err != nil {
			return err
		}
		published[filepath.ToSlash(filepath.Join(prefix, name))] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return testArchive(t, published)
}

func TestDirectorySourceResolvesContentAddressedSnapshot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[]}`))
	writeTestFile(t, root, "registry/file.txt", []byte("alpha"))

	source := DirectorySource{Root: root}
	first, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(first): %v", err)
	}
	if len(first.Commit) != 64 {
		t.Fatalf("commit length = %d, want 64", len(first.Commit))
	}
	got, err := fs.ReadFile(first.FS, "registry/file.txt")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != "alpha" {
		t.Fatalf("snapshot content = %q, want alpha", got)
	}

	writeTestFile(t, root, "registry/file.txt", []byte("mutated"))
	if _, err := fs.ReadFile(first.FS, "registry/file.txt"); err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("ReadFile after mutation error = %v, want content-changed rejection", err)
	}
	file, err := first.FS.Open("registry/file.txt")
	if err == nil {
		_, err = io.ReadAll(file)
		err = errors.Join(err, file.Close())
	}
	if err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("Open/read after mutation error = %v, want content-changed rejection", err)
	}
	writeTestFile(t, root, "registry/file.txt", []byte("alpha"))
	writeTestFile(t, root, "tmp/ignored.txt", []byte("ignored"))

	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "registry/file.txt"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	second, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(second): %v", err)
	}
	if second.Commit != first.Commit {
		t.Fatalf("mtime changed commit: %s != %s", second.Commit, first.Commit)
	}

	writeTestFile(t, root, "registry/file.txt", []byte("beta"))
	third, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(third): %v", err)
	}
	if third.Commit == first.Commit {
		t.Fatal("content change did not change commit")
	}

	if err := os.Symlink(filepath.Join(root, "registry/file.txt"), filepath.Join(root, "unsafe-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := source.Resolve(context.Background(), ProjectRegistry{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Resolve(symlink) error = %v, want symlink rejection", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Resolve(ctx, ProjectRegistry{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve(cancelled) error = %v, want context cancellation", err)
	}
}

func TestGitHubSourceResolvesAndCachesArchive(t *testing.T) {
	archive := signedGitHubArchive(t, "widgets-"+testCommitA, map[string]string{
		"registry.json": `{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[]}`,
		"registry/file": "payload",
	})
	registry := ProjectRegistry{Source: "github", Namespace: "ggg", PublicKey: "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik=", Repository: "acme/widgets", Ref: "main"}
	var apiRequests atomic.Int32
	var archiveRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/commits/main":
			apiRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": testCommitA})
		case "/acme/widgets/tar.gz/" + testCommitA:
			archiveRequests.Add(1)
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cache := t.TempDir()
	source := GitHubSource{
		Client:          server.Client(),
		CacheDir:        cache,
		APIBaseURL:      server.URL,
		CodeloadBaseURL: server.URL,
	}
	first, err := source.Resolve(context.Background(), registry)
	if err != nil {
		t.Fatalf("Resolve(first): %v", err)
	}
	second, err := source.Resolve(context.Background(), registry)
	if err != nil {
		t.Fatalf("Resolve(second): %v", err)
	}
	if first.Commit != testCommitA || second.Commit != testCommitA {
		t.Fatalf("commits = %q, %q, want %q", first.Commit, second.Commit, testCommitA)
	}
	if got := archiveRequests.Load(); got != 1 {
		t.Fatalf("archive requests = %d, want 1", got)
	}
	if got := apiRequests.Load(); got != 2 {
		t.Fatalf("API requests = %d, want 2 ref resolutions", got)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != first.SnapshotSHA256 {
		t.Fatalf("cache identity = %#v, want snapshot digest %q", entries, first.SnapshotSHA256)
	}
	payload, err := fs.ReadFile(second.FS, "registry/file")
	if err != nil {
		t.Fatalf("read cached payload: %v", err)
	}
	if string(payload) != "payload" {
		t.Fatalf("cached payload = %q", payload)
	}

	offline := GitHubSource{CacheDir: cache, Offline: true}
	offlineRegistry := registry
	offlineRegistry.Ref = testCommitA
	cached, err := offline.Resolve(context.Background(), offlineRegistry)
	if err != nil {
		t.Fatalf("Resolve(offline): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, first.SnapshotSHA256, "tree", "registry", "file"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeCorrupt, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := offline.Resolve(context.Background(), offlineRegistry); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Resolve(corrupt offline) error = %v, want digest refusal", err)
	}
	afterCorrupt, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterCorrupt) != len(beforeCorrupt) || afterCorrupt[0].Name() != beforeCorrupt[0].Name() {
		t.Fatalf("offline corruption changed cache entries: before=%v after=%v", beforeCorrupt, afterCorrupt)
	}
	if cached.Commit != testCommitA {
		t.Fatalf("offline commit = %q, want %q", cached.Commit, testCommitA)
	}
}

func TestGitHubSourceRejectsUnsafeArchive(t *testing.T) {
	archive := testArchive(t, map[string]string{
		"widgets-" + testCommitA + "/../../escape": "nope",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": testCommitA})
		case strings.HasPrefix(r.URL.Path, "/acme/widgets/tar.gz/"):
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := GitHubSource{
		Client: server.Client(), CacheDir: t.TempDir(),
		APIBaseURL: server.URL, CodeloadBaseURL: server.URL,
	}
	_, err := source.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/widgets", Ref: "main"})
	if err == nil || !strings.Contains(err.Error(), "archive path") {
		t.Fatalf("Resolve error = %v, want unsafe archive path rejection", err)
	}
}

func TestGitHubSourceRejectsInvalidResponsesAndOfflineMisses(t *testing.T) {
	t.Run("invalid commit sha", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "NOTAHEX"})
		}))
		defer server.Close()
		source := GitHubSource{Client: server.Client(), CacheDir: t.TempDir(), APIBaseURL: server.URL, CodeloadBaseURL: server.URL}
		_, err := source.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/widgets", Ref: "main"})
		if err == nil || !strings.Contains(err.Error(), "lowercase commit") {
			t.Fatalf("Resolve error = %v, want invalid commit rejection", err)
		}
	})

	t.Run("missing registry root", func(t *testing.T) {
		archive := testArchive(t, map[string]string{
			"widgets-" + testCommitA + "/README.txt": "no registry",
		})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/repos/") {
				_ = json.NewEncoder(w).Encode(map[string]string{"sha": testCommitA})
				return
			}
			_, _ = w.Write(archive)
		}))
		defer server.Close()
		source := GitHubSource{Client: server.Client(), CacheDir: t.TempDir(), APIBaseURL: server.URL, CodeloadBaseURL: server.URL}
		_, err := source.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/widgets", Ref: "main"})
		if err == nil || !strings.Contains(err.Error(), "registry.json") {
			t.Fatalf("Resolve error = %v, want missing registry.json rejection", err)
		}
	})

	t.Run("offline requires full cached commit", func(t *testing.T) {
		source := GitHubSource{CacheDir: t.TempDir(), Offline: true}
		if _, err := source.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/widgets", Ref: "main"}); err == nil || !strings.Contains(err.Error(), "full 40") {
			t.Fatalf("Resolve(symbolic offline) error = %v, want full commit rejection", err)
		}
		if _, err := source.Resolve(context.Background(), ProjectRegistry{Source: "github", Repository: "acme/widgets", Ref: testCommitA}); err == nil || !strings.Contains(err.Error(), "cache") {
			t.Fatalf("Resolve(missing offline cache) error = %v, want cache miss", err)
		}
	})
}

// A self-hosting registry resolves from the same tree it installs into, so the
// snapshot commit must depend only on what the registry can distribute. If
// project state or tool-owned output moved the commit, writing the lock would
// invalidate the lock that was just written and `sync --check` could never be
// clean.
func TestDirectorySourceCommitIgnoresProjectStateAndGeneratedOutput(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[]}`))
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2}`))
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n"))

	source := DirectorySource{Root: root}
	before, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(before): %v", err)
	}

	// Everything below is project state or tool-owned output.
	writeTestFile(t, root, "gogogadget.json", []byte(`{"schema":2}`))
	writeTestFile(t, root, "gogogadget.lock.json", []byte(`{"schema":2}`))
	writeTestFile(t, root, "internal/modules/bootstrap_registry_gen.go", []byte("package modules\n"))
	writeTestFile(t, root, "internal/web/templates/page_templ.go", []byte("package templates\n"))
	writeTestFile(t, root, "static/app.css", []byte(".a{}\n"))

	after, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(after): %v", err)
	}
	if before.Commit != after.Commit {
		t.Fatalf("commit moved on project state/generated output: %s -> %s", before.Commit, after.Commit)
	}

	// A real module payload change must still move the commit.
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst V = 2\n"))
	changed, err := source.Resolve(context.Background(), ProjectRegistry{})
	if err != nil {
		t.Fatalf("Resolve(changed): %v", err)
	}
	if changed.Commit == after.Commit {
		t.Fatal("commit did not move when a module payload changed")
	}
}
