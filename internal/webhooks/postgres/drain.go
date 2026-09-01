package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Drain claims webhook outbox rows, fans each event out to matching endpoint
// delivery rows, and queues the existing retry/dead-letter job. The outbox row
// is deleted only after all delivery rows/jobs are durable.
type DrainDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func Drain(ctx context.Context, db DrainDB, q *sqlc.Queries, limit int) error {
	if db == nil || q == nil {
		return fmt.Errorf("webhook drain: database and queries are required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(ctx, `SELECT id,org_id,event_type,payload FROM webhook_outbox WHERE available_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var orgID, typ string
		var payload []byte
		if err := rows.Scan(&id, &orgID, &typ, &payload); err != nil {
			return err
		}
		var data any
		if err := json.Unmarshal(payload, &data); err != nil {
			return err
		}
		endpoints, err := q.ListActiveEndpointsForEvent(ctx, sqlc.ListActiveEndpointsForEventParams{OrgID: orgID, EventType: typ})
		if err != nil {
			return err
		}
		for _, ep := range endpoints {
			d, err := q.InsertWebhookDelivery(ctx, sqlc.InsertWebhookDeliveryParams{EndpointID: ep.ID, OrgID: orgID, EventType: typ, Payload: payload})
			if err != nil {
				return err
			}
			if err := jobs.Enqueue(ctx, q, jobs.KindWebhookDeliver, jobs.WebhookDeliverPayload{DeliveryID: d.ID}); err != nil {
				return err
			}
		}
		if _, err := db.Exec(ctx, `DELETE FROM webhook_outbox WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return rows.Err()
}
