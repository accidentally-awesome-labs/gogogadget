package webhooks_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedOrg(t *testing.T, q *sqlc.Queries, id string) {
	t.Helper()
	_, err := q.UpsertOrg(context.Background(), sqlc.UpsertOrgParams{OrgID: id, Name: id, Slug: id, ImageUrl: ""})
	require.NoError(t, err)
}

func insertEndpoint(t *testing.T, q *sqlc.Queries, orgID, url, events string, disabled bool) int64 {
	t.Helper()
	var eventTypes []string = []string{} // NOT NULL column: always a real array
	if events != "{}" {                  // '{}' wildcard = empty array = subscribe-all
		require.NoError(t, json.Unmarshal([]byte(events), &eventTypes))
	}
	ep, err := q.InsertWebhookEndpoint(context.Background(), sqlc.InsertWebhookEndpointParams{
		OrgID: orgID, CreatedBy: "user_w", Url: url, Secret: webhooks.NewSecret(), EventTypes: eventTypes, Description: "",
	})
	require.NoError(t, err)
	if disabled {
		require.NoError(t, q.SetWebhookEndpointDisabled(context.Background(), sqlc.SetWebhookEndpointDisabledParams{
			ID: ep.ID, OrgID: orgID, Disabled: true,
		}))
	}
	return ep.ID
}

func TestNewSecretFormatAndUniqueness(t *testing.T) {
	a, b := webhooks.NewSecret(), webhooks.NewSecret()
	assert.Regexp(t, `^whsec_[A-Za-z0-9_-]+={0,2}$`, a, "whsec_ + base64url (padded)")
	assert.NotEqual(t, a, b, "two mints must differ")
}

func TestEmitFansOutPerSubscribedActiveEndpoint(t *testing.T) {
	_, q := testdb.Open(t, "webhooks")
	ctx := context.Background()
	seedOrg(t, q, "org_w")

	sub := insertEndpoint(t, q, "org_w", "https://a.example.com/cb", `["project.created"]`, false)
	insertEndpoint(t, q, "org_w", "https://b.example.com/cb", `["project.created","project.deleted"]`, false) // multi-subscription
	insertEndpoint(t, q, "org_w", "https://c.example.com/cb", `["project.created"]`, true)                    // disabled

	webhooks.Emit(ctx, q, "org_w", "project.created", map[string]any{"id": 1})

	deliveries, err := q.ListDeliveriesByOrg(ctx, "org_w")
	require.NoError(t, err)
	require.Len(t, deliveries, 2, "subscribed+active endpoints only")
	for _, d := range deliveries {
		assert.Equal(t, "project.created", d.EventType)
		assert.Contains(t, string(d.Payload), `"type": "project.created"`, "envelope carries the event type")
		assert.Contains(t, string(d.Payload), `"id": 1`, "envelope carries the data")
	}

	jobs, err := q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "webhook.deliver", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Len(t, jobs, 2, "one queued delivery job per delivery")
	assert.NotZero(t, sub)
}

func TestEmitWildcardReceivesEverything(t *testing.T) {
	_, q := testdb.Open(t, "webhooks2")
	ctx := context.Background()
	seedOrg(t, q, "org_w2")
	insertEndpoint(t, q, "org_w2", "https://all.example.com/cb", `{}`, false)

	webhooks.Emit(ctx, q, "org_w2", "project.archived", map[string]any{"id": 2})

	deliveries, err := q.ListDeliveriesByOrg(ctx, "org_w2")
	require.NoError(t, err)
	assert.Len(t, deliveries, 1, "'{}' wildcard endpoint receives every event")
}

func TestEmitUnsubscribedEventWritesNothing(t *testing.T) {
	_, q := testdb.Open(t, "webhooks3")
	ctx := context.Background()
	seedOrg(t, q, "org_w3")

	webhooks.Emit(ctx, q, "org_w3", "project.created", map[string]any{"id": 3})

	deliveries, err := q.ListDeliveriesByOrg(ctx, "org_w3")
	require.NoError(t, err)
	assert.Empty(t, deliveries)
	jobs, err := q.ListJobs(ctx, sqlc.ListJobsParams{Filter: "", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestEmitUnmarshalableDataLogsAndStops(t *testing.T) {
	_, q := testdb.Open(t, "webhooks4")
	ctx := context.Background()
	seedOrg(t, q, "org_w4")
	insertEndpoint(t, q, "org_w4", "https://x.example.com/cb", `{}`, false)

	// A channel cannot marshal: Emit logs and returns before any DB write.
	assert.NotPanics(t, func() {
		webhooks.Emit(ctx, q, "org_w4", "project.created", map[string]any{"ch": make(chan int)})
	})
	deliveries, err := q.ListDeliveriesByOrg(ctx, "org_w4")
	require.NoError(t, err)
	assert.Empty(t, deliveries, "no delivery rows on unmarshalable data")
}
