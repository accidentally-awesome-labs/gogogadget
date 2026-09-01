package web

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// contentImageTypes is the inline-serving allowlist. SVG is excluded
// deliberately: it can carry script, and /media serves same-origin.
var contentImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// POST /admin/media — image upload.
//
// The stored content type is SNIFFED from the bytes, never taken from the
// client's part header: this is the one surface served inline, so a lie about
// the type would be a lie about what the browser executes.
func (s *Server) handleAdminMediaUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.renderMediaError(w, r, i18n.T(ctx, "admin.media.invalid_missing"))
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		s.renderMediaError(w, r, i18n.T(ctx, "admin.media.invalid_missing"))
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		s.renderError(w, r, err.Error())
		return
	}
	head = head[:n]
	sniffed := trimMediaType(http.DetectContentType(head))
	if !contentImageTypes[sniffed] {
		s.renderMediaError(w, r, i18n.T(ctx, "admin.media.invalid_type"))
		return
	}

	key := storage.NewContentKey(header.Filename)
	size, err := s.store.Put(ctx, key, sniffed, io.MultiReader(bytes.NewReader(head), f))
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	m, err := s.q.InsertMedia(ctx, sqlc.InsertMediaParams{
		Filename: header.Filename, ContentType: sniffed, SizeBytes: size,
		StorageKey: key, Alt: "", UploadedBy: actor.UserID,
	})
	if err != nil {
		_ = s.store.Delete(ctx, key) // never orphan the object
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, "", actor.UserID, "content.media_uploaded", map[string]any{
		"id": m.ID, "filename": m.Filename, "content_type": m.ContentType, "size_bytes": m.SizeBytes,
	})
	Toast(w, "success", i18n.T(ctx, "admin.media.uploaded"))
	Navigate(w, r, "/admin/media")
}

// POST /admin/media/{id}/delete
func (s *Server) handleAdminMediaDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	id, ok := parseContentID(w, r)
	if !ok {
		return
	}
	m, err := s.q.GetMedia(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.DeleteMedia(ctx, id); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// Best-effort: an orphaned object is cheaper than a resolvable 500 after
	// the row is already gone.
	_ = s.store.Delete(ctx, m.StorageKey)
	audit.Log(ctx, s.q, "", actor.UserID, "content.media_deleted", map[string]any{"id": id})
	Toast(w, "success", i18n.T(ctx, "admin.media.deleted"))
	Navigate(w, r, "/admin/media")
}

// renderMediaError re-renders the upload card with 422 so the rejection lands
// beside the file input and the library below it is left alone.
func (s *Server) renderMediaError(w http.ResponseWriter, r *http.Request, msg string) {
	d, err := s.mediaData(r, 1, msg)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(r.Context(), "admin.media.title"), Layout: templates.LayoutAdmin},
		templates.MediaUploadCard(d))
}

// trimMediaType drops the "; charset=…" suffix DetectContentType adds.
func trimMediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		return ct[:i]
	}
	return ct
}
