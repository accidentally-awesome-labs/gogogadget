package web

import (
	"encoding/json"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

// GET /admin/content/new?kind=… — an empty editor for one type.
func (s *Server) handleAdminContentNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := s.types.Get(r.URL.Query().Get("kind"))
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	media, err := s.recentMedia(r)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.content.title"), Layout: templates.LayoutAdmin},
		templates.AdminContentEditorPage(templates.ContentEditorData{
			Type: t, Locales: contentLocales(), Meta: map[string]string{},
			Status: "draft", Media: media,
		}))
}

// GET /admin/content/{id} — the editor for an existing entry.
func (s *Server) handleAdminContentEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entry, t, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	revisions, err := s.q.ListRevisionsByEntry(ctx, sqlc.ListRevisionsByEntryParams{
		EntryID: entry.ID, Lim: contentRevisionLimit,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	media, err := s.recentMedia(r)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: entry.Title, Layout: templates.LayoutAdmin},
		templates.AdminContentEditorPage(editorDataFrom(entry, t, revisions, media)))
}

// formatAdminTime renders a column value back into the input's wire format.
func formatAdminTime(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(datetimeLocal)
}

func editorDataFrom(entry sqlc.ContentEntry, t content.Type, revisions []sqlc.ContentRevision, media []sqlc.ContentMedium) templates.ContentEditorData {
	meta := map[string]string{}
	if len(entry.Meta) > 0 {
		_ = json.Unmarshal(entry.Meta, &meta)
	}
	return templates.ContentEditorData{
		ID: entry.ID, Type: t, Locales: contentLocales(),
		Title: entry.Title, Slug: entry.Slug, Locale: entry.Locale,
		Summary: entry.Summary, BodyMd: entry.BodyMd, Meta: meta,
		PublishedAt: formatAdminTime(entry.PublishedAt),
		UnpublishAt: formatAdminTime(entry.UnpublishAt),
		Status:      entry.Status, PreviewHTML: entry.BodyHtml,
		Revisions: revisions, Media: media,
		Errs: templates.ContentErrors{Meta: map[string]string{}},
	}
}
