package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateUpDown(t *testing.T) {
	pool, _ := testdb.Open(t, "dbmig")
	ctx := context.Background()
	require.NoError(t, db.MigrateDown(ctx, pool))
	require.NoError(t, db.Migrate(ctx, pool))
}

func TestRoundtripEveryTable(t *testing.T) {
	_, q := testdb.Open(t, "db")
	ctx := context.Background()

	// users
	u, err := q.UpsertUser(ctx, sqlc.UpsertUserParams{
		ClerkUserID: "user_rt1", Email: "rt@example.com", Name: "RT", AvatarUrl: "",
	})
	require.NoError(t, err)
	assert.Equal(t, "rt@example.com", string(u.Email))
	gotU, err := q.GetUserByClerkID(ctx, "user_rt1")
	require.NoError(t, err)
	assert.Equal(t, u.ClerkUserID, gotU.ClerkUserID)
	assert.Equal(t, "weekly", gotU.DigestFrequency, "a new user is opted into the digest by default")
	assert.False(t, gotU.LastDigestAt.Valid, "never sent = due immediately")

	// digest cadence + stamp
	require.NoError(t, q.SetUserDigestFrequency(ctx, sqlc.SetUserDigestFrequencyParams{
		ClerkUserID: "user_rt1", DigestFrequency: "daily",
	}))
	require.NoError(t, q.MarkUserDigestSent(ctx, "user_rt1"))
	gotU, err = q.GetUserByClerkID(ctx, "user_rt1")
	require.NoError(t, err)
	assert.Equal(t, "daily", gotU.DigestFrequency)
	require.True(t, gotU.LastDigestAt.Valid)
	// A user stamped just now is no longer due on any cadence.
	due, err := q.ListUsersDueForDigest(ctx, 100)
	require.NoError(t, err)
	for _, d := range due {
		assert.NotEqual(t, "user_rt1", d.ClerkUserID, "a freshly stamped user must drop out of the due set")
	}

	// orgs
	_, err = q.UpsertOrg(ctx, sqlc.UpsertOrgParams{
		ClerkOrgID: "org_rt1", Name: "RT Org", Slug: "rt-org", ImageUrl: "",
	})
	require.NoError(t, err)
	_, err = q.GetOrgByClerkID(ctx, "org_rt1")
	require.NoError(t, err)

	// memberships
	require.NoError(t, q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{
		ClerkOrgID: "org_rt1", ClerkUserID: "user_rt1", Role: "org:admin",
	}))
	members, err := q.ListMembersByOrg(ctx, "org_rt1")
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "org:admin", members[0].Role)

	// subscriptions (upsert conflict target is clerk_org_id)
	sub, err := q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_rt1", PolarSubscriptionID: pgtype.Text{String: "sub_1", Valid: true},
		PolarCustomerID: "cust_1", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)
	sub2, err := q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_rt1", PolarSubscriptionID: pgtype.Text{String: "sub_2", Valid: true},
		PolarCustomerID: "cust_1", ProductKey: "team", Status: "trialing",
	})
	require.NoError(t, err)
	assert.Equal(t, sub.ID, sub2.ID, "resubscribe must overwrite the same org row")
	assert.Equal(t, "sub_2", sub2.PolarSubscriptionID.String)

	// webhook idempotency
	id1, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: "wh_rt1", Provider: "clerk", EventType: "user.created"})
	require.NoError(t, err)
	assert.Equal(t, "wh_rt1", id1)
	_, err = q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: "wh_rt1", Provider: "clerk", EventType: "user.created"})
	require.ErrorIs(t, err, pgx.ErrNoRows, "duplicate delivery must be a no-op")

	// audit
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		ClerkOrgID:  pgtype.Text{String: "org_rt1", Valid: true},
		ClerkUserID: pgtype.Text{String: "user_rt1", Valid: true},
		Action:      "project.created",
		Metadata:    []byte(`{"name":"x"}`),
	})
	require.NoError(t, err)
	rows, err := q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{ClerkOrgID: pgtype.Text{String: "org_rt1", Valid: true}, Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "rt@example.com", rows[0].ActorEmail)

	// projects
	p, err := q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_rt1", Name: "Alpha"})
	require.NoError(t, err)
	_, err = q.UpdateProject(ctx, sqlc.UpdateProjectParams{ID: p.ID, ClerkOrgID: "org_rt1", Name: "Alpha 2"})
	require.NoError(t, err)
	found, err := q.ListProjectsByOrg(ctx, sqlc.ListProjectsByOrgParams{ClerkOrgID: "org_rt1", Column2: "lpha", Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "Alpha 2", found[0].Name)
	// cross-org reads never leak
	_, err = q.GetProjectByID(ctx, sqlc.GetProjectByIDParams{ID: p.ID, ClerkOrgID: "org_other"})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// jobs
	jid, err := q.EnqueueJob(ctx, sqlc.EnqueueJobParams{Kind: "email.welcome", Payload: []byte(`{"to":"rt@example.com"}`)})
	require.NoError(t, err)
	job, err := q.ClaimJob(ctx)
	require.NoError(t, err)
	assert.Equal(t, jid, job.ID)
	assert.Equal(t, int32(1), job.Attempts)
	require.NoError(t, q.CompleteJob(ctx, jid))
	_, err = q.ClaimJob(ctx)
	require.ErrorIs(t, err, pgx.ErrNoRows, "completed job must not be reclaimed")

	// api tokens
	tid, err := q.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{
		ClerkOrgID: "org_rt1", Name: "ci", TokenHash: "hash_rt1", Scope: "write",
	})
	require.NoError(t, err)
	tok, err := q.GetAPITokenByHash(ctx, "hash_rt1")
	require.NoError(t, err)
	assert.Equal(t, tid, tok.ID)
	require.NoError(t, q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: tid, ClerkOrgID: "org_rt1"}))
	_, err = q.GetAPITokenByHash(ctx, "hash_rt1")
	require.ErrorIs(t, err, pgx.ErrNoRows, "revoked token must not authenticate")

	// announcements — one-active partial unique index is the invariant
	a1, err := q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{Kind: "info", Message: "First", Url: ""})
	require.NoError(t, err)
	assert.False(t, a1.Active, "inserts land inactive")
	a2, err := q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{Kind: "critical", Message: "Second", Url: "https://example.com"})
	require.NoError(t, err)
	_, err = q.GetActiveAnnouncement(ctx)
	require.ErrorIs(t, err, pgx.ErrNoRows, "nothing active yet")
	require.NoError(t, q.SetAnnouncementActive(ctx, sqlc.SetAnnouncementActiveParams{ID: a1.ID, Active: true}))
	require.Error(t, q.SetAnnouncementActive(ctx, sqlc.SetAnnouncementActiveParams{ID: a2.ID, Active: true}),
		"partial unique index must reject a second active row")
	require.NoError(t, q.DeactivateAnnouncements(ctx))
	require.NoError(t, q.SetAnnouncementActive(ctx, sqlc.SetAnnouncementActiveParams{ID: a2.ID, Active: true}))
	active, err := q.GetActiveAnnouncement(ctx)
	require.NoError(t, err)
	assert.Equal(t, a2.ID, active.ID)
	all, err := q.ListAnnouncements(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, a2.ID, all[0].ID, "newest first")
	require.NoError(t, q.DeleteAnnouncement(ctx, a1.ID))
	all, err = q.ListAnnouncements(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	// notification preferences — upsert idempotency on (user, kind)
	require.NoError(t, q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
		ClerkUserID: "user_rt1", Kind: "welcome", InApp: false,
	}))
	pref, err := q.GetNotificationPreference(ctx, sqlc.GetNotificationPreferenceParams{ClerkUserID: "user_rt1", Kind: "welcome"})
	require.NoError(t, err)
	assert.False(t, pref.InApp)
	require.NoError(t, q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
		ClerkUserID: "user_rt1", Kind: "welcome", InApp: true,
	}))
	prefs, err := q.ListNotificationPreferencesByUser(ctx, "user_rt1")
	require.NoError(t, err)
	require.Len(t, prefs, 1, "upsert must not duplicate the (user, kind) row")
	assert.True(t, prefs[0].InApp)

	// admin audit queries — platform-wide, filtered
	allAudit, err := q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, allAudit, 1)
	assert.Equal(t, "rt@example.com", allAudit[0].ActorEmail)
	filtered, err := q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "project.created", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	none, err := q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "no-such-action", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Empty(t, none)
	n, err := q.CountAuditAll(ctx, "project.created")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	byUser, err := q.ListAuditByUser(ctx, sqlc.ListAuditByUserParams{UserID: pgtype.Text{String: "user_rt1", Valid: true}, Lim: 10})
	require.NoError(t, err)
	require.Len(t, byUser, 1)

	// admin jobs queries — status projection + dead-letter requeue
	jobRows, err := q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, jobRows, 1)
	assert.Equal(t, "done", jobRows[0].Status)
	jid2, err := q.EnqueueJob(ctx, sqlc.EnqueueJobParams{Kind: "email.digest", Payload: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, q.DeadLetterJob(ctx, jid2))
	jobRows, err = q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "digest", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, jobRows, 1)
	assert.Equal(t, "dead", jobRows[0].Status)
	jn, err := q.CountJobs(ctx, "digest")
	require.NoError(t, err)
	assert.Equal(t, int64(1), jn)
	require.NoError(t, q.RequeueDeadJob(ctx, jid2))
	requeued, err := q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "digest", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Equal(t, "pending", requeued[0].Status, "requeue resets done_at/attempts")
	assert.False(t, requeued[0].LastError.Valid, "requeue clears last_error")
	require.NoError(t, q.RequeueDeadJob(ctx, jid2))
	stillPending, err := q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "digest", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Equal(t, "pending", stillPending[0].Status, "double requeue is a no-op, not a revive")
	// cleanup so ClaimJob assertions elsewhere keep their queue-empty meaning
	claimed, err := q.ClaimJob(ctx)
	require.NoError(t, err)
	require.NoError(t, q.CompleteJob(ctx, claimed.ID))

	// impersonation sessions must not block account deletion (no FK cascade)
	_, err = q.UpsertUser(ctx, sqlc.UpsertUserParams{
		ClerkUserID: "user_rt2", Email: "rt2@example.com", Name: "RT2", AvatarUrl: "",
	})
	require.NoError(t, err)
	sess, err := q.InsertImpersonationSession(ctx, sqlc.InsertImpersonationSessionParams{
		ID: "sess_rt1", AdminUserID: "user_rt2", TargetUserID: "user_rt1", TargetOrgID: "org_rt1",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.NoError(t, q.DeleteImpersonationSessionsForUser(ctx, "user_rt1"))
	_, err = q.GetImpersonationSession(ctx, sess.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// audit retention: future cutoff deletes, past cutoff keeps
	future := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	n, err = q.DeleteOldAuditRows(ctx, future)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "cutoff in the future deletes everything inserted so far")
	past := pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{Action: "retention.check", Metadata: []byte(`{}`)})
	require.NoError(t, err)
	n, err = q.DeleteOldAuditRows(ctx, past)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "cutoff in the past deletes nothing recent")

	// sole-admin guard
	admins, err := q.CountAdminsByOrg(ctx, "org_rt1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), admins)

	// idempotency keys — the claim is a PK conflict, so a second claim with
	// the same (org, key) must return no rows rather than a second row.
	claim := sqlc.ClaimIdempotencyKeyParams{
		ClerkOrgID: "org_rt1", Key: "k1", Endpoint: "POST /api/v1/projects", RequestHash: "abc",
	}
	row, err := q.ClaimIdempotencyKey(ctx, claim)
	require.NoError(t, err)
	assert.EqualValues(t, 0, row.Status, "a fresh claim is in-flight until completed")
	_, err = q.ClaimIdempotencyKey(ctx, claim)
	require.ErrorIs(t, err, pgx.ErrNoRows, "the second claimant must lose")

	require.NoError(t, q.CompleteIdempotencyKey(ctx, sqlc.CompleteIdempotencyKeyParams{
		ClerkOrgID: "org_rt1", Key: "k1", Status: 201, Response: []byte(`{"id":1}`),
	}))
	stored, err := q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{ClerkOrgID: "org_rt1", Key: "k1"})
	require.NoError(t, err)
	assert.EqualValues(t, 201, stored.Status)
	assert.Equal(t, `{"id":1}`, string(stored.Response), "bytes are stored verbatim, not normalized")

	// retention: a fresh key survives a past cutoff, a future cutoff sweeps it
	n, err = q.DeleteOldIdempotencyKeys(ctx, pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	n, err = q.DeleteOldIdempotencyKeys(ctx, pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	// content — the CMS tables. kind carries no CHECK on purpose: registering
	// a new content type is a Go change, never a migration.
	entry, err := q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "roundtrip", Locale: "", Title: "Roundtrip",
		Summary: "s", BodyMd: "# hi", BodyHtml: "<h1>hi</h1>",
		Meta: []byte(`{"author":"Ada"}`), Status: "draft",
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"author":"Ada"}`, string(entry.Meta), "meta round-trips through JSONB")

	entry, err = q.UpdateEntry(ctx, sqlc.UpdateEntryParams{
		ID: entry.ID, Title: "Roundtrip v2", Slug: entry.Slug, Locale: entry.Locale,
		Summary: "s2", BodyMd: "# hi2", BodyHtml: "<h1>hi2</h1>", Meta: []byte(`{"author":"Grace"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "Roundtrip v2", entry.Title)

	entry, err = q.SetEntryStatus(ctx, sqlc.SetEntryStatusParams{
		ID: entry.ID, Status: "published",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)

	live := func(locale string) []sqlc.ContentEntry {
		rows, err := q.ListLiveEntries(ctx, sqlc.ListLiveEntriesParams{Kind: "post", Locale: locale, Lim: 100})
		require.NoError(t, err)
		return rows
	}
	require.Len(t, live("en"), 1)

	// A locale variant of the SAME slug replaces the all-languages row for
	// readers in that language — one row per slug either way.
	esVariant, err := q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "roundtrip", Locale: "es", Title: "Ida y vuelta",
		BodyMd: "es", BodyHtml: "<p>es</p>", Meta: []byte("{}"), Status: "published",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)
	esRows := live("es")
	require.Len(t, esRows, 1, "the variant replaces the shared row, it does not add to it")
	assert.Equal(t, "Ida y vuelta", esRows[0].Title)
	enRows := live("en")
	require.Len(t, enRows, 1)
	assert.Equal(t, "Roundtrip v2", enRows[0].Title)

	// A past unpublish_at IS the expired state: no job retires an entry.
	_, err = q.UpdateEntry(ctx, sqlc.UpdateEntryParams{
		ID: esVariant.ID, Title: esVariant.Title, Slug: esVariant.Slug, Locale: esVariant.Locale,
		Summary: "", BodyMd: esVariant.BodyMd, BodyHtml: esVariant.BodyHtml, Meta: esVariant.Meta,
		PublishedAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true},
		UnpublishAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)
	esRows = live("es")
	require.Len(t, esRows, 1)
	assert.Equal(t, "Roundtrip v2", esRows[0].Title, "an expired variant falls back to the shared row")

	// (kind, slug, locale) is unique.
	_, err = q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "roundtrip", Locale: "", Title: "Duplicate",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte("{}"), Status: "draft",
	})
	require.Error(t, err, "a second entry with the same kind, slug and locale must be rejected")

	// published without a date is meaningless: the CHECK refuses it.
	_, err = q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "post", Slug: "no-date", Title: "No date",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte("{}"), Status: "published",
	})
	require.Error(t, err)

	// An unregistered kind is accepted by the SCHEMA: the Go registry is the
	// validator, which is what makes a new type migration-free.
	_, err = q.CreateEntry(ctx, sqlc.CreateEntryParams{
		Kind: "anything", Slug: "free-form", Title: "Free form",
		BodyMd: "x", BodyHtml: "<p>x</p>", Meta: []byte("{}"), Status: "draft",
	})
	require.NoError(t, err, "kind carries no CHECK constraint on purpose")

	rev, err := q.InsertRevision(ctx, sqlc.InsertRevisionParams{
		EntryID: entry.ID, Title: "Roundtrip v2", Summary: "s2", BodyMd: "# hi2",
		Meta: []byte(`{"author":"Grace"}`), EditorID: "user_rt1",
	})
	require.NoError(t, err)
	got, err := q.GetRevision(ctx, sqlc.GetRevisionParams{ID: rev.ID, EntryID: entry.ID})
	require.NoError(t, err)
	assert.Equal(t, "# hi2", got.BodyMd)
	_, err = q.GetRevision(ctx, sqlc.GetRevisionParams{ID: rev.ID, EntryID: esVariant.ID})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a revision id from another entry must miss, not cross-load")

	media, err := q.InsertMedia(ctx, sqlc.InsertMediaParams{
		Filename: "diagram.png", ContentType: "image/png", SizeBytes: 1234,
		StorageKey: "content/deadbeef.png", Alt: "A diagram", UploadedBy: "user_rt1",
	})
	require.NoError(t, err)
	fetched, err := q.GetMedia(ctx, media.ID)
	require.NoError(t, err)
	assert.Equal(t, "image/png", fetched.ContentType)
	mediaCount, err := q.CountMedia(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, mediaCount)
	require.NoError(t, q.DeleteMedia(ctx, media.ID))

	// Revisions cascade with their entry.
	require.NoError(t, q.DeleteEntry(ctx, entry.ID))
	_, err = q.GetRevision(ctx, sqlc.GetRevisionParams{ID: rev.ID, EntryID: entry.ID})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestProjectSearchFTS(t *testing.T) {
	_, q := testdb.Open(t, "dbsearch")
	ctx := context.Background()

	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{ClerkOrgID: "org_s", Name: "S", Slug: "s", ImageUrl: ""})
	require.NoError(t, err)
	seed := []string{"Quarterly planning", "Launch checklist", "Quarterly revenue report", "Onboarding docs"}
	for _, name := range seed {
		_, err := q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_s", Name: name})
		require.NoError(t, err)
	}

	list := func(query string) []sqlc.Project {
		rows, err := q.ListProjectsByOrg(ctx, sqlc.ListProjectsByOrgParams{ClerkOrgID: "org_s", Column2: query, Limit: 50, Offset: 0})
		require.NoError(t, err)
		return rows
	}

	// Exact word: FTS matches both "Quarterly" rows, ranked.
	rows := list("Quarterly")
	require.Len(t, rows, 2)

	// Multi-word websearch syntax: both tokens must match.
	rows = list("Quarterly report")
	require.Len(t, rows, 1)
	assert.Equal(t, "Quarterly revenue report", rows[0].Name)

	// Partial token falls back to ILIKE (FTS alone would miss "check" inside
	// no words — ILIKE catches "checklist").
	rows = list("check")
	require.Len(t, rows, 1)
	assert.Equal(t, "Launch checklist", rows[0].Name)

	// Empty query returns all, newest first.
	rows = list("")
	assert.Len(t, rows, 4)

	// Count matches the same predicate.
	n, err := q.CountProjectsByOrgSearch(ctx, sqlc.CountProjectsByOrgSearchParams{ClerkOrgID: "org_s", Column2: "Quarterly"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
