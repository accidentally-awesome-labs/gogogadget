package jobs

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
)

// exportProjectsCSV renders the org's projects to CSV through the storage
// seam (DevStore when unconfigured — works zero-account), records a files row
// so it shows on /app/files, and notifies the requesting user with the link.
func (w *Worker) exportProjectsCSV(ctx context.Context, p ExportProjectsPayload) error {
	if w.Storage == nil {
		return fmt.Errorf("export.projects_csv: no storage configured on worker")
	}

	projects, err := w.q.ListAllProjectsByOrg(ctx, p.OrgID)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	if err := cw.Write([]string{"id", "name", "status", "created_at"}); err != nil {
		return err
	}
	for _, pr := range projects {
		if err := cw.Write([]string{
			strconv.FormatInt(pr.ID, 10), pr.Name, pr.Status, pr.CreatedAt.Time.UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}

	now := time.Now().UTC()
	filename := "projects-" + now.Format("20060102-150405") + ".csv"
	key := exportKey(p.OrgID, now, filename)
	size, err := w.Storage.Put(ctx, key, "text/csv", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	f, err := w.q.InsertFile(ctx, sqlc.InsertFileParams{
		ClerkOrgID: p.OrgID, UploaderUserID: p.UserID, Filename: filename,
		ContentType: "text/csv", SizeBytes: size, StorageKey: key,
	})
	if err != nil {
		_ = w.Storage.Delete(ctx, key)
		return err
	}
	notify.Send(ctx, w.q, p.OrgID, p.UserID, "export.ready", "Projects export ready", filename, "/app/files/"+strconv.FormatInt(f.ID, 10))
	return nil
}
