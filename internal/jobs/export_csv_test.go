package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportProjectsCSVJob(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	w := testWorker(q, t.TempDir())
	dir := t.TempDir()
	w.Storage = storagefs.NewDevStore(dir)

	_, err := pool.Exec(ctx, "INSERT INTO users (user_id, email, name, avatar_url) VALUES ('user_ex', 'ex@example.com', 'EX', '') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO orgs (org_id, name, slug) VALUES ('org_ex', 'EX', 'ex') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	for _, name := range []string{"Alpha", "Beta"} {
		_, err = q.CreateProject(ctx, sqlc.CreateProjectParams{OrgID: "org_ex", Name: name})
		require.NoError(t, err)
	}

	require.NoError(t, w.exportProjectsCSV(ctx, ExportProjectsPayload{OrgID: "org_ex", UserID: "user_ex"}))

	// Files row exists, CSV bytes parse.
	var f struct {
		Key      string
		Filename string
	}
	require.NoError(t, pool.QueryRow(ctx, "SELECT storage_key, filename FROM files WHERE org_id = 'org_ex'").Scan(&f.Key, &f.Filename))
	// The key carries a nanosecond prefix so two exports in the same second
	// cannot overwrite each other; the human filename rides along after it.
	assert.True(t, strings.HasPrefix(f.Key, "exports/org_ex/"), "keys stay namespaced per org: %s", f.Key)
	assert.Contains(t, f.Key, "-projects-")
	assert.True(t, strings.HasSuffix(f.Key, ".csv"))
	assert.NotEqual(t, "exports/org_ex/"+f.Filename, f.Key, "key must not be just the (second-granular) filename")

	stored, err := os.ReadFile(filepath.Join(dir, f.Key))
	require.NoError(t, err)
	assert.Contains(t, string(stored), "id,name,status,created_at")
	assert.Contains(t, string(stored), "Alpha")
	assert.Contains(t, string(stored), "Beta")

	// Notification with the download link.
	var title, url string
	require.NoError(t, pool.QueryRow(ctx, "SELECT title, url FROM notifications WHERE user_id = 'user_ex'").Scan(&title, &url))
	assert.Equal(t, "Projects export ready", title)
	assert.True(t, strings.HasPrefix(url, "/app/files/"))
}
