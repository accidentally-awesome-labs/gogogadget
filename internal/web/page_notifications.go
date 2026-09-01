package web

import (
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const notificationsPageSize = 20

func (s *Server) notificationsData(r *http.Request, orgID, userID string, page int) (templates.NotificationsData, error) {
	ctx := r.Context()
	total, err := s.q.CountNotificationsByUser(ctx, sqlc.CountNotificationsByUserParams{OrgID: orgID, UserID: userID})
	if err != nil {
		return templates.NotificationsData{}, err
	}
	totalPages := max(int((total+notificationsPageSize-1)/notificationsPageSize), 1)
	items, err := s.q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
		OrgID: orgID, UserID: userID,
		Limit: notificationsPageSize, Offset: int32((page - 1) * notificationsPageSize),
	})
	if err != nil {
		return templates.NotificationsData{}, err
	}
	unread, err := s.q.CountUnreadByUser(ctx, sqlc.CountUnreadByUserParams{OrgID: orgID, UserID: userID})
	if err != nil {
		return templates.NotificationsData{}, err
	}
	return templates.NotificationsData{Items: items, Page: page, TotalPages: totalPages, Unread: unread}, nil
}

// GET /app/notifications — the list page.
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	d, err := s.notificationsData(r, org.OrgID, user.UserID, page)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// The pager swaps innerMorph into #notif-list, so the fragment is the box's
	// CONTENTS. NotificationsList repeats the wrapper because read and read-all
	// target it with outerHTML; returning that here would nest a duplicate id.
	if wantsFragment(r) {
		s.Render(w, r, Page{Title: "Notifications", Layout: templates.LayoutApp}, templates.NotificationsListBody(d))
		return
	}
	s.Render(w, r, Page{Title: "Notifications", Layout: templates.LayoutApp}, templates.NotificationsPage(d))
}
