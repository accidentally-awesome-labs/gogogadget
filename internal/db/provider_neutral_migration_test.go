package db_test

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestProviderNeutralMigrationPreservesLegacyRows(t *testing.T) {
	pool, _ := testdb.Open(t, "provider-neutral")
	ctx := context.Background()
	require.NoError(t, db.MigrateDown(ctx, pool))
	goose.SetBaseFS(os.DirFS("."))
	require.NoError(t, goose.SetDialect("postgres"))
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	require.NoError(t, goose.UpToContext(ctx, sqlDB, "migrations", 19))
	_, err := pool.Exec(ctx, `INSERT INTO users(clerk_user_id,email,name) VALUES ('user_fixture','fixture@example.test','Fixture'); INSERT INTO orgs(clerk_org_id,name,slug) VALUES ('org_fixture','Fixture Org','fixture-org'); INSERT INTO org_members(clerk_org_id,clerk_user_id,role) VALUES ('org_fixture','user_fixture','org:admin'); INSERT INTO subscriptions(clerk_org_id,polar_subscription_id,polar_customer_id,product_key,status) VALUES ('org_fixture','sub_fixture','cust_fixture','pro','active'); INSERT INTO projects(clerk_org_id,name) VALUES ('org_fixture','Project'); INSERT INTO audit_log(clerk_org_id,clerk_user_id,action) VALUES ('org_fixture','user_fixture','fixture'); INSERT INTO files(clerk_org_id,uploader_user_id,filename,content_type,size_bytes,storage_key) VALUES ('org_fixture','user_fixture','f','text/plain',1,'fixture-key'); INSERT INTO notifications(clerk_org_id,clerk_user_id,kind,title) VALUES ('org_fixture','user_fixture','fixture','Fixture'); INSERT INTO api_tokens(clerk_org_id,name,token_hash,scope) VALUES ('org_fixture','token','fixture-hash','read'); INSERT INTO jobs(kind,payload) VALUES ('fixture','{}');`)
	require.NoError(t, err)
	require.NoError(t, goose.UpContext(ctx, sqlDB, "migrations"))
	var user, org, subject, customer, provider, providerSub string
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id FROM users WHERE user_id='user_fixture'`).Scan(&user))
	require.NoError(t, pool.QueryRow(ctx, `SELECT org_id FROM orgs WHERE org_id='org_fixture'`).Scan(&org))
	require.NoError(t, pool.QueryRow(ctx, `SELECT subject FROM identity_subjects WHERE provider='clerk' AND user_id='user_fixture'`).Scan(&subject))
	require.NoError(t, pool.QueryRow(ctx, `SELECT provider_customer_id FROM billing_accounts WHERE provider='polar' AND org_id='org_fixture'`).Scan(&customer))
	require.NoError(t, pool.QueryRow(ctx, `SELECT provider, provider_subscription_id FROM subscriptions WHERE org_id='org_fixture'`).Scan(&provider, &providerSub))
	require.Equal(t, "polar", provider)
	require.Equal(t, "sub_fixture", providerSub)
	require.Equal(t, "user_fixture", user)
	require.Equal(t, "org_fixture", org)
	require.Equal(t, "user_fixture", subject)
	require.Equal(t, "cust_fixture", customer)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM org_members WHERE org_id='org_fixture' AND user_id='user_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE org_id='org_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE org_id='org_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE org_id='org_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE org_id='org_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE org_id='org_fixture'`).Scan(&n))
	require.Equal(t, 1, n)
}
