package db_test

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/stretchr/testify/require"
)

func TestSearchRowsCascadeWithOrganizationDeletion(t *testing.T) {
	pool, q := testdb.Open(t, "search_lifecycle")
	ctx := context.Background()
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: "org_search_cascade", Name: "Search", Slug: "search-cascade"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO search_documents (tenant_id,collection,document_id,text) VALUES ('org_search_cascade','projects','one','private')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO search_outbox (tenant_id,collection,document_id,operation,payload) VALUES ('org_search_cascade','projects','one','upsert','{}')`)
	require.NoError(t, err)
	require.NoError(t, q.DeleteOrg(ctx, "org_search_cascade"))
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM search_documents WHERE tenant_id='org_search_cascade'`).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM search_outbox WHERE tenant_id='org_search_cascade'`).Scan(&n))
	require.Zero(t, n)
}
