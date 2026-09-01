package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/gogogadget/gogogadget/internal/search"
	"github.com/jackc/pgx/v5"
)

type OutboxStore struct{ DB DB }
type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// EnqueueTx records the latest operation for a document inside the caller's
// transaction. Coalescing means a rapid upsert/delete sequence leaves one
// durable intent and never exposes partially committed search state.
func (s *OutboxStore) EnqueueTx(ctx context.Context, tx pgx.Tx, e search.OutboxEntry) error {
	if tx == nil {
		return fmt.Errorf("search outbox: transaction is required")
	}
	if e.TenantID == "" || e.Collection == "" || e.DocumentID == "" {
		return fmt.Errorf("search outbox: tenant, collection, and document are required")
	}
	_, err := tx.Exec(ctx, `INSERT INTO search_outbox (tenant_id,collection,document_id,operation,payload,attempts,available_at,updated_at) VALUES ($1,$2,$3,$4,$5,0,now(),now()) ON CONFLICT (tenant_id,collection,document_id) DO UPDATE SET operation=EXCLUDED.operation,payload=EXCLUDED.payload,attempts=0,available_at=now(),updated_at=now()`, e.TenantID, e.Collection, e.DocumentID, e.Operation, e.Payload)
	return err
}
func (s *OutboxStore) Enqueue(ctx context.Context, e search.OutboxEntry) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("search outbox: database is required")
	}
	if b, ok := s.DB.(txBeginner); ok {
		tx, err := b.Begin(ctx)
		if err != nil {
			return err
		}
		if err := s.EnqueueTx(ctx, tx, e); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	return fmt.Errorf("search outbox: database does not support transactions")
}

func (s *OutboxStore) Claim(ctx context.Context, limit int) ([]search.OutboxEntry, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("search outbox: database is required")
	}
	if limit <= 0 {
		limit = 50
	}
	b, ok := s.DB.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("search outbox: database does not support transactions")
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id,collection,document_id,operation,payload,attempts,available_at,created_at,updated_at FROM search_outbox WHERE available_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	entries, scanErr := scanRows(rows)
	rows.Close()
	if scanErr != nil {
		_ = tx.Rollback(ctx)
		return nil, scanErr
	}
	for _, e := range entries {
		if _, err := tx.Exec(ctx, `UPDATE search_outbox SET available_at=$4,updated_at=now() WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, e.TenantID, e.Collection, e.DocumentID, time.Now().Add(5*time.Minute)); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return entries, nil
}
func scanRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]search.OutboxEntry, error) {
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
	if s == nil || s.DB == nil {
		return fmt.Errorf("search outbox: database is required")
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM search_outbox WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, e.TenantID, e.Collection, e.DocumentID)
	return err
}
func (s *OutboxStore) Backoff(ctx context.Context, e search.OutboxEntry, at time.Time) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("search outbox: database is required")
	}
	_, err := s.DB.Exec(ctx, `UPDATE search_outbox SET attempts=attempts+1,available_at=$4,updated_at=now() WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, e.TenantID, e.Collection, e.DocumentID, at)
	return err
}
