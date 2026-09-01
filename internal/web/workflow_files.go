package web

import (
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// renderFilesError re-renders the table fragment with 422 (quota hits land
// here — same shape as the project limit pattern).
func (s *Server) renderFilesError(w http.ResponseWriter, r *http.Request, d templates.FilesData) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: "Files", Layout: templates.LayoutApp}, templates.FilesTable(d))
}

// POST /app/files — multipart upload. The global 10 MB request cap doubles as
// the per-file cap; the plan quota (MaxStorageMB) is enforced against the org
// total.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	plan := identity.PlanFrom(ctx)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	defer f.Close()

	// Quota: existing bytes + incoming size vs plan cap.
	if plan.MaxStorageMB > 0 {
		used, err := s.q.SumBytesByOrg(ctx, org.OrgID)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if used+header.Size > int64(plan.MaxStorageMB)*1024*1024 {
			d, err := s.filesData(r, org.OrgID, 1, true)
			if err != nil {
				s.renderError(w, r, err.Error())
				return
			}
			s.renderFilesError(w, r, d)
			return
		}
	}

	key := storage.NewKey(org.OrgID, header.Filename)
	size, err := s.store.Put(ctx, key, fileContentType(header), f)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	file, err := s.q.InsertFile(ctx, sqlc.InsertFileParams{
		OrgID: org.OrgID, UploaderUserID: user.UserID,
		Filename: header.Filename, ContentType: fileContentType(header),
		SizeBytes: size, StorageKey: key,
	})
	if err != nil {
		_ = s.store.Delete(ctx, key) // never orphan the object
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "file.uploaded", map[string]any{
		"id": file.ID, "filename": file.Filename, "size_bytes": file.SizeBytes,
	})
	Toast(w, "success", "File uploaded")
	Navigate(w, r, "/app/files")
}

func fileContentType(h *multipart.FileHeader) string {
	if ct := h.Header.Get("Content-Type"); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// fileForOrg loads the row or 404s — cross-org ids get 404, never 403 (no
// existence leak). Needs the sqlc row type aliased below.
func (s *Server) fileForOrg(w http.ResponseWriter, r *http.Request, orgID string) (sqlc.File, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return sqlc.File{}, false
	}
	f, err := s.q.GetFileByID(r.Context(), sqlc.GetFileByIDParams{ID: id, OrgID: orgID})
	if err != nil {
		http.NotFound(w, r)
		return sqlc.File{}, false
	}
	return f, true
}

// GET /app/files/{id} — download. Always attachment (never inline): user
// uploads are untrusted bytes; the global nosniff header backs this up.
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	f, ok := s.fileForOrg(w, r, org.OrgID)
	if !ok {
		return
	}
	if err := s.store.Serve(r.Context(), w, f.StorageKey, f.Filename, f.ContentType); err != nil {
		s.renderError(w, r, err.Error())
	}
}

// DELETE /app/files/{id} — row swap: 200 empty, htmx removes the tr.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	f, ok := s.fileForOrg(w, r, org.OrgID)
	if !ok {
		return
	}
	if err := s.q.DeleteFile(ctx, sqlc.DeleteFileParams{ID: f.ID, OrgID: org.OrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// Object cleanup is best-effort: an orphaned object is cheaper than a
	// resolvable 500 after the row is already gone.
	_ = s.store.Delete(ctx, f.StorageKey)
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "file.deleted", map[string]any{"id": f.ID})
	w.WriteHeader(http.StatusOK)
}
