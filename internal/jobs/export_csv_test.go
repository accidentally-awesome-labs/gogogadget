package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportProjectsCSVJob(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	w := testWorker(q, t.TempDir())
	dir := t.TempDir()
	w.Storage = storage.NewDevStore(dir)

	_, err := pool.Exec(ctx, "INSERT INTO users (clerk_user_id, email, name, avatar_url) VALUES ('user_ex', 'ex@example.com', 'EX', '') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO orgs (clerk_org_id, name, slug) VALUES ('org_ex', 'EX', 'ex') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	for _, name := range []string{"Alpha", "Beta"} {
		_, err = q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_ex", Name: name})
		require.NoError(t, err)
	}

	require.NoError(t, w.exportProjectsCSV(ctx, sqlc.Job{Payload: mustJSON(t, ExportProjectsPayload{OrgID: "org_ex", UserID: "user_ex"})}))

	// Files row exists, CSV bytes parse.
	var f struct {
		Key      string
		Filename string
	}
	require.NoError(t, pool.QueryRow(ctx, "SELECT storage_key, filename FROM files WHERE clerk_org_id = 'org_ex'").Scan(&f.Key, &f.Filename))
	assert.True(t, strings.HasPrefix(f.Key, "exports/org_ex/projects-"))
	assert.True(t, strings.HasSuffix(f.Key, ".csv"))

	stored, err := os.ReadFile(filepath.Join(dir, f.Key))
	require.NoError(t, err)
	assert.Contains(t, string(stored), "id,name,status,created_at")
	assert.Contains(t, string(stored), "Alpha")
	assert.Contains(t, string(stored), "Beta")

	// Notification with the download link.
	var title, url string
	require.NoError(t, pool.QueryRow(ctx, "SELECT title, url FROM notifications WHERE clerk_user_id = 'user_ex'").Scan(&title, &url))
	assert.Equal(t, "Projects export ready", title)
	assert.True(t, strings.HasPrefix(url, "/app/files/"))
}
