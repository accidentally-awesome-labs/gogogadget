package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type PostgresOutbox struct{ DB DB }

func (o *PostgresOutbox) Enqueue(ctx context.Context, e Entry) error {
	if o == nil || o.DB == nil {
		return fmt.Errorf("audit export outbox: database is required")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = o.DB.Exec(ctx, `INSERT INTO audit_export_outbox (entry) VALUES ($1)`, raw)
	return err
}
func (o *PostgresOutbox) Drain(ctx context.Context, exporter Exporter, limit int) error {
	if o == nil || o.DB == nil || exporter == nil {
		return fmt.Errorf("audit export outbox: database and exporter are required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := o.DB.Query(ctx, `SELECT id,entry FROM audit_export_outbox WHERE available_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		ec, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = exporter.Export(ec, e)
		cancel()
		if err != nil {
			_, _ = o.DB.Exec(ctx, `UPDATE audit_export_outbox SET attempts=attempts+1,available_at=now()+LEAST((2^attempts)*interval '1 second',interval '1 hour') WHERE id=$1`, id)
			continue
		}
		if _, err := o.DB.Exec(ctx, `DELETE FROM audit_export_outbox WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return rows.Err()
}
