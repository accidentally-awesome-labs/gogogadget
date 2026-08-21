package web

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Dunning: the human half of a failed payment. The processor retries the
// card; these are the messages that get someone to fix it.

// postPolarWebhook signs and delivers one Polar event. Each call needs a
// unique message id — the handler dedupes on it.
var polarMsgSeq int

func postPolarWebhook(t *testing.T, s *Server, payload []byte) {
	t.Helper()
	polarMsgSeq++
	id := "msg_dun_" + strconv.Itoa(polarMsgSeq)
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", payload, signStandard(t, testPolarWebhookSecret, id, payload))
	require.Equal(t, http.StatusOK, code)
}

type dunningJob struct {
	Kind  string
	RunAt time.Time
}

func dunningJobs(t *testing.T, s *Server, orgID string) []dunningJob {
	t.Helper()
	rows, err := s.db.Query(t.Context(),
		`SELECT kind, run_at FROM jobs WHERE payload->>'org_id' = $1 AND kind LIKE 'email.dunning%' ORDER BY run_at`, orgID)
	require.NoError(t, err)
	defer rows.Close()
	var out []dunningJob
	for rows.Next() {
		var j dunningJob
		require.NoError(t, rows.Scan(&j.Kind, &j.RunAt))
		out = append(out, j)
	}
	return out
}

func TestPastDueSchedulesFollowUpSequence(t *testing.T) {
	s := polarServer(t, nil)
	seedOrg(t, s, "org_dun", "Dun Org")
	seedMembership(t, s, "user_dun", "org_dun", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(t.Context(), "DELETE FROM jobs WHERE payload->>'org_id' = 'org_dun'")
	})

	before := time.Now()
	postPolarWebhook(t, s, subPayload("subscription.updated", "sub_dun", "org_dun", "prod_pro", "past_due",
		time.Now().Add(20*24*time.Hour)))

	jobs := dunningJobs(t, s, "org_dun")
	require.Len(t, jobs, 2, "one reminder and one final notice, scheduled at the failure")
	assert.Equal(t, "email.dunning_reminder", jobs[0].Kind)
	assert.Equal(t, "email.dunning_final", jobs[1].Kind)

	// Scheduled, not sent now — the whole point is that they arrive later.
	assert.WithinDuration(t, before.Add(billing.DunningReminderAfter), jobs[0].RunAt, time.Minute)
	assert.WithinDuration(t, before.Add(billing.DunningFinalAfter), jobs[1].RunAt, time.Minute)
	assert.True(t, jobs[0].RunAt.Before(jobs[1].RunAt), "the reminder must precede the final notice")
}

// Re-delivery of the same status must not stack a second sequence: a customer
// whose card fails five times should not get five sets of warnings.
func TestRepeatedPastDueDoesNotStackSequences(t *testing.T) {
	s := polarServer(t, nil)
	seedMembership(t, s, "user_dun2", "org_dun2", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(t.Context(), "DELETE FROM jobs WHERE payload->>'org_id' = 'org_dun2'")
	})

	payload := subPayload("subscription.updated", "sub_dun2", "org_dun2", "prod_pro", "past_due",
		time.Now().Add(20*24*time.Hour))
	postPolarWebhook(t, s, payload)
	postPolarWebhook(t, s, payload)
	postPolarWebhook(t, s, payload)

	assert.Len(t, dunningJobs(t, s, "org_dun2"), 2, "only the transition INTO past_due schedules the sequence")
}

func TestRecoveryBeforeFollowUpLeavesNothingScheduledTwice(t *testing.T) {
	s := polarServer(t, nil)
	seedMembership(t, s, "user_dun3", "org_dun3", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(t.Context(), "DELETE FROM jobs WHERE payload->>'org_id' = 'org_dun3'")
	})

	postPolarWebhook(t, s, subPayload("subscription.updated", "sub_dun3", "org_dun3", "prod_pro", "past_due",
		time.Now().Add(20*24*time.Hour)))
	require.Len(t, dunningJobs(t, s, "org_dun3"), 2)

	// Card fixed: status goes back to active. The jobs stay queued — the
	// worker's guard is what stops them, and that is tested in internal/jobs.
	postPolarWebhook(t, s, subPayload("subscription.updated", "sub_dun3", "org_dun3", "prod_pro", "active",
		time.Now().Add(20*24*time.Hour)))
	sub, err := s.q.GetSubscriptionByOrg(t.Context(), "org_dun3")
	require.NoError(t, err)
	assert.Equal(t, "active", sub.Status)

	// …and a later failure schedules a fresh sequence, because this is a new
	// transition into past_due.
	postPolarWebhook(t, s, subPayload("subscription.updated", "sub_dun3", "org_dun3", "prod_pro", "past_due",
		time.Now().Add(20*24*time.Hour)))
	assert.Len(t, dunningJobs(t, s, "org_dun3"), 4, "a second failure earns a second sequence")
}

func TestDunningEmailsCarryBillingLink(t *testing.T) {
	s := polarServer(t, nil)
	seedMembership(t, s, "user_dun4", "org_dun4", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(t.Context(), "DELETE FROM jobs WHERE payload->>'org_id' = 'org_dun4'")
	})
	postPolarWebhook(t, s, subPayload("subscription.updated", "sub_dun4", "org_dun4", "prod_pro", "past_due",
		time.Now().Add(20*24*time.Hour)))

	rows, err := s.db.Query(t.Context(),
		`SELECT payload->>'html', payload->>'subject' FROM jobs WHERE payload->>'org_id' = 'org_dun4' AND kind LIKE 'email.dunning%'`)
	require.NoError(t, err)
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var html, subject string
		require.NoError(t, rows.Scan(&html, &subject))
		assert.NotEmpty(t, subject)
		assert.Contains(t, html, "/app/settings/billing", "every dunning email must link to the fix")
		seen++
	}
	assert.Equal(t, 2, seen)
}

// The sink rejects a stage it does not know rather than silently sending the
// wrong email.
func TestDunningSinkRejectsUnknownStage(t *testing.T) {
	s := integrationServer(t, nil)
	err := emailSink{s: s}.EnqueueDunning(t.Context(), "nag", "a@example.com", time.Time{}, "org_x", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nag")
}

var _ billing.EmailSink = emailSink{} // the sink still satisfies the seam

var _ = sqlc.Subscription{}
