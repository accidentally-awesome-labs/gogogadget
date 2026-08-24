package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nullText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func TestAdminFlagRolloutUpdatesValue(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_fr", "org_fr")
	require.NoError(t, s.q.UpsertFeatureFlag(t.Context(), sqlc.UpsertFeatureFlagParams{Key: "rollout_test", Enabled: true, Rollout: 0}))

	form := url.Values{"rollout": []string{"50"}}
	code, _, _ := postForm(t, s, "/admin/flags/rollout_test/rollout", form, sessionCookie("user_fr", "org_fr", "org:admin"))
	assert.Equal(t, http.StatusOK, code)

	flag, err := s.q.GetFeatureFlag(t.Context(), "rollout_test")
	require.NoError(t, err)
	assert.EqualValues(t, 50, flag.Rollout)
}

func TestAdminFlagRolloutRejectsOutOfRange(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_fr2", "org_fr2")
	require.NoError(t, s.q.UpsertFeatureFlag(t.Context(), sqlc.UpsertFeatureFlagParams{Key: "rollout_bad", Enabled: true, Rollout: 0}))

	for _, v := range []string{"101", "-1", "abc"} {
		form := url.Values{"rollout": []string{v}}
		code, _, _ := postForm(t, s, "/admin/flags/rollout_bad/rollout", form, sessionCookie("user_fr2", "org_fr2", "org:admin"))
		assert.Equal(t, http.StatusUnprocessableEntity, code, "rollout %s", v)
	}
	flag, err := s.q.GetFeatureFlag(t.Context(), "rollout_bad")
	require.NoError(t, err)
	assert.EqualValues(t, 0, flag.Rollout, "invalid input must not change the value")
}

// Admin viewers: audit + jobs pages, filter, dead-letter requeue.

func TestAdminAuditPageRendersAndFilters(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_av", "org_av")
	seedMembership(t, s, "user_av2", "org_av2", "org:member")
	s.q.InsertAuditLog(t.Context(), sqlc.InsertAuditLogParams{
		ClerkOrgID:  nullText("org_av"),
		ClerkUserID: nullText("user_av"),
		Action:      "project.created",
		Metadata:    []byte(`{}`),
	})
	s.q.InsertAuditLog(t.Context(), sqlc.InsertAuditLogParams{
		ClerkOrgID:  nullText("org_av2"),
		ClerkUserID: nullText("user_av2"),
		Action:      "account.exported",
		Metadata:    []byte(`{}`),
	})

	cookie := sessionCookie("user_av", "org_av", "org:admin")
	code, _, body := serve(t, s, "GET", "/admin/audit", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="audit-table"`)
	assert.Contains(t, body, "project.created")
	assert.Contains(t, body, "account.exported")
	assert.Contains(t, body, "org_av", "org column visible")

	code, _, body = serve(t, s, "GET", "/admin/audit?q=account.exported", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "account.exported")
	assert.NotContains(t, body, "project.created", "filter narrows the platform-wide list")

	code, _, body = serve(t, s, "GET", "/admin/audit?q=no-such-action", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "No audit rows match", "empty state")
}

func TestAdminJobsPageAndRequeue(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_jv", "org_jv")
	deadID, err := s.q.EnqueueJob(t.Context(), sqlc.EnqueueJobParams{Kind: "email.digest", Payload: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, s.q.DeadLetterJob(t.Context(), sqlc.DeadLetterJobParams{ID: deadID, Reason: pgtype.Text{String: "exhausted", Valid: true}}))
	doneID, err := s.q.EnqueueJob(t.Context(), sqlc.EnqueueJobParams{Kind: "email.welcome", Payload: []byte(`{}`)})
	require.NoError(t, err)
	require.NoError(t, s.q.CompleteJob(t.Context(), doneID))

	cookie := sessionCookie("user_jv", "org_jv", "org:admin")
	code, _, body := serve(t, s, "GET", "/admin/jobs", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="jobs-table"`)
	assert.Contains(t, body, "email.digest")
	assert.Contains(t, body, "dead", "dead-letter status surfaces")
	assert.Contains(t, body, `data-testid="job-requeue-`+strconv.FormatInt(deadID, 10)+`"`, "requeue offered on dead rows")

	code, _, body = serve(t, s, "GET", "/admin/jobs?q=welcome", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "email.welcome")
	assert.NotContains(t, body, "email.digest", "kind filter narrows")

	code, _, _ = postForm(t, s, "/admin/jobs/"+strconv.FormatInt(deadID, 10)+"/requeue", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	rows, err := s.q.ListJobs(t.Context(), sqlc.ListJobsParams{Filter: "email.digest", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "pending", rows[0].Status, "requeue revives the dead row")

	// Requeueing a non-dead job is a guarded no-op.
	code, _, _ = postForm(t, s, "/admin/jobs/"+strconv.FormatInt(doneID, 10)+"/requeue", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	rows, err = s.q.ListJobs(t.Context(), sqlc.ListJobsParams{Filter: "email.welcome", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Equal(t, "done", rows[0].Status, "a completed job must not be revived")
}
