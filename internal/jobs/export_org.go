package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/jackc/pgx/v5/pgtype"
)

// Org-level data export: everything the platform holds for one organization,
// as one JSON document, delivered through the same job → storage → files →
// notification path as the projects CSV.
//
// The account export (/app/settings/account/export) answers "what do you know
// about ME" and runs inline. This answers "what do you hold for MY COMPANY",
// which is a different question with a different audience (an admin leaving
// the product, a procurement review, a data-portability request) and a size
// that does not belong on a request thread.
//
// Rows are mapped to explicit DTOs rather than marshaled straight from sqlc.
// That is not tidiness: `api_tokens.token_hash`, `webhook_endpoints.secret`
// and `secret_previous` all carry json tags, so encoding the rows would write
// live signing secrets and token hashes into a file the customer downloads
// and forwards. The export names every field it publishes.

// exportRowCap bounds each collection. An export is a portable snapshot, not
// a database dump; the manifest records when a collection was truncated so
// the recipient is never quietly handed a partial answer.
const exportRowCap = 10000

type orgExport struct {
	ExportedAt time.Time           `json:"exported_at"`
	Org        exportOrg           `json:"organization"`
	Members    []exportMember      `json:"members"`
	Projects   []exportProject     `json:"projects"`
	Files      []exportFile        `json:"files"`
	APITokens  []exportAPIToken    `json:"api_tokens"`
	Webhooks   []exportWebhook     `json:"webhook_endpoints"`
	Audit      []exportAuditEntry  `json:"audit_log"`
	Sub        *exportSubscription `json:"subscription"`
	Truncated  map[string]bool     `json:"truncated"`
	Notice     string              `json:"notice"`
}

type exportOrg struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type exportMember struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type exportProject struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type exportFile struct {
	ID          int64     `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedBy  string    `json:"uploaded_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// exportAPIToken deliberately has no hash field: the stored SHA-256 is the
// only thing standing between a leaked export and someone else's API access.
type exportAPIToken struct {
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// exportWebhook deliberately has no secret field: exporting a live signing
// secret would let any holder of the file forge deliveries.
type exportWebhook struct {
	ID          int64     `json:"id"`
	URL         string    `json:"url"`
	EventTypes  []string  `json:"event_types"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type exportAuditEntry struct {
	Action    string          `json:"action"`
	ActorID   string          `json:"actor_user_id"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

type exportSubscription struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status"`
	PeriodEnd         *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
}

func (w *Worker) exportOrgJSON(ctx context.Context, job sqlc.Job) error {
	var p ExportProjectsPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return err
	}
	if w.Storage == nil {
		return fmt.Errorf("export.org_json: no storage configured on worker")
	}

	out, err := w.collectOrgExport(ctx, p.OrgID)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	filename := "organization-" + now.Format("20060102-150405") + ".json"
	key := exportKey(p.OrgID, now, filename)
	size, err := w.Storage.Put(ctx, key, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	f, err := w.q.InsertFile(ctx, sqlc.InsertFileParams{
		ClerkOrgID: p.OrgID, UploaderUserID: p.UserID, Filename: filename,
		ContentType: "application/json", SizeBytes: size, StorageKey: key,
	})
	if err != nil {
		_ = w.Storage.Delete(ctx, key) // no dangling object without a row
		return err
	}
	notify.Send(ctx, w.q, p.OrgID, p.UserID, "export.ready",
		"Organization export ready", filename, "/app/files/"+strconv.FormatInt(f.ID, 10))
	return nil
}

// collectOrgExport gathers and redacts. Split out so the contents can be
// asserted without a storage backend.
func (w *Worker) collectOrgExport(ctx context.Context, orgID string) (orgExport, error) {
	out := orgExport{
		ExportedAt: time.Now().UTC(),
		Members:    []exportMember{},
		Projects:   []exportProject{},
		Files:      []exportFile{},
		APITokens:  []exportAPIToken{},
		Webhooks:   []exportWebhook{},
		Audit:      []exportAuditEntry{},
		Truncated:  map[string]bool{},
		Notice: "Secrets are excluded by design: API token hashes and webhook signing " +
			"secrets are never exported. Rotate a webhook secret from Settings → Webhooks.",
	}

	org, err := w.q.GetOrgByClerkID(ctx, orgID)
	if err != nil {
		return out, err
	}
	out.Org = exportOrg{ID: org.ClerkOrgID, Name: org.Name, Slug: org.Slug, CreatedAt: org.CreatedAt.Time.UTC()}

	members, err := w.q.ListMembersByOrg(ctx, orgID)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		out.Members = append(out.Members, exportMember{
			UserID: m.ClerkUserID, Email: string(m.Email), Name: m.Name,
			Role: m.Role, JoinedAt: m.CreatedAt.Time.UTC(),
		})
	}

	projects, err := w.q.ListAllProjectsByOrg(ctx, orgID)
	if err != nil {
		return out, err
	}
	out.Truncated["projects"] = len(projects) > exportRowCap
	for _, pr := range cap2(projects) {
		out.Projects = append(out.Projects, exportProject{
			ID: pr.ID, Name: pr.Name, Status: pr.Status,
			CreatedAt: pr.CreatedAt.Time.UTC(), UpdatedAt: pr.UpdatedAt.Time.UTC(),
		})
	}

	files, err := w.q.ListFilesByOrg(ctx, sqlc.ListFilesByOrgParams{ClerkOrgID: orgID, Limit: exportRowCap, Offset: 0})
	if err != nil {
		return out, err
	}
	for _, f := range files {
		out.Files = append(out.Files, exportFile{
			ID: f.ID, Filename: f.Filename, ContentType: f.ContentType,
			SizeBytes: f.SizeBytes, UploadedBy: f.UploaderUserID, CreatedAt: f.CreatedAt.Time.UTC(),
		})
	}

	tokens, err := w.q.ListAPITokensByOrg(ctx, orgID)
	if err != nil {
		return out, err
	}
	for _, t := range tokens {
		out.APITokens = append(out.APITokens, exportAPIToken{
			Name: t.Name, Scope: t.Scope, CreatedAt: t.CreatedAt.Time.UTC(),
			LastUsedAt: optTime(t.LastUsedAt), ExpiresAt: optTime(t.ExpiresAt), RevokedAt: optTime(t.RevokedAt),
		})
	}

	endpoints, err := w.q.ListWebhookEndpointsByOrg(ctx, orgID)
	if err != nil {
		return out, err
	}
	for _, e := range endpoints {
		out.Webhooks = append(out.Webhooks, exportWebhook{
			ID: e.ID, URL: e.Url, EventTypes: e.EventTypes, Description: e.Description,
			Enabled: !e.DisabledAt.Valid, CreatedAt: e.CreatedAt.Time.UTC(),
		})
	}

	audit, err := w.q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{
		ClerkOrgID: pgtype.Text{String: orgID, Valid: true}, Limit: exportRowCap, Offset: 0,
	})
	if err != nil {
		return out, err
	}
	out.Truncated["audit_log"] = len(audit) == exportRowCap
	for _, a := range audit {
		out.Audit = append(out.Audit, exportAuditEntry{
			Action: a.Action, ActorID: a.ClerkUserID.String,
			Metadata: json.RawMessage(a.Metadata), CreatedAt: a.CreatedAt.Time.UTC(),
		})
	}

	if sub, err := w.q.GetSubscriptionByOrg(ctx, orgID); err == nil {
		out.Sub = &exportSubscription{
			Plan: sub.ProductKey, Status: sub.Status,
			PeriodEnd: optTime(sub.CurrentPeriodEnd), CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		}
	}
	return out, nil
}

func cap2[T any](rows []T) []T {
	if len(rows) > exportRowCap {
		return rows[:exportRowCap]
	}
	return rows
}

func optTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// exportKey builds the storage key for an export artifact.
//
// The filename users see is a second-granularity timestamp, which is friendly
// but not unique: two exports for one organization inside the same second
// produced the SAME key, so the second Put silently overwrote the first
// object while both files rows survived — a download already streaming the
// old bytes gets cut off. The key therefore carries nanoseconds; the pretty
// name stays on the row.
func exportKey(orgID string, at time.Time, filename string) string {
	return "exports/" + orgID + "/" + strconv.FormatInt(at.UnixNano(), 10) + "-" + filename
}
