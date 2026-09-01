package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// contentRevisionLimit is how much history the editor shows. The table
	// keeps every snapshot; this is a page size, not a retention policy.
	contentRevisionLimit = 20
	// contentMediaPicker is how many recent images the editor offers.
	contentMediaPicker = 12
	// datetimeLocal is the wire format of <input type="datetime-local">,
	// interpreted as UTC.
	datetimeLocal = "2006-01-02T15:04"
)

var contentSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// POST /admin/content — create.
func (s *Server) handleAdminContentCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	in, t, meta, ok := s.decodeContentForm(w, r)
	if !ok {
		return
	}
	d := s.editorDataFromInput(0, t, in, meta, "draft", nil, r)
	valid, errs := validContent(ctx, t, in, meta)
	if !valid {
		d.Errs = errs
		s.renderContentFormError(w, r, d)
		return
	}
	html, err := content.Render([]byte(in.BodyMd))
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	entry, err := s.q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: t.Kind, Slug: in.Slug, Locale: in.Locale,
		Title: in.Title, Summary: in.Summary,
		BodyMd: in.BodyMd, BodyHtml: html, Meta: metaJSON,
		Status:      "draft",
		PublishedAt: parseAdminTime(in.PublishedAt),
		UnpublishAt: parseAdminTime(in.UnpublishAt),
		CreatedBy:   actor.UserID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			d.Errs = templates.ContentErrors{Slug: i18n.T(ctx, "admin.content.invalid_slug_taken")}
			s.renderContentFormError(w, r, d)
			return
		}
		s.renderError(w, r, err.Error())
		return
	}
	s.snapshotRevision(ctx, entry, actor.UserID)
	s.auditContent(ctx, actor.UserID, "content.created", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.created"))
	Navigate(w, r, "/admin/content")
}

// POST /admin/content/{id} — update.
func (s *Server) handleAdminContentUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	existing, _, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	in, t, meta, ok := s.decodeContentForm(w, r)
	if !ok {
		return
	}
	// The revision panel must survive a rejected save: an editor fixing a
	// validation error still needs the history they were about to restore from.
	revisions, err := s.q.ListRevisionsByEntry(ctx, sqlc.ListRevisionsByEntryParams{
		EntryID: existing.ID, Lim: contentRevisionLimit,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	d := s.editorDataFromInput(existing.ID, t, in, meta, existing.Status, revisions, r)
	valid, errs := validContent(ctx, t, in, meta)
	if !valid {
		d.Errs = errs
		s.renderContentFormError(w, r, d)
		return
	}
	html, renderErr := content.Render([]byte(in.BodyMd))
	if renderErr != nil {
		s.renderError(w, r, renderErr.Error())
		return
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	entry, err := s.q.UpdateEntry(ctx, sqlc.UpdateEntryParams{
		ID: existing.ID, Title: in.Title, Slug: in.Slug, Locale: in.Locale,
		Summary: in.Summary, BodyMd: in.BodyMd, BodyHtml: html, Meta: metaJSON,
		PublishedAt: parseAdminTime(in.PublishedAt),
		UnpublishAt: parseAdminTime(in.UnpublishAt),
	})
	if err != nil {
		if isUniqueViolation(err) {
			d.Errs = templates.ContentErrors{Slug: i18n.T(ctx, "admin.content.invalid_slug_taken")}
			s.renderContentFormError(w, r, d)
			return
		}
		s.renderError(w, r, err.Error())
		return
	}
	s.snapshotRevision(ctx, entry, actor.UserID)
	s.auditContent(ctx, actor.UserID, "content.updated", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.updated"))
	Navigate(w, r, "/admin/content")
}

// POST /admin/content/preview — the live preview pane. Returns ONLY the
// fragment, never a layout, and renders through the same goldmark instance
// the save path uses, so preview and publication cannot diverge.
func (s *Server) handleAdminContentPreview(w http.ResponseWriter, r *http.Request) {
	var in contentFormInput
	if err := decodeForm(r, &in); err != nil {
		http.Error(w, "invalid form", http.StatusUnprocessableEntity)
		return
	}
	html, err := content.Render([]byte(in.BodyMd))
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ContentPreview(html).Render(r.Context(), w); err != nil {
		s.log.Error("render", "error", err, "path", r.URL.Path)
	}
}

// POST /admin/content/{id}/publish — an already-set future published_at stays,
// which is exactly what makes the entry scheduled rather than live.
func (s *Server) handleAdminContentPublish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	existing, _, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	publishedAt := existing.PublishedAt
	if !publishedAt.Valid {
		publishedAt = pgtype.Timestamptz{Time: s.cfg.Now(), Valid: true}
	}
	entry, err := s.q.SetEntryStatus(ctx, sqlc.SetEntryStatusParams{
		ID: existing.ID, Status: "published", PublishedAt: publishedAt,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.auditContent(ctx, actor.UserID, "content.published", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.published"))
	Navigate(w, r, "/admin/content")
}

// POST /admin/content/{id}/unpublish — published_at is retained so
// re-publishing restores the original date.
func (s *Server) handleAdminContentUnpublish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	existing, _, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	entry, err := s.q.SetEntryStatus(ctx, sqlc.SetEntryStatusParams{
		ID: existing.ID, Status: "draft", PublishedAt: existing.PublishedAt,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.auditContent(ctx, actor.UserID, "content.unpublished", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.unpublished"))
	Navigate(w, r, "/admin/content")
}

// POST /admin/content/{id}/delete — revisions cascade (FK, migration 0019).
func (s *Server) handleAdminContentDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	entry, _, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	if err := s.q.DeleteEntry(ctx, entry.ID); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.auditContent(ctx, actor.UserID, "content.deleted", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.deleted"))
	Navigate(w, r, "/admin/content")
}

// POST /admin/content/{id}/revisions/{rev}/restore — restoring is itself a
// save, so the current state is snapshotted first by the same code path.
func (s *Server) handleAdminContentRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := identity.UserFrom(ctx)
	existing, _, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	revID, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if err != nil || revID < 1 {
		http.NotFound(w, r)
		return
	}
	rev, err := s.q.GetRevision(ctx, sqlc.GetRevisionParams{ID: revID, EntryID: existing.ID})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	html, err := content.Render([]byte(rev.BodyMd))
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	entry, err := s.q.UpdateEntry(ctx, sqlc.UpdateEntryParams{
		ID: existing.ID, Title: rev.Title, Slug: existing.Slug, Locale: existing.Locale,
		Summary: rev.Summary, BodyMd: rev.BodyMd, BodyHtml: html, Meta: rev.Meta,
		PublishedAt: existing.PublishedAt, UnpublishAt: existing.UnpublishAt,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.snapshotRevision(ctx, entry, actor.UserID)
	s.auditContent(ctx, actor.UserID, "content.restored", entry)
	s.cms.Invalidate()
	Toast(w, "success", i18n.T(ctx, "admin.content.restored"))
	Navigate(w, r, "/admin/content")
}

// --- form decoding, validation, and shared plumbing --------------------

// contentFormInput is the fixed part of the editor form. Type-declared fields
// arrive as meta_<key> inputs and are read separately: the prefix keeps a
// field named "title" or "slug" from colliding with a core input.
type contentFormInput struct {
	Kind        string `form:"kind"`
	Title       string `form:"title"`
	Slug        string `form:"slug"`
	Locale      string `form:"locale"`
	Summary     string `form:"summary"`
	BodyMd      string `form:"body_md"`
	PublishedAt string `form:"published_at"`
	UnpublishAt string `form:"unpublish_at"`
}

// decodeContentForm parses the body, resolves the type, and collects the
// declared meta fields. Unknown meta_* inputs are dropped, never stored.
func (s *Server) decodeContentForm(w http.ResponseWriter, r *http.Request) (contentFormInput, content.Type, map[string]string, bool) {
	var in contentFormInput
	if err := decodeForm(r, &in); err != nil {
		http.Error(w, "invalid form", http.StatusUnprocessableEntity)
		return in, content.Type{}, nil, false
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Slug = strings.TrimSpace(in.Slug)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Summary = strings.TrimSpace(in.Summary)
	in.PublishedAt = strings.TrimSpace(in.PublishedAt)
	in.UnpublishAt = strings.TrimSpace(in.UnpublishAt)

	t, ok := s.types.Get(in.Kind)
	if !ok {
		s.handleNotFound(w, r)
		return in, content.Type{}, nil, false
	}
	if in.Slug == "" {
		in.Slug = t.SlugOf(in.Title, s.parseAdminTimeOrNow(in.PublishedAt))
	}
	meta := make(map[string]string, len(t.Fields))
	for _, f := range t.Fields {
		meta[f.Key] = strings.TrimSpace(r.PostFormValue("meta_" + f.Key))
	}
	return in, t, meta, true
}

// validContent enforces every rule the editor can show a message for. The DB
// constraints are the backstop; these are the words a human reads.
func validContent(ctx context.Context, t content.Type, in contentFormInput, meta map[string]string) (bool, templates.ContentErrors) {
	errs := templates.ContentErrors{Meta: map[string]string{}}
	ok := true
	if in.Title == "" || len(in.Title) > 200 {
		errs.Title = i18n.T(ctx, "admin.content.invalid_title")
		ok = false
	}
	if len(in.Slug) > 200 || !contentSlugRe.MatchString(in.Slug) {
		errs.Slug = i18n.T(ctx, "admin.content.invalid_slug")
		ok = false
	}
	if in.Locale != "" {
		if _, supported := i18n.ParseSupported(in.Locale); !supported {
			errs.Locale = i18n.T(ctx, "admin.content.invalid_locale")
			ok = false
		}
	}
	if len(in.Summary) > 300 {
		errs.Summary = i18n.T(ctx, "admin.content.invalid_field")
		ok = false
	}
	if strings.TrimSpace(in.BodyMd) == "" {
		errs.Body = i18n.T(ctx, "admin.content.invalid_body")
		ok = false
	}
	published, publishedOK := parseAdminTimeStrict(in.PublishedAt)
	if !publishedOK {
		errs.PublishedAt = i18n.T(ctx, "admin.content.invalid_field")
		ok = false
	}
	unpublish, unpublishOK := parseAdminTimeStrict(in.UnpublishAt)
	if !unpublishOK {
		errs.UnpublishAt = i18n.T(ctx, "admin.content.invalid_field")
		ok = false
	}
	if publishedOK && unpublishOK && !published.IsZero() && !unpublish.IsZero() && !unpublish.After(published) {
		errs.UnpublishAt = i18n.T(ctx, "admin.content.invalid_expiry")
		ok = false
	}
	for _, f := range t.Fields {
		v := meta[f.Key]
		if f.Required && v == "" {
			errs.Meta[f.Key] = i18n.T(ctx, "admin.content.invalid_field")
			ok = false
			continue
		}
		if len(v) > f.Limit() {
			errs.Meta[f.Key] = i18n.T(ctx, "admin.content.invalid_field")
			ok = false
			continue
		}
		switch f.Kind {
		case content.FieldURL:
			if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				errs.Meta[f.Key] = i18n.T(ctx, "admin.content.invalid_field")
				ok = false
			}
		case content.FieldBool:
			if v != "" && v != "true" && v != "false" {
				errs.Meta[f.Key] = i18n.T(ctx, "admin.content.invalid_field")
				ok = false
			}
		case content.FieldSelect:
			if v != "" && !slicesContains(f.Options, v) {
				errs.Meta[f.Key] = i18n.T(ctx, "admin.content.invalid_field")
				ok = false
			}
		}
	}
	return ok, errs
}

func slicesContains(options []string, v string) bool {
	for _, o := range options {
		if o == v {
			return true
		}
	}
	return false
}

// parseAdminTimeStrict reports whether a datetime-local value parses. An
// empty value is valid and yields the zero time.
func parseAdminTimeStrict(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.ParseInLocation(datetimeLocal, v, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// parseAdminTime maps a validated datetime-local value onto the column type.
func parseAdminTime(v string) pgtype.Timestamptz {
	t, ok := parseAdminTimeStrict(v)
	if !ok || t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// parseAdminTimeOrNow falls back to the render clock, not the wall clock, so a
// frozen TEST_NOW yields a stable date-derived slug.
func (s *Server) parseAdminTimeOrNow(v string) time.Time {
	if t, ok := parseAdminTimeStrict(v); ok && !t.IsZero() {
		return t
	}
	return s.cfg.Now()
}

// contentEntry loads the row named by {id} together with its declared type.
// An unknown id or an entry whose kind is no longer registered is a 404.
func (s *Server) contentEntry(w http.ResponseWriter, r *http.Request) (sqlc.ContentEntry, content.Type, bool) {
	id, ok := parseContentID(w, r)
	if !ok {
		return sqlc.ContentEntry{}, content.Type{}, false
	}
	entry, err := s.q.GetEntry(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return sqlc.ContentEntry{}, content.Type{}, false
	}
	t, registered := s.types.Get(entry.Kind)
	if !registered {
		http.NotFound(w, r)
		return sqlc.ContentEntry{}, content.Type{}, false
	}
	return entry, t, true
}

func parseContentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// snapshotRevision records the state that was just saved. Fire-and-forget:
// history is valuable, but losing one snapshot must never fail the save the
// editor already made.
func (s *Server) snapshotRevision(ctx context.Context, entry sqlc.ContentEntry, editorID string) {
	if _, err := s.q.InsertRevision(ctx, sqlc.InsertRevisionParams{
		EntryID: entry.ID, Title: entry.Title, Summary: entry.Summary,
		BodyMd: entry.BodyMd, Meta: entry.Meta, EditorID: editorID,
	}); err != nil {
		s.log.Error("content revision", "error", err, "entry", entry.ID)
	}
}

func (s *Server) auditContent(ctx context.Context, actorID, action string, entry sqlc.ContentEntry) {
	audit.Log(ctx, s.q, "", actorID, action, map[string]any{
		"id": entry.ID, "kind": entry.Kind, "slug": entry.Slug, "locale": entry.Locale,
	})
}

func (s *Server) recentMedia(r *http.Request) ([]sqlc.ContentMedium, error) {
	return s.q.ListMedia(r.Context(), sqlc.ListMediaParams{Lim: contentMediaPicker, Off: 0})
}

// contentLocales are the editor's language options: "" (all languages) first,
// then every supported code.
func contentLocales() []string {
	out := make([]string, 0, len(i18n.Locales)+1)
	out = append(out, "")
	for _, l := range i18n.Locales {
		out = append(out, l.Code)
	}
	return out
}

// editorDataFromInput echoes a rejected submission back to the editor.
func (s *Server) editorDataFromInput(id int64, t content.Type, in contentFormInput, meta map[string]string, status string, revisions []sqlc.ContentRevision, r *http.Request) templates.ContentEditorData {
	media, err := s.recentMedia(r)
	if err != nil {
		s.log.Error("content media list", "error", err)
	}
	return templates.ContentEditorData{
		ID: id, Type: t, Locales: contentLocales(),
		Title: in.Title, Slug: in.Slug, Locale: in.Locale,
		Summary: in.Summary, BodyMd: in.BodyMd, Meta: meta,
		PublishedAt: in.PublishedAt, UnpublishAt: in.UnpublishAt,
		Status: status, Revisions: revisions, Media: media,
		Errs: templates.ContentErrors{Meta: map[string]string{}},
	}
}

// renderContentFormError re-renders the editor form with 422 so the invalid
// submission and its values stay on screen (project-form convention).
func (s *Server) renderContentFormError(w http.ResponseWriter, r *http.Request, d templates.ContentEditorData) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(r.Context(), "admin.content.title"), Layout: templates.LayoutAdmin},
		templates.AdminContentEditor(d))
}

// isUniqueViolation reports a (kind, slug, locale) collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
