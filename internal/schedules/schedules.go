// Package schedules is the builder-facing helper for recurring work: a
// schedule row says "enqueue jobs kind `Kind` with `Payload` every
// `EverySeconds`", and the worker's scheduler pass (internal/jobs) claims
// due rows and enqueues them.
package schedules

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// Schedule is the create-time shape; NextRunAt zero = first fire immediately.
type Schedule struct {
	Name, Kind   string
	Payload      map[string]any
	OrgID   string // "" = system-wide
	EverySeconds int
	NextRunAt    time.Time
}

// Create inserts a schedule. every_seconds must be >= 60 (table CHECK).
func Create(ctx context.Context, q *sqlc.Queries, s Schedule) (sqlc.Schedule, error) {
	raw, err := json.Marshal(s.Payload)
	if err != nil {
		return sqlc.Schedule{}, err
	}
	var at pgtype.Timestamptz
	if !s.NextRunAt.IsZero() {
		at = pgtype.Timestamptz{Time: s.NextRunAt, Valid: true}
	}
	return q.CreateSchedule(ctx, sqlc.CreateScheduleParams{
		Name: s.Name, Kind: s.Kind, Payload: raw,
		OrgID:   pgtype.Text{String: s.OrgID, Valid: s.OrgID != ""},
		EverySeconds: int32(s.EverySeconds), NextRunAt: at,
	})
}
