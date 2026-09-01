package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCapturer struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeCapturer) Capture(userID, event string, _ map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, userID+":"+event)
}

func (f *fakeCapturer) contains(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e == want {
			return true
		}
	}
	return false
}

func TestProjectCreatedCaptured(t *testing.T) {
	fake := &fakeCapturer{}
	s := integrationServer(t, func(d *Deps) { d.Analytics = fake })
	seedMembership(t, s, "user_ph", "org_ph", "org:admin")
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE org_id='org_ph'") })

	code, _, _ := postForm(t, s, "/app/projects", url.Values{"name": {"Tracked"}}, sessionCookie("user_ph", "org_ph", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.True(t, fake.contains("user_ph:project_created"), "project_created must be captured, got %v", fake.events)
}

func TestIngestProxiedThroughFullStack(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		assert.Equal(t, "/e/", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := integrationServer(t, func(d *Deps) {
		d.Config.PostHogAPIKey = "phc_test"
		d.Config.PostHogHost = upstream.URL
	})

	// Through the FULL middleware stack: not 403 (nosurf exempt), not 429
	// (rate-limit exempt).
	code, _, body := serve(t, s, "POST", "/ingest/e/", []byte(`{"event":"test"}`), nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, upstreamHits)
	assert.NotContains(t, body, "Forbidden")
}
