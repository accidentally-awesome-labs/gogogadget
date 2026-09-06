package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver at /webhooks/polar is provider-neutral: it records
// idempotency on the delivery id and runs the subscription state machine on
// a neutral event. These fixtures are neutral too — a typed
// billing.SubscriptionEvent encoded by the seam's own double.
//
// They used to be Polar's JSON payload signed with the standard-webhooks
// library, which put a hosted adapter inside a payload of
// ggg/page/settings-billing: a per-environment provider selection pinned
// into a suite that has no opinion about which provider is selected, and
// which used the seam's own MockClient two hundred lines further down.
// Polar's signature verification and payload shape are pinned by
// billing/polar's own contract suite.

func subEvent(eventType, subID, orgID, productID, status string, periodEnd time.Time) billing.SubscriptionEvent {
	return billing.SubscriptionEvent{
		Type: eventType, OrgIDHint: orgID,
		ProviderSubscriptionID: subID, ProviderCustomerID: "cust_1",
		ProviderProductID: productID, Status: status,
		CurrentPeriodEnd: periodEnd,
	}
}

// deliverBilling posts one event to the receiver and returns its status. The
// delivery id is explicit at every call site because idempotency is what
// half these tests are about: the same id is a replay, a new id with the
// same event is a provider retry.
func deliverBilling(t *testing.T, s *Server, deliveryID string, event billing.SubscriptionEvent) int {
	t.Helper()
	payload, headers, err := billing.MockDelivery(deliveryID, event)
	require.NoError(t, err)
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", payload, headers)
	return code
}

// billingWebhookServer selects the seam's webhook double and maps the two
// paid plans onto product ids, which is what the receiver reads to resolve a
// plan key.
func billingWebhookServer(t *testing.T, mutate func(*Deps)) *Server {
	t.Helper()
	return integrationServer(t, func(d *Deps) {
		plans := billing.DefaultPlanCatalog().All()
		plans[1].ProviderProductID, plans[2].ProviderProductID = "prod_pro", "prod_team"
		d.BillingCatalog, _ = billing.NewPlanCatalog(plans)
		d.BillingWebhook = billing.MockWebhook{}
		if mutate != nil {
			mutate(d)
		}
	})
}

func TestPolarWebhookReplay(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_pb", "org_pb", "org:admin")
	created := subEvent("subscription.created", "sub_pb1", "org_pb", "prod_pro", "active", time.Now().Add(30*24*time.Hour))

	assert.Equal(t, http.StatusOK, deliverBilling(t, s, "msg_pb1", created))

	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_pb")
	require.NoError(t, err)
	assert.Equal(t, "active", sub.Status)
	assert.Equal(t, "pro", sub.ProductKey)
	assert.Equal(t, "sub_pb1", sub.ProviderSubscriptionID.String)

	// Replay: same delivery id → 200, no duplicate write (row count stays 1).
	assert.Equal(t, http.StatusOK, deliverBilling(t, s, "msg_pb1", created))
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE org_id='org_pb'`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestPolarWebhookOneShotCancelEmail(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_oc", "org_oc", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM jobs WHERE kind='email.subscription_canceled'")
	})

	deliverBilling(t, s, "msg_oc1", subEvent("subscription.created", "sub_oc", "org_oc", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))

	canceled := subEvent("subscription.canceled", "sub_oc", "org_oc", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour))
	require.Equal(t, http.StatusOK, deliverBilling(t, s, "msg_oc2", canceled))

	// Deliver the same event TWICE more with NEW delivery ids (provider retry
	// semantics): the email must still be sent exactly once.
	deliverBilling(t, s, "msg_oc3", canceled)
	deliverBilling(t, s, "msg_oc4", canceled)

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.subscription_canceled'`).Scan(&n))
	assert.Equal(t, 1, n, "cancellation email must be one-shot across redeliveries")
}

func TestResubscribe(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_rs", "org_rs", "org:admin")

	deliverBilling(t, s, "msg_rs1", subEvent("subscription.created", "sub_rs1", "org_rs", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))
	deliverBilling(t, s, "msg_rs2", subEvent("subscription.canceled", "sub_rs1", "org_rs", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour)))

	// Re-checkout arrives with a NEW provider_subscription_id → overwrites the row.
	code := deliverBilling(t, s, "msg_rs3", subEvent("subscription.created", "sub_rs2", "org_rs", "prod_team", "active", time.Now().Add(30*24*time.Hour)))
	require.Equal(t, http.StatusOK, code)

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE org_id='org_rs'`).Scan(&n))
	assert.Equal(t, 1, n, "exactly one subscription row per org")
	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_rs")
	require.NoError(t, err)
	assert.Equal(t, "sub_rs2", sub.ProviderSubscriptionID.String)
	assert.Equal(t, "team", sub.ProductKey)
	assert.Equal(t, "active", sub.Status)
}

func TestRevokedMapsPayloadStatus(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_rv", "org_rv", "org:admin")

	deliverBilling(t, s, "msg_rv1", subEvent("subscription.created", "sub_rv", "org_rv", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))

	// 'revoked' is an EVENT; the event carries the stored status verbatim.
	code := deliverBilling(t, s, "msg_rv2", subEvent("subscription.revoked", "sub_rv", "org_rv", "prod_pro", "unpaid", time.Now().Add(30*24*time.Hour)))
	require.Equal(t, http.StatusOK, code)
	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_rv")
	require.NoError(t, err)
	assert.Equal(t, "unpaid", sub.Status)
}

func TestUncanceledReactivates(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_uc", "org_uc", "org:admin")

	deliverBilling(t, s, "msg_uc1", subEvent("subscription.created", "sub_uc", "org_uc", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))
	deliverBilling(t, s, "msg_uc2", subEvent("subscription.canceled", "sub_uc", "org_uc", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour)))

	sub, _ := s.q.GetSubscriptionByOrg(ctx, "org_uc")
	assert.True(t, sub.CancelAtPeriodEnd)

	code := deliverBilling(t, s, "msg_uc3", subEvent("subscription.uncanceled", "sub_uc", "org_uc", "prod_pro", "active", time.Now().Add(15*24*time.Hour)))
	require.Equal(t, http.StatusOK, code)

	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_uc")
	require.NoError(t, err)
	assert.False(t, sub.CancelAtPeriodEnd, "uncanceled flips cancel_at_period_end off")
	assert.Equal(t, "active", sub.Status)

	var action string
	require.NoError(t, s.db.QueryRow(ctx, `SELECT action FROM audit_log WHERE org_id='org_uc' ORDER BY id DESC LIMIT 1`).Scan(&action))
	assert.Equal(t, "subscription.reactivated", action)
}

func TestPastDueTransitionEmailsOnce(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_pd", "org_pd", "org:admin")
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM jobs WHERE kind='email.payment_failed'") })

	deliverBilling(t, s, "msg_pd1", subEvent("subscription.created", "sub_pd", "org_pd", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))

	pastDue := subEvent("subscription.updated", "sub_pd", "org_pd", "prod_pro", "past_due", time.Now().Add(30*24*time.Hour))
	deliverBilling(t, s, "msg_pd2", pastDue)
	deliverBilling(t, s, "msg_pd3", pastDue)

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.payment_failed'`).Scan(&n))
	assert.Equal(t, 1, n, "payment-failed email fires only on the transition INTO past_due")

	// Recovery → subscription.active clears the grace (cancel flag stays false).
	deliverBilling(t, s, "msg_pd4", subEvent("subscription.active", "sub_pd", "org_pd", "prod_pro", "active", time.Now().Add(30*24*time.Hour)))
	sub, _ := s.q.GetSubscriptionByOrg(ctx, "org_pd")
	assert.Equal(t, "active", sub.Status)
}

func TestCheckoutHandlerMockClient(t *testing.T) {
	mock := &billing.MockClient{}
	s := billingWebhookServer(t, func(d *Deps) { d.Billing = mock })
	seedMembership(t, s, "user_co", "org_co", "org:admin")

	code, hdr, _ := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"pro"}}, sessionCookie("user_co", "org_co", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "https://checkout.example.test/session", hdr.Get("HX-Redirect"))

	require.Len(t, mock.CheckoutCalls, 1)
	call := mock.CheckoutCalls[0]
	assert.Equal(t, "prod_pro", call.ProductID)
	assert.Equal(t, "org_co", call.CustomerExternalID)
	assert.Equal(t, "http://localhost:18080/app/settings/billing?success=1", call.SuccessURL)
	assert.Equal(t, "org_co", call.Metadata["org_id"])
}

func TestCheckoutUnknownPlan422(t *testing.T) {
	s := billingWebhookServer(t, func(d *Deps) { d.Billing = &billing.MockClient{} })
	seedMembership(t, s, "user_up", "org_up", "org:admin")

	code, _, body := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"free"}}, sessionCookie("user_up", "org_up", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code, "free plan has no product id → 422")
	assert.Contains(t, body, "isn&#39;t available")
}

func TestBillingUnselectedPlanRefusesCheckout(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_nc", "org_nc", "org:admin")

	code, _, body := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"pro"}}, sessionCookie("user_nc", "org_nc", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "isn&#39;t available")
}

func TestEntitledGateInCurrentPlan(t *testing.T) {
	s := billingWebhookServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_eg", "org_eg", "org:admin")

	// Canceled sub PAST period end → plan resolves to free → 4th project 422s.
	_, err := s.q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		Provider: "polar",
		OrgID:    "org_eg", ProviderSubscriptionID: pgtype.Text{String: "sub_eg", Valid: true},
		ProviderCustomerID: "cust_eg", ProductKey: "pro", Status: "canceled",
		CurrentPeriodEnd: pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	for i := range 3 {
		_, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{OrgID: "org_eg", Name: fmt.Sprintf("p%d", i)})
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE org_id='org_eg'") })

	code, _, body := postForm(t, s, "/app/projects", url.Values{"name": {"over"}}, sessionCookie("user_eg", "org_eg", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "plan-limit", "expired sub must not keep paid limits")
}

func TestSettingsBillingPage(t *testing.T) {
	s := billingWebhookServer(t, nil)
	seedMembership(t, s, "user_sb", "org_sb", "org:admin")
	cookie := sessionCookie("user_sb", "org_sb", "org:admin")

	// Free: usage meter + upgrade buttons, no manage button.
	code, _, body := serve(t, s, "GET", "/app/settings/billing", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "usage-meter")
	assert.Contains(t, body, "Upgrade to Pro")
	assert.NotContains(t, body, "manage-subscription")

	// ?success=1 with no subscription row → processing fragment that polls.
	code, _, body = serve(t, s, "GET", "/app/settings/billing?success=1", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Processing your subscription")
	assert.Contains(t, body, "every 2s")

	// Fragment endpoint with a row → the card without polling.
	_, err := s.q.UpsertSubscription(t.Context(), sqlc.UpsertSubscriptionParams{
		Provider: "polar",
		OrgID:    "org_sb", ProviderSubscriptionID: pgtype.Text{String: "sub_sb", Valid: true},
		ProviderCustomerID: "cust_sb", ProductKey: "pro", Status: "active",
		CurrentPeriodEnd: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	code, _, body = serve(t, s, "GET", "/app/settings/billing/fragment", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "plan-badge")
	assert.NotContains(t, body, "every 2s")
}
