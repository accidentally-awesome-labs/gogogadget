package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fmt"
	"github.com/gogogadget/gogogadget/internal/db"
	"reflect"
)

const (
	exportSecret    = "whsec_live_do_not_export_me"
	exportTokenHash = "deadbeefcafe0000deadbeefcafe0000deadbeefcafe0000deadbeefcafe0000"
)

// seedExportOrg builds an org carrying one of everything the export touches,
// including the two pieces of secret material that must never leave.
func seedExportOrg(t *testing.T, pool poolExec, q *sqlc.Queries, orgID string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO orgs (clerk_org_id, name, slug) VALUES ($1, 'Export Org', $1) ON CONFLICT DO NOTHING`, []any{orgID}},
		{`INSERT INTO users (clerk_user_id, email, name) VALUES ('user_exp', 'exp@example.com', 'Exporter') ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO org_members (clerk_org_id, clerk_user_id, role) VALUES ($1, 'user_exp', 'org:admin') ON CONFLICT DO NOTHING`, []any{orgID}},
		{`INSERT INTO projects (clerk_org_id, name) VALUES ($1, 'Exported project')`, []any{orgID}},
		{`INSERT INTO files (clerk_org_id, uploader_user_id, filename, content_type, size_bytes, storage_key)
		  VALUES ($1, 'user_exp', 'notes.txt', 'text/plain', 12, 'k/notes.txt')`, []any{orgID}},
		{`INSERT INTO api_tokens (clerk_org_id, name, token_hash, scope) VALUES ($1, 'ci', $2, 'read')`, []any{orgID, exportTokenHash}},
		{`INSERT INTO webhook_endpoints (clerk_org_id, created_by, url, secret, event_types)
		  VALUES ($1, 'user_exp', 'https://example.test/hook', $2, ARRAY['project.created'])`, []any{orgID, exportSecret}},
		{`INSERT INTO audit_log (clerk_org_id, clerk_user_id, action, metadata) VALUES ($1, 'user_exp', 'project.created', '{"id":1}')`, []any{orgID}},
		{`INSERT INTO subscriptions (clerk_org_id, polar_customer_id, product_key, status)
		  VALUES ($1, 'cus_x', 'pro', 'active') ON CONFLICT DO NOTHING`, []any{orgID}},
	} {
		_, err := pool.Exec(ctx, stmt.sql, stmt.args...)
		require.NoError(t, err, stmt.sql)
	}
}

func exportWorker(t *testing.T, q *sqlc.Queries, sender mail.Sender) *Worker {
	t.Helper()
	w := NewWorker(q, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Storage = storagefs.NewDevStore(t.TempDir())
	return w
}

func TestOrgExportContainsEveryCollection(t *testing.T) {
	pool, q := testdb.Open(t, "jobsexport")
	ctx := context.Background()
	orgID := "org_exp1"
	seedExportOrg(t, pool, q, orgID)

	w := exportWorker(t, q, &captureSender{})
	out, err := w.collectOrgExport(ctx, orgID)
	require.NoError(t, err)

	assert.Equal(t, orgID, out.Org.ID)
	assert.Len(t, out.Members, 1)
	assert.Equal(t, "org:admin", out.Members[0].Role)
	assert.Len(t, out.Projects, 1)
	assert.Equal(t, "Exported project", out.Projects[0].Name)
	assert.Len(t, out.Files, 1)
	assert.Len(t, out.APITokens, 1)
	assert.Equal(t, "ci", out.APITokens[0].Name)
	assert.Len(t, out.Webhooks, 1)
	assert.True(t, out.Webhooks[0].Enabled)
	assert.NotEmpty(t, out.Audit)
	require.NotNil(t, out.Sub)
	assert.Equal(t, "pro", out.Sub.Plan)
}

// The whole reason this maps to DTOs instead of marshaling sqlc rows: the
// token hash and the webhook signing secret carry json tags, so encoding the
// rows would write live credentials into a file the customer downloads.
func TestOrgExportNeverLeaksSecrets(t *testing.T) {
	pool, q := testdb.Open(t, "jobsexport")
	ctx := context.Background()
	orgID := "org_exp2"
	seedExportOrg(t, pool, q, orgID)

	w := exportWorker(t, q, &captureSender{})
	out, err := w.collectOrgExport(ctx, orgID)
	require.NoError(t, err)

	body, err := json.Marshal(out)
	require.NoError(t, err)
	blob := string(body)

	assert.NotContains(t, blob, exportSecret, "a webhook signing secret in an export lets any holder forge deliveries")
	assert.NotContains(t, blob, exportTokenHash, "the stored hash is what protects API access")
	for _, field := range []string{"token_hash", "secret", "secret_previous", "storage_key"} {
		assert.NotContains(t, blob, `"`+field+`"`, "field %q must not appear in an export", field)
	}
	// …and the export says so, rather than leaving the recipient guessing.
	assert.Contains(t, out.Notice, "Secrets are excluded")
}

// A cross-org row must not ride along: exports are org-scoped by definition.
func TestOrgExportIsScopedToOneOrg(t *testing.T) {
	pool, q := testdb.Open(t, "jobsexport")
	ctx := context.Background()
	seedExportOrg(t, pool, q, "org_exp3")
	_, err := pool.Exec(ctx, `INSERT INTO orgs (clerk_org_id, name, slug) VALUES ('org_other', 'Other', 'other') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO projects (clerk_org_id, name) VALUES ('org_other', 'Not yours')`)
	require.NoError(t, err)

	w := exportWorker(t, q, &captureSender{})
	out, err := w.collectOrgExport(ctx, "org_exp3")
	require.NoError(t, err)

	body, _ := json.Marshal(out)
	assert.NotContains(t, string(body), "Not yours")
	assert.NotContains(t, string(body), "org_other")
}

// End to end through the job: file stored, files row written, user notified.
func TestOrgExportJobStoresFileAndNotifies(t *testing.T) {
	pool, q := testdb.Open(t, "jobsexport")
	ctx := context.Background()
	orgID := "org_exp4"
	seedExportOrg(t, pool, q, orgID)

	w := exportWorker(t, q, &captureSender{})
	payload := ExportProjectsPayload{OrgID: orgID, UserID: "user_exp"}
	require.NoError(t, w.exportOrgJSON(ctx, payload))

	files, err := q.ListFilesByOrg(ctx, sqlc.ListFilesByOrgParams{ClerkOrgID: orgID, Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, files, 2, "the seeded file plus the export")
	var export sqlc.File
	for _, f := range files {
		if strings.HasPrefix(f.Filename, "organization-") {
			export = f
		}
	}
	require.NotZero(t, export.ID, "the export must be recorded as a file")
	assert.Equal(t, "application/json", export.ContentType)
	assert.Positive(t, export.SizeBytes)

	// Read the object back through the seam the app actually uses.
	rec := httptest.NewRecorder()
	require.NoError(t, w.Storage.Serve(ctx, rec, export.StorageKey, export.Filename, export.ContentType))
	raw := rec.Body.Bytes()
	var round orgExport
	require.NoError(t, json.Unmarshal(raw, &round), "the stored object must be valid JSON")
	assert.Equal(t, orgID, round.Org.ID)
	assert.NotContains(t, string(raw), exportSecret)

	notes, err := q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
		ClerkOrgID: orgID, ClerkUserID: "user_exp", Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.NotEmpty(t, notes, "the requester must learn the export is ready")
	assert.Equal(t, "export.ready", notes[0].Kind)
	assert.Contains(t, notes[0].Url, "/app/files/")
}

// A storage failure must not leave a files row pointing at nothing.
func TestOrgExportJobFailsLoudlyWithoutStorage(t *testing.T) {
	_, q := testdb.Open(t, "jobsexport")
	w := NewWorker(q, &captureSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := ExportProjectsPayload{OrgID: "org_exp5", UserID: "user_exp"}
	err := w.exportOrgJSON(context.Background(), payload)
	require.Error(t, err, "no storage configured is a job failure, not a silent no-op")
	assert.Contains(t, err.Error(), "storage")
}

// Two exports for one org inside the same second must not share a storage
// key: the second Put would overwrite the first object while both files rows
// survive, and a download already streaming the old bytes is cut off. Caught
// by an e2e flake, not by review.
func TestExportKeysAreUniqueWithinASecond(t *testing.T) {
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	first := exportKey("org_x", at, "projects-20260820-120000.csv")
	second := exportKey("org_x", at.Add(time.Millisecond), "projects-20260820-120000.csv")

	assert.NotEqual(t, first, second, "same-second exports must not collide")
	assert.Contains(t, first, "exports/org_x/", "still namespaced per organization")
	assert.Contains(t, first, "projects-20260820-120000.csv", "the human filename survives in the key")
}

// Every table a module declares as org-scoped and exportable must appear as a
// collection in the org export. The export's DTOs are hand-written — the
// redaction decisions are the whole point — but the coverage list is generated
// from the same declarations, so a newly installed data module that declares
// exportable rows cannot be omitted silently.
//
// This exact gap existed: usage_events was declared exportable and collected
// by nothing.
func TestOrgExportCoversEveryDeclaredExportableTable(t *testing.T) {
	collections := map[string]bool{
		"orgs": true, "org_members": true, "projects": true, "files": true,
		"api_tokens": true, "webhook_endpoints": true, "audit_log": true,
		"subscriptions": true, "usage_events": true,
	}
	out := orgExport{}
	value := reflect.ValueOf(&out).Elem()
	for _, field := range reflect.VisibleFields(value.Type()) {
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		collections[name] = true
	}
	var missing []string
	for _, d := range db.DataLifecycleRegistry {
		if d.Scope != "org" || !d.Export {
			continue
		}
		if !collections[d.Table] {
			missing = append(missing, fmt.Sprintf("%s (declared by %s)", d.Table, d.Module))
		}
	}
	require.Empty(t, missing,
		"org export omits tables declared exportable; a data module was installed into silence: %v", missing)
}
