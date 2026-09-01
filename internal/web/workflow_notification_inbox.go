package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/notifications/badge — the unread-bubble fragment (self-swapping
// span; refreshed on load and by SSE events).
func (s *Server) handleNotificationsBadge(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	unread, err := s.q.CountUnreadByUser(r.Context(), sqlc.CountUnreadByUserParams{OrgID: org.OrgID, UserID: user.UserID})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{}, templates.NotificationsBadge(unread))
}

// POST /app/notifications/{id}/read — mark one read, re-render the list.
func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if err := s.q.MarkNotificationRead(ctx, sqlc.MarkNotificationReadParams{ID: id, OrgID: org.OrgID, UserID: user.UserID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d, err := s.notificationsData(r, org.OrgID, user.UserID, 1)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Notifications", Layout: templates.LayoutApp}, templates.NotificationsList(d))
}

// POST /app/notifications/read-all — mark all read, re-render the list, toast.
func (s *Server) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	if err := s.q.MarkAllRead(ctx, sqlc.MarkAllReadParams{OrgID: org.OrgID, UserID: user.UserID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d, err := s.notificationsData(r, org.OrgID, user.UserID, 1)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	Toast(w, "success", "All notifications read")
	s.Render(w, r, Page{Title: "Notifications", Layout: templates.LayoutApp}, templates.NotificationsList(d))
}

// GET /app/notifications/stream — SSE: emits one `notifications` event
// ({"unread":N}) whenever the unread count changes; comment heartbeats every
// 15s keep proxies from idling the connection. Server WriteTimeout is 30s —
// disabled per-response via ResponseController.
//
// The loop polls the DB every 5s; the documented upgrade path (not built) is
// Postgres LISTEN/NOTIFY. The subscriber is the sidebar badge span
// (hx-trigger="load, notifications"), fed by the native EventSource in
// app.js — there is no htmx SSE extension; see /docs/notifications.
func (s *Server) handleNotificationsStream(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	unread := func() int64 {
		n, err := s.q.CountUnreadByUser(ctx, sqlc.CountUnreadByUserParams{OrgID: org.OrgID, UserID: user.UserID})
		if err != nil {
			s.log.Error("sse unread count", "error", err)
			return -1
		}
		return n
	}

	last := int64(-2) // force the initial emit
	poll := time.NewTicker(5 * time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	var events <-chan []byte
	if s.realtime != nil {
		sub, err := s.realtime.Subscribe(ctx, "notifications:"+org.OrgID)
		if err == nil {
			ch := make(chan []byte, 8)
			events = ch
			go func() {
				defer close(ch)
				defer sub.Close()
				for {
					payload, err := sub.Next(ctx)
					if err != nil {
						return
					}
					select {
					case ch <- payload:
					case <-ctx.Done():
						return
					}
				}
			}()
		} else {
			s.log.Error("sse realtime subscribe", "error", err)
		}
	}

	for {
		if n := unread(); n >= 0 && n != last {
			last = n
			if _, err := fmt.Fprintf(w, "event: notifications\ndata: {\"unread\":%d}\n\n", n); err != nil {
				return
			}
			flusher.Flush()
		}
		select {
		case <-ctx.Done():
			return
		case payload, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if _, err := fmt.Fprintf(w, "event: notifications\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
