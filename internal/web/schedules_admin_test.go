package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminSchedulesCRUD(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_sc", "org_sc")
	cookie := sessionCookie("user_sc", "org_sc", "org:admin")

	// Page renders with the create form.
	code, _, body := serve(t, s, "GET", "/admin/schedules", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="schedule-create-form"`)
	assert.Contains(t, body, `data-testid="schedules-table"`)
	assert.Contains(t, body, "email.digest", "schedulable kinds offered")
	assert.Contains(t, body, "System-wide")

	// Invalid: interval below 60 → 422 with values kept.
	form := url.Values{"name": []string{"Too fast"}, "kind": []string{"email.digest"}, "every_seconds": []string{"30"}, "payload": []string{""}, "org": []string{""}}
	code, _, body = postForm(t, s, "/admin/schedules", form, cookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "alert-danger")

	// Invalid: unschedulable kind → 422.
	form = url.Values{"name": []string{"Bad kind"}, "kind": []string{"webhook.deliver"}, "every_seconds": []string{"3600"}, "payload": []string{""}, "org": []string{""}}
	code, _, _ = postForm(t, s, "/admin/schedules", form, cookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	// Invalid: payload not a JSON object → 422.
	form = url.Values{"name": []string{"Bad payload"}, "kind": []string{"usage.flush"}, "every_seconds": []string{"3600"}, "payload": []string{"[1,2]"}, "org": []string{""}}
	code, _, _ = postForm(t, s, "/admin/schedules", form, cookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	// Valid create (org-scoped).
	form = url.Values{"name": []string{"Audit digest"}, "kind": []string{"usage.flush"}, "every_seconds": []string{"3600"}, "payload": []string{`{"x":1}`}, "org": []string{"org_sc"}}
	code, _, _ = postForm(t, s, "/admin/schedules", form, cookie)
	assert.Equal(t, http.StatusOK, code)
	rows, err := s.q.ListSchedules(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, "Audit digest", row.Name)
	assert.Equal(t, "usage.flush", row.Kind)
	assert.True(t, row.ClerkOrgID.Valid)
	assert.Equal(t, "org_sc", row.ClerkOrgID.String)
	assert.True(t, row.Enabled)
	assert.EqualValues(t, 3600, row.EverySeconds)

	// Run-now on an enabled schedule moves next_run_at to ~now.
	code, _, _ = postForm(t, s, "/admin/schedules/"+itoa64(row.ID)+"/run", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	fresh, err := s.q.GetSchedule(t.Context(), row.ID)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), fresh.NextRunAt.Time, 5*time.Second, "next run is claimable on the next pass")

	// Toggle off → run-now is a guarded no-op.
	code, _, _ = postForm(t, s, "/admin/schedules/"+itoa64(row.ID)+"/toggle", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	disabled, err := s.q.GetSchedule(t.Context(), row.ID)
	require.NoError(t, err)
	assert.False(t, disabled.Enabled)
	require.NoError(t, s.q.RunScheduleNow(t.Context(), row.ID))
	still, err := s.q.GetSchedule(t.Context(), row.ID)
	require.NoError(t, err)
	assert.False(t, still.NextRunAt.Time.Before(disabled.NextRunAt.Time), "run-now must not fire a disabled schedule")

	// Delete.
	code, _, _ = postForm(t, s, "/admin/schedules/"+itoa64(row.ID)+"/delete", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	rows, err = s.q.ListSchedules(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows)

	// Every mutation audited.
	countAudit := func(action string) int {
		rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: action, Off: 0, Lim: 10})
		require.NoError(t, err)
		return len(rows)
	}
	for _, action := range []string{"schedule.created", "schedule.run_now", "schedule.updated", "schedule.deleted"} {
		assert.Equal(t, 1, countAudit(action), action)
	}
}
