package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const mediaPageSize = 20

// GET /admin/media — the platform image library.
func (s *Server) handleAdminMedia(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	d, err := s.mediaData(r, page, "")
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(r.Context(), "admin.media.title"), Layout: templates.LayoutAdmin},
		templates.AdminMediaPage(d))
}

func (s *Server) mediaData(r *http.Request, page int, errMsg string) (templates.MediaData, error) {
	ctx := r.Context()
	total, err := s.q.CountMedia(ctx)
	if err != nil {
		return templates.MediaData{}, err
	}
	totalPages := max(int(math.Ceil(float64(total)/mediaPageSize)), 1)
	items, err := s.q.ListMedia(ctx, sqlc.ListMediaParams{
		Lim: mediaPageSize, Off: int32((page - 1) * mediaPageSize),
	})
	if err != nil {
		return templates.MediaData{}, err
	}
	return templates.MediaData{
		Items: items, Page: page, TotalPages: totalPages, Err: errMsg, Now: s.cfg.Now,
	}, nil
}
