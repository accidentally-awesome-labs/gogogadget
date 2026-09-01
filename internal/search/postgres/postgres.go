// Package postgres implements Postgres full-text search for the search seam.
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
	if i == nil || i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	if d.TenantID == "" || d.Collection == "" || d.ID == "" {
		return fmt.Errorf("search postgres: tenant, collection, and id are required")
	}
	fields, err := json.Marshal(d.Fields)
	if err != nil {
		return fmt.Errorf("search postgres: fields: %w", err)
	}
	_, err = i.db.Exec(ctx, `INSERT INTO search_documents (tenant_id,collection,document_id,text,fields,updated_at) VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT (tenant_id,collection,document_id) DO UPDATE SET text=EXCLUDED.text, fields=EXCLUDED.fields, updated_at=now()`, d.TenantID, d.Collection, d.ID, d.Text, fields)
	return err
}

func (i *Index) Delete(ctx context.Context, tenantID, collection, id string) error {
	if i == nil || i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	if tenantID == "" || collection == "" || id == "" {
		return fmt.Errorf("search postgres: tenant, collection, and id are required")
	}
	_, err := i.db.Exec(ctx, `DELETE FROM search_documents WHERE tenant_id=$1 AND collection=$2 AND document_id=$3`, tenantID, collection, id)
	return err
}

func (i *Index) Query(ctx context.Context, q search.Query) (search.Result, error) {
	if i == nil || i.db == nil {
		return search.Result{}, fmt.Errorf("search postgres: database is required")
	}
	if q.TenantID == "" || q.Collection == "" {
		return search.Result{}, fmt.Errorf("search postgres: tenant and collection are required")
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	args := []any{q.TenantID, q.Collection, q.Text}
	where := []string{"tenant_id=$1", "collection=$2", "search_vector @@ plainto_tsquery('simple',$3)"}
	for key, value := range q.Filters {
		if key == "" || strings.ContainsAny(key, "'\";-") {
			return search.Result{}, fmt.Errorf("search postgres: invalid filter key %q", key)
		}
		args = append(args, key, value)
		where = append(where, fmt.Sprintf("fields->>$%d = $%d", len(args)-1, len(args)))
	}
	cursorScore, cursorID, err := decodeCursor(q.Cursor)
	if err != nil {
		return search.Result{}, err
	}
	if q.Cursor != "" {
		args = append(args, cursorScore, cursorID)
		where = append(where, fmt.Sprintf("(ts_rank(search_vector, plainto_tsquery('simple',$3)) < $%d OR (ts_rank(search_vector, plainto_tsquery('simple',$3)) = $%d AND document_id > $%d))", len(args)-1, len(args)-1, len(args)))
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT document_id, ts_rank(search_vector, plainto_tsquery('simple',$3)) AS score, fields FROM search_documents WHERE %s ORDER BY score DESC, document_id LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := i.db.Query(ctx, query, args...)
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
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &h.Fields); err != nil {
				return out, fmt.Errorf("search postgres: invalid fields: %w", err)
			}
		}
		out.Hits = append(out.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Hits) == limit {
		out.NextCursor = encodeCursor(out.Hits[len(out.Hits)-1].Score, out.Hits[len(out.Hits)-1].ID)
	}
	return out, nil
}

func encodeCursor(score float64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatFloat(score, 'g', -1, 64) + "|" + id))
}
func decodeCursor(cursor string) (float64, string, error) {
	if cursor == "" {
		return 0, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", fmt.Errorf("search postgres: invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", fmt.Errorf("search postgres: invalid cursor")
	}
	score, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, "", fmt.Errorf("search postgres: invalid cursor")
	}
	return score, parts[1], nil
}

func (i *Index) Health(ctx context.Context) error {
	if i == nil || i.db == nil {
		return fmt.Errorf("search postgres: database is required")
	}
	return nil
}
