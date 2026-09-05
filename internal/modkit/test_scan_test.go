package modkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recorderTest(body string) ([]Manifest, map[string][]byte) {
	const target = "internal/web/realtime_test.go"
	return []Manifest{{ID: "ggg/system/realtime", Kind: ModuleSystem, Name: "realtime",
			Files: []ManifestFile{{Source: target, Target: target, Class: FileClassTest}}}},
		map[string][]byte{target: []byte(body)}
}

// The incident, reproduced as a unit: the SSE test handed httptest's recorder
// to the handler goroutine and then read its body. -race caught it; nothing
// else did.
func TestRecorderHandoffToAGoroutineIsRefused(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	"net/http/httptest"
	"testing"
)

func TestServeRealtimePublishesSSE(t *testing.T) {
	req := httptest.NewRequest("GET", "/events/org-1", nil)
	rec := httptest.NewRecorder()
	s := &Server{}
	done := make(chan struct{})
	go func() {
		s.serveRealtime(rec, req)
		close(done)
	}()
	<-done
	if rec.Body.String() == "" {
		t.Fatal("no body")
	}
}
`)

	err := ValidateNoRecorderGoroutineHandoff(modules, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realtime_test.go:13")
	assert.Contains(t, err.Error(), "ggg/system/realtime")
	assert.Contains(t, err.Error(), "TestServeRealtimePublishesSSE")
	assert.Contains(t, err.Error(), `"rec"`)
	assert.Contains(t, err.Error(), "one mutex")
}

// The recorder passed straight to a named call is the same handoff; the rule
// is the crossing, not the closure.
func TestRecorderHandoffRefusesADirectGoCall(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	"net/http/httptest"
	"testing"
)

func TestStream(t *testing.T) {
	w := httptest.NewRecorder()
	go serve(w, nil)
	_ = w.Code
}
`)

	require.Error(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}

// A composite literal is the same type by another spelling.
func TestRecorderHandoffRefusesACompositeLiteral(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestStream(t *testing.T) {
	rec := &httptest.ResponseRecorder{Body: &bytes.Buffer{}}
	go serve(rec, nil)
	_ = rec.Code
}
`)

	require.Error(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}

// The sanctioned shape: the goroutine gets a recorder that synchronises its
// own writes, and the assertion reads through the same lock. Nothing about the
// goroutine is refused — only the unsynchronised type crossing into it.
func TestSynchronisedRecorderOnAGoroutineIsAllowed(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type syncRecorder struct {
	header http.Header
	mu     sync.Mutex
	body   bytes.Buffer
}

func (r *syncRecorder) Header() http.Header  { return r.header }
func (r *syncRecorder) WriteHeader(int)      {}
func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}
func (r *syncRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestStream(t *testing.T) {
	req := httptest.NewRequest("GET", "/events/org-1", nil)
	rec := &syncRecorder{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		serve(rec, req)
		close(done)
	}()
	<-done
	if rec.String() == "" {
		t.Fatal("no body")
	}
}
`)

	require.NoError(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}

// The overwhelmingly common shape stays legal: a recorder used on the test's
// own goroutine, in a function that also starts an unrelated one. Refusing
// this would refuse most of the suite.
func TestRecorderOnTheTestGoroutineIsAllowed(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	"net/http/httptest"
	"testing"
)

func TestServe(t *testing.T) {
	rec := httptest.NewRecorder()
	ready := make(chan struct{})
	go func() { close(ready) }()
	<-ready
	handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}
`)

	require.NoError(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}

// Product code is not scanned: the rule is about a test observing a server
// goroutine, and a handler that legitimately writes a recorder it was handed
// is not that.
func TestRecorderHandoffIgnoresNonTestPayloads(t *testing.T) {
	const target = "internal/web/server.go"
	modules := []Manifest{{ID: "ggg/system/server", Kind: ModuleSystem, Name: "server",
		Files: []ManifestFile{{Source: target, Target: target, Class: FileClassGo}}}}
	files := map[string][]byte{target: []byte(`package web

import "net/http/httptest"

func probe() {
	rec := httptest.NewRecorder()
	go serve(rec, nil)
}
`)}

	require.NoError(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}

// An aliased or blank import must not be a way around the rule. The whole
// installed tree is checked live instead of here: the scan runs on every plan,
// so `ggg sync --check` is the standing assertion that the tree is clean, and
// a copy of it in a unit test would only restate it from disk.
func TestRecorderHandoffFollowsTheImportAlias(t *testing.T) {
	modules, files := recorderTest(`package web

import (
	ht "net/http/httptest"
	"testing"
)

func TestStream(t *testing.T) {
	rec := ht.NewRecorder()
	go serve(rec, nil)
	_ = rec.Code
}
`)

	require.Error(t, ValidateNoRecorderGoroutineHandoff(modules, files))

	// httptest as a local name for something else is not the guarded type.
	other, otherFiles := recorderTest(`package web

import "testing"

func TestStream(t *testing.T) {
	httptest := newFixture()
	rec := httptest.NewRecorder()
	go serve(rec, nil)
	_ = rec
}
`)

	require.NoError(t, ValidateNoRecorderGoroutineHandoff(other, otherFiles))
}

// A class-"test" payload that is not Go must never reach the Go parser. The
// tree has 235 of them — 200 committed PNG baselines, 34 Playwright specs and
// internal/gggcli/testdata/new-saas.json — so a selector keyed on class alone
// turns every plan into 235 parse errors. The selector is the conjunction:
// declared test AND a Go target.
func TestRecorderHandoffIgnoresNonGoTestPayloads(t *testing.T) {
	files := map[string][]byte{}
	var declared []ManifestFile
	for _, target := range []string{
		"internal/gggcli/testdata/new-saas.json",
		"e2e/admin-content.spec.ts",
		"e2e/visual.spec.ts-snapshots/home-light-desktop-chromium-linux.png",
	} {
		declared = append(declared, ManifestFile{Source: target, Target: target, Class: FileClassTest})
		files[target] = []byte(`{"module":"example","registry":"directory:."}`)
	}
	modules := []Manifest{{ID: "ggg/system/e2e-sweeps", Kind: ModuleSystem, Name: "e2e-sweeps", Files: declared}}

	require.NoError(t, ValidateNoRecorderGoroutineHandoff(modules, files))
}
