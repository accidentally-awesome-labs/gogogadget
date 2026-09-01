package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/search"
	"time"
)

type OutboxStore struct{ DB DB }

func (s *OutboxStore) Enqueue(ctx context.Context, e search.OutboxEntry) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("search outbox: database is required")
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO search_outbox (tenant_id,collection,document_id,operation,payload,attempts,available_at,updated_at) VALUES ($1,$2,$3,$4,$5,0,now(),now()) ON CONFLICT (tenant_id,collection,document_id) DO UPDATE SET operation=EXCLUDED.operation,payload=EXCLUDED.payload,attempts=0,available_at=now(),updated_at=now()`, e.TenantID, e.Collection, e.DocumentID, e.Operation, e.Payload)
	return err
}
func (s *OutboxStore) Claim(ctx context.Context, limit int) ([]search.OutboxEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT tenant_id,collection,document_id,operation,payload,attempts,available_at,created_at,updated_at FROM search_outbox WHERE available_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []search.OutboxEntry
	for rows.Next() {
		var e search.OutboxEntry
		if err := rows.Scan(&e.TenantID, &e.Collection, &e.DocumentID, &e.Operation, &e.Payload, &e.Attempts, &e.AvailableAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *OutboxStore) Ack(ctx context.Context, e search.OutboxEntry) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM search_outbox WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, e.TenantID, e.Collection, e.DocumentID)
	return err
}
func (s *OutboxStore) Backoff(ctx context.Context, e search.OutboxEntry, at time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE search_outbox SET attempts=attempts+1,available_at=$4,updated_at=now() WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, e.TenantID, e.Collection, e.DocumentID, at)
	return err
}

var _ = json.Valid
