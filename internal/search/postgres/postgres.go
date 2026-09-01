// Package postgres implements Postgres full-text search for the search seam.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/search"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type Index struct{ db DB }

func New(db DB) *Index { return &Index{db: db} }

var _ search.Index = (*Index)(nil)

func (i *Index) Upsert(ctx context.Context, d search.Document) error {
	if i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	fields, _ := json.Marshal(d.Fields)
	_, err := i.db.Exec(ctx, `INSERT INTO search_documents (tenant_id,collection,document_id,text,fields,updated_at) VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT (tenant_id,collection,document_id) DO UPDATE SET text=EXCLUDED.text, fields=EXCLUDED.fields, updated_at=now()`, d.TenantID, d.Collection, d.ID, d.Text, fields)
	return err
}
func (i *Index) Delete(ctx context.Context, t, c, id string) error {
	if i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	_, err := i.db.Exec(ctx, `DELETE FROM search_documents WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, t, c, id)
	return err
}
func (i *Index) Query(ctx context.Context, q search.Query) (search.Result, error) {
	if i.db == nil {
		return search.Result{}, fmt.Errorf("search postgres: database is required")
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := i.db.Query(ctx, `SELECT document_id, ts_rank(search_vector, plainto_tsquery('simple',$3)) AS score, fields FROM search_documents WHERE tenant_id=$1 AND collection=$2 AND search_vector @@ plainto_tsquery('simple',$3) ORDER BY score DESC, document_id LIMIT $4`, q.TenantID, q.Collection, q.Text, limit)
	if err != nil {
		return search.Result{}, err
	}
	defer rows.Close()
	var out search.Result
	for rows.Next() {
		var h search.Hit
		var raw []byte
		if err := rows.Scan(&h.ID, &h.Score, &raw); err != nil {
			return out, err
		}
		if len(raw) > 0 && json.Unmarshal(raw, &h.Fields) != nil {
			return out, fmt.Errorf("search postgres: invalid fields")
		}
		out.Hits = append(out.Hits, h)
	}
	return out, rows.Err()
}

var _ = pgx.ErrNoRows

func (i *Index) Health(ctx context.Context) error {
	if i == nil || i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	return nil
}
