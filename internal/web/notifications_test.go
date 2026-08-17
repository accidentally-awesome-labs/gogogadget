package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func notifySeed(t *testing.T, s *Server, orgID, userID string) (unreadID int64) {
	t.Helper()
	n1, err := s.q.InsertNotification(t.Context(), sqlc.InsertNotificationParams{
		ClerkOrgID: orgID, ClerkUserID: userID, Kind: "welcome", Title: "Welcome", Body: "body", Url: "/app",
	})
	require.NoError(t, err)
	_, err = s.q.InsertNotification(t.Context(), sqlc.InsertNotificationParams{
		ClerkOrgID: orgID, ClerkUserID: userID, Kind: "system", Title: "Old news", Body: "", Url: "",
	})
	require.NoError(t, err)
	// Third row already read.
	n3, err := s.q.InsertNotification(t.Context(), sqlc.InsertNotificationParams{
		ClerkOrgID: orgID, ClerkUserID: userID, Kind: "system", Title: "Read one", Body: "", Url: "",
	})
	require.NoError(t, err)
	require.NoError(t, s.q.MarkNotificationRead(t.Context(), sqlc.MarkNotificationReadParams{ID: n3.ID, ClerkOrgID: orgID, ClerkUserID: userID}))
	return n1.ID
}

func TestNotificationsPageBadgeRead(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_nt", "org_nt", "org:admin")
	cookie := sessionCookie("user_nt", "org_nt", "org:admin")
	id := notifySeed(t, s, "org_nt", "user_nt")

	// Page lists unread (bold) + read rows.
	code, _, body := serve(t, s, "GET", "/app/notifications", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Welcome")
	assert.Contains(t, body, `data-testid="notification-row"`)
	assert.Contains(t, body, `data-testid="notifications-read-all"`)

	// Badge: 2 unread.
	code, _, body = serve(t, s, "GET", "/app/notifications/badge", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, ">2</span>", "badge shows 2 unread")

	// Mark one read → badge drops to 1.
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("HX-Request", "true")
	code, _, _ = serve(t, s, "POST", "/app/notifications/"+strconv.FormatInt(id, 10)+"/read", nil, h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)

	code, _, body = serve(t, s, "GET", "/app/notifications/badge", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, ">1</span>")

	// Read-all → badge hidden (0 → no bubble).
	code, _, _ = serve(t, s, "POST", "/app/notifications/read-all", nil, h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)
	code, _, body = serve(t, s, "GET", "/app/notifications/badge", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "rounded-full", "zero unread → no bubble")
}

func TestNotificationReadIsUserScoped(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_na", "org_na", "org:admin")
	seedMembership(t, s, "user_nb", "org_na", "org:admin")
	id := notifySeed(t, s, "org_na", "user_na")

	// user_b cannot mark user_a's notification read (user-scoped WHERE).
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/app/notifications/"+strconv.FormatInt(id, 10)+"/read", nil, h,
		append(csrfCookies, sessionCookie("user_nb", "org_na", "org:admin"))...)
	require.Equal(t, http.StatusOK, code)

	n, err := s.q.CountUnreadByUser(t.Context(), sqlc.CountUnreadByUserParams{ClerkOrgID: "org_na", ClerkUserID: "user_na"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "user_a's unread untouched")
}

// TestNotificationsStreamEmitsInitialEvent reads exactly one event from the
// SSE handler over a REAL httptest server (streaming needs a live conn).
func TestNotificationsStreamEmitsInitialEvent(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_sse", "org_sse", "org:admin")
	notifySeed(t, s, "org_sse", "user_sse")

	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/app/notifications/stream", nil)
	require.NoError(t, err)
	req.AddCookie(sessionCookie("user_sse", "org_sse", "org:admin"))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))

	// First data: line within 3s carries the initial unread count.
	var event string
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				event = line
				cancel() // close the stream
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE event within 3s")
	}
	assert.Contains(t, event, `"unread":2`, "initial event carries the unread count")
}
