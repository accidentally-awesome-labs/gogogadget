package content_test

import (
	"context"
	"testing"
	"time"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cmsFixture(t *testing.T) (*pgxpool.Pool, *sqlc.Queries, *content.CMS) {
	t.Helper()
	pool, q := testdb.Open(t, "cms")
	reg, err := content.NewRegistry(content.DefaultTypes())
	require.NoError(t, err)
	return pool, q, content.NewCMS(q, reg)
}

func publishPost(t *testing.T, q *sqlc.Queries, slug, locale, title string) sqlc.ContentEntry {
	t.Helper()
	row, err := q.CreateEntry(t.Context(), sqlc.CreateEntryParams{
		Kind: "post", Slug: slug, Locale: locale, Title: title,
		BodyMd: "body", BodyHtml: "<p>body</p>", Meta: []byte(`{"author":"A"}`),
		Status:      "published",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)
	return row
}

// The cache is what keeps a public page off the database on every request;
// its TTL is also why an admin mutation must invalidate explicitly.
func TestCMSCachesUntilInvalidated(t *testing.T) {
	pool, q, cms := cmsFixture(t)
	ctx := context.Background()
	publishPost(t, q, "cached", "", "Cached")

	first, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Mutate underneath the cache: the snapshot must still be served.
	_, err = pool.Exec(ctx, "UPDATE content_entries SET title = 'Changed' WHERE slug = 'cached'")
	require.NoError(t, err)

	stale, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "Cached", stale[0].Title, "a 30s TTL means the next read is served from memory")

	cms.Invalidate()
	fresh, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, fresh, 1)
	assert.Equal(t, "Changed", fresh[0].Title)
}

// A query hiccup must not blank a published page: the last good snapshot is
// served, and the expiry stays in the past so the next request retries. A
// pair that was never loaded has nothing to fall back on and must error
// rather than pretend the collection is empty.
func TestCMSServesStaleOnQueryError(t *testing.T) {
	pool, q, cms := cmsFixture(t)
	ctx := context.Background()
	publishPost(t, q, "stale", "", "Stale")

	loaded, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, loaded, 1)

	cms.Invalidate() // expires the snapshot; the next read re-queries
	// Break the read the way a bad migration or a permission change would.
	_, err = pool.Exec(ctx, "ALTER TABLE content_entries RENAME TO content_entries_hidden")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE content_entries_hidden RENAME TO content_entries")
	})

	served, err := cms.List(ctx, "post", "en")
	require.NoError(t, err, "a failed refresh must not blank a published page")
	require.Len(t, served, 1)
	assert.Equal(t, "Stale", served[0].Title)

	_, err = cms.List(ctx, "post", "es")
	require.Error(t, err, "a first read with no snapshot must surface the failure")
}

// An unregistered kind is a typo. Rendering nothing would hide it; the error
// names the kind.
func TestCMSRejectsUnregisteredKind(t *testing.T) {
	_, _, cms := cmsFixture(t)
	_, err := cms.List(context.Background(), "wombat", "en")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wombat")
}

// Locales are cached independently: a Spanish read must not poison the
// English snapshot for the same kind.
func TestCMSCachesPerLocale(t *testing.T) {
	_, q, cms := cmsFixture(t)
	ctx := context.Background()
	publishPost(t, q, "shared", "", "Shared")
	publishPost(t, q, "shared", "es", "Compartido")

	en, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, en, 1, "one row per slug")
	assert.Equal(t, "Shared", en[0].Title)

	es, err := cms.List(ctx, "post", "es")
	require.NoError(t, err)
	require.Len(t, es, 1, "the variant replaces the shared row, it does not add to it")
	assert.Equal(t, "Compartido", es[0].Title)

	// Reading es must not have changed en.
	en, err = cms.List(ctx, "post", "en")
	require.NoError(t, err)
	assert.Equal(t, "Shared", en[0].Title)
}

func TestCMSBySlugAndLatest(t *testing.T) {
	_, q, cms := cmsFixture(t)
	ctx := context.Background()
	publishPost(t, q, "older", "", "Older")
	newer, err := q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "newer", Title: "Newer",
		BodyMd: "b", BodyHtml: "<p>b</p>", Meta: []byte("{}"), Status: "published",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})
	require.NoError(t, err)

	latest, err := cms.Latest(ctx, "post", "en")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, newer.Slug, latest.Slug, "List re-sorts by date; DISTINCT ON orders by slug")

	got, err := cms.BySlug(ctx, "post", "older", "en")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "A", got.Field("author"), "meta round-trips through JSONB")

	// A miss is (nil, nil): the handler 404s, it does not 500.
	missing, err := cms.BySlug(ctx, "post", "never-written", "en")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

// The markdown corpus is a seed, and seeding runs more than once. A second
// import must add nothing — an operator who edited a post in the admin must
// not have it silently reverted by the next `make seed`.
func TestImportIsIdempotentAndNeverClobbers(t *testing.T) {
	_, q, cms := cmsFixture(t)
	ctx := context.Background()

	posts, releases, err := content.Import(ctx, q, contentfs.FS)
	require.NoError(t, err)
	assert.Positive(t, posts)
	assert.Positive(t, releases)

	live, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	require.Len(t, live, posts, "imported posts are published at their frontmatter date")

	// An operator edits an imported entry.
	edited := live[0]
	_, err = q.UpdateEntry(ctx, sqlc.UpdateEntryParams{
		ID: edited.ID, Title: "Edited by an operator", Slug: edited.Slug, Locale: edited.Locale,
		Summary: edited.Summary, BodyMd: "edited", BodyHtml: "<p>edited</p>", Meta: []byte("{}"),
		PublishedAt: pgtype.Timestamptz{Time: edited.PublishedAt, Valid: true},
	})
	require.NoError(t, err)

	posts2, releases2, err := content.Import(ctx, q, contentfs.FS)
	require.NoError(t, err)
	assert.Zero(t, posts2, "a second import adds no posts")
	assert.Zero(t, releases2, "a second import adds no releases")

	cms.Invalidate()
	after, err := cms.List(ctx, "post", "en")
	require.NoError(t, err)
	assert.Len(t, after, posts, "no duplicates")
	stored, err := q.GetEntry(ctx, edited.ID)
	require.NoError(t, err)
	assert.Equal(t, "Edited by an operator", stored.Title, "re-seeding must never clobber an edit")
}

// The publish instant belongs to the DATABASE clock, and this pins it
// deterministically rather than hoping two clocks agree.
//
// now() is transaction_timestamp(), constant for the life of one transaction.
// So if PublishEntry stamps published_at from the database, the value it
// returns is byte-identical to that transaction's now(); if it were stamped
// from the application clock — as it was — equality is impossible, because the
// two readings come from different sources at microsecond resolution.
//
// The defect this defends against had teeth: visibility is decided by
// `published_at <= now()`, so an instant taken from the app clock is invisible
// until the database clock catches up, and a database whose clock trails the
// app's by more than one request hides a just-published entry — contradicting
// the promise that publishing shows on the next request, not the next TTL.
// Measured on the project's own test stack the trail was 0.7 ms idle and up to
// 5.0 ms under load, against a request that closes the gap in 2.4 ms at best.
func TestPublishStampsTheDatabaseClock(t *testing.T) {
	pool, _, _ := cmsFixture(t)
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)

	draft, err := q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "clock-owner", Locale: "", Title: "Clock owner",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte("{}"), Status: "draft",
	})
	require.NoError(t, err)
	require.False(t, draft.PublishedAt.Valid, "a draft carries no instant to keep")

	published, err := q.PublishEntry(ctx, draft.ID)
	require.NoError(t, err)
	require.Equal(t, "published", published.Status)

	var transactionNow time.Time
	require.NoError(t, tx.QueryRow(ctx, "select now()").Scan(&transactionNow))
	assert.True(t, published.PublishedAt.Valid, "publishing stamps an instant")
	assert.True(t, published.PublishedAt.Time.Equal(transactionNow),
		"published_at = %s, want this transaction's now() %s: the instant must come from the clock the visibility predicate reads",
		published.PublishedAt.Time, transactionNow)

	// And a date already on the row is kept, which is what makes a future one
	// scheduled rather than live.
	scheduled, err := q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "clock-keeper", Locale: "", Title: "Clock keeper",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte("{}"), Status: "draft",
		PublishedAt: pgtype.Timestamptz{Time: transactionNow.Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	kept, err := q.PublishEntry(ctx, scheduled.ID)
	require.NoError(t, err)
	assert.True(t, kept.PublishedAt.Time.Equal(scheduled.PublishedAt.Time),
		"a declared date survives publishing")
}
