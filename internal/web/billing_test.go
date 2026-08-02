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
	svix "github.com/svix/svix-webhooks/go"
)

// signStandard emits webhook-* headers (Polar's Standard Webhooks family).
// The signing scheme is identical to svix's, so the svix lib computes the
// signature; only the header names differ.
func signStandard(t *testing.T, secret, msgID string, payload []byte) http.Header {
	t.Helper()
	wh, err := svix.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := wh.Sign(msgID, time.Now(), payload)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("webhook-id", msgID)
	h.Set("webhook-timestamp", fmt.Sprint(time.Now().Unix()))
	h.Set("webhook-signature", sig)
	return h
}

func subPayload(eventType, subID, orgID, productID, status string, periodEnd time.Time) []byte {
	return []byte(fmt.Sprintf(`{
	  "type": %q,
	  "data": {
	    "id": %q, "status": %q, "product_id": %q, "customer_id": "cust_1",
	    "current_period_end": %q, "cancel_at_period_end": false,
	    "customer": {"external_id": %q}, "metadata": {"clerk_org_id": %q}
	  }
	}`, eventType, subID, status, productID, periodEnd.Format(time.RFC3339), orgID, orgID))
}

func polarServer(t *testing.T, mutate func(*Deps)) *Server {
	t.Helper()
	return integrationServer(t, func(d *Deps) {
		d.Config.PolarAccessToken = "polar_test"
		d.Config.PolarWebhookSecret = testWebhookSecret
		d.Config.PolarServer = "sandbox"
		billing.SetPolarProductIDs("prod_pro", "prod_team")
		if mutate != nil {
			mutate(d)
		}
	})
}

func TestPolarWebhookReplay(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_pb", "org_pb", "org:admin")
	payload := subPayload("subscription.created", "sub_pb1", "org_pb", "prod_pro", "active", time.Now().Add(30*24*time.Hour))

	code, _, _ := serve(t, s, "POST", "/webhooks/polar", payload, signStandard(t, testWebhookSecret, "msg_pb1", payload))
	assert.Equal(t, http.StatusOK, code)

	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_pb")
	require.NoError(t, err)
	assert.Equal(t, "active", sub.Status)
	assert.Equal(t, "pro", sub.ProductKey)
	assert.Equal(t, "sub_pb1", sub.PolarSubscriptionID.String)

	// Replay: 200, no duplicate write (row count stays 1).
	code, _, _ = serve(t, s, "POST", "/webhooks/polar", payload, signStandard(t, testWebhookSecret, "msg_pb1", payload))
	assert.Equal(t, http.StatusOK, code)
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE clerk_org_id='org_pb'`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestPolarWebhookOneShotCancelEmail(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_oc", "org_oc", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM jobs WHERE kind='email.subscription_canceled'")
	})

	created := subPayload("subscription.created", "sub_oc", "org_oc", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", created, signStandard(t, testWebhookSecret, "msg_oc1", created))

	canceled := subPayload("subscription.canceled", "sub_oc", "org_oc", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour))
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", canceled, signStandard(t, testWebhookSecret, "msg_oc2", canceled))
	require.Equal(t, http.StatusOK, code)

	// Deliver the same event TWICE more with NEW message ids (provider retry
	// semantics): the email must still be sent exactly once.
	serve(t, s, "POST", "/webhooks/polar", canceled, signStandard(t, testWebhookSecret, "msg_oc3", canceled))
	serve(t, s, "POST", "/webhooks/polar", canceled, signStandard(t, testWebhookSecret, "msg_oc4", canceled))

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.subscription_canceled'`).Scan(&n))
	assert.Equal(t, 1, n, "cancellation email must be one-shot across redeliveries")
}

func TestResubscribe(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_rs", "org_rs", "org:admin")

	created := subPayload("subscription.created", "sub_rs1", "org_rs", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", created, signStandard(t, testWebhookSecret, "msg_rs1", created))
	canceled := subPayload("subscription.canceled", "sub_rs1", "org_rs", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", canceled, signStandard(t, testWebhookSecret, "msg_rs2", canceled))

	// Re-checkout arrives with a NEW polar_subscription_id → overwrites the row.
	resub := subPayload("subscription.created", "sub_rs2", "org_rs", "prod_team", "active", time.Now().Add(30*24*time.Hour))
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", resub, signStandard(t, testWebhookSecret, "msg_rs3", resub))
	require.Equal(t, http.StatusOK, code)

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE clerk_org_id='org_rs'`).Scan(&n))
	assert.Equal(t, 1, n, "exactly one subscription row per org")
	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_rs")
	require.NoError(t, err)
	assert.Equal(t, "sub_rs2", sub.PolarSubscriptionID.String)
	assert.Equal(t, "team", sub.ProductKey)
	assert.Equal(t, "active", sub.Status)
}

func TestRevokedMapsPayloadStatus(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_rv", "org_rv", "org:admin")

	created := subPayload("subscription.created", "sub_rv", "org_rv", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", created, signStandard(t, testWebhookSecret, "msg_rv1", created))

	// 'revoked' is an EVENT; the payload carries the stored status verbatim.
	revoked := subPayload("subscription.revoked", "sub_rv", "org_rv", "prod_pro", "unpaid", time.Now().Add(30*24*time.Hour))
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", revoked, signStandard(t, testWebhookSecret, "msg_rv2", revoked))
	require.Equal(t, http.StatusOK, code)
	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_rv")
	require.NoError(t, err)
	assert.Equal(t, "unpaid", sub.Status)
}

func TestUncanceledReactivates(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_uc", "org_uc", "org:admin")

	created := subPayload("subscription.created", "sub_uc", "org_uc", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", created, signStandard(t, testWebhookSecret, "msg_uc1", created))
	canceled := subPayload("subscription.canceled", "sub_uc", "org_uc", "prod_pro", "canceled", time.Now().Add(15*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", canceled, signStandard(t, testWebhookSecret, "msg_uc2", canceled))

	sub, _ := s.q.GetSubscriptionByOrg(ctx, "org_uc")
	assert.True(t, sub.CancelAtPeriodEnd)

	uncanceled := subPayload("subscription.uncanceled", "sub_uc", "org_uc", "prod_pro", "active", time.Now().Add(15*24*time.Hour))
	code, _, _ := serve(t, s, "POST", "/webhooks/polar", uncanceled, signStandard(t, testWebhookSecret, "msg_uc3", uncanceled))
	require.Equal(t, http.StatusOK, code)

	sub, err := s.q.GetSubscriptionByOrg(ctx, "org_uc")
	require.NoError(t, err)
	assert.False(t, sub.CancelAtPeriodEnd, "uncanceled flips cancel_at_period_end off")
	assert.Equal(t, "active", sub.Status)

	var action string
	require.NoError(t, s.db.QueryRow(ctx, `SELECT action FROM audit_log WHERE clerk_org_id='org_uc' ORDER BY id DESC LIMIT 1`).Scan(&action))
	assert.Equal(t, "subscription.reactivated", action)
}

func TestPastDueTransitionEmailsOnce(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_pd", "org_pd", "org:admin")
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM jobs WHERE kind='email.payment_failed'") })

	created := subPayload("subscription.created", "sub_pd", "org_pd", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", created, signStandard(t, testWebhookSecret, "msg_pd1", created))

	pastDue := subPayload("subscription.updated", "sub_pd", "org_pd", "prod_pro", "past_due", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", pastDue, signStandard(t, testWebhookSecret, "msg_pd2", pastDue))
	serve(t, s, "POST", "/webhooks/polar", pastDue, signStandard(t, testWebhookSecret, "msg_pd3", pastDue))

	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='email.payment_failed'`).Scan(&n))
	assert.Equal(t, 1, n, "payment-failed email fires only on the transition INTO past_due")

	// Recovery → subscription.active clears the grace (cancel flag stays false).
	active := subPayload("subscription.active", "sub_pd", "org_pd", "prod_pro", "active", time.Now().Add(30*24*time.Hour))
	serve(t, s, "POST", "/webhooks/polar", active, signStandard(t, testWebhookSecret, "msg_pd4", active))
	sub, _ := s.q.GetSubscriptionByOrg(ctx, "org_pd")
	assert.Equal(t, "active", sub.Status)
}

func TestCheckoutHandlerMockClient(t *testing.T) {
	mock := &billing.MockClient{}
	s := polarServer(t, func(d *Deps) { d.Billing = mock })
	seedMembership(t, s, "user_co", "org_co", "org:admin")

	code, hdr, _ := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"pro"}}, sessionCookie("user_co", "org_co", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "https://checkout.example.test/session", hdr.Get("HX-Redirect"))

	require.Len(t, mock.CheckoutCalls, 1)
	call := mock.CheckoutCalls[0]
	assert.Equal(t, "prod_pro", call.ProductID)
	assert.Equal(t, "org_co", call.CustomerExternalID)
	assert.Equal(t, "http://localhost:18080/app/settings/billing?success=1", call.SuccessURL)
	assert.Equal(t, "org_co", call.Metadata["clerk_org_id"])
}

func TestCheckoutUnknownPlan422(t *testing.T) {
	s := polarServer(t, func(d *Deps) { d.Billing = &billing.MockClient{} })
	seedMembership(t, s, "user_up", "org_up", "org:admin")

	code, _, body := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"free"}}, sessionCookie("user_up", "org_up", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code, "free plan has no product id → 422")
	assert.Contains(t, body, "isn&#39;t available")
}

func TestBillingNotConfigured503(t *testing.T) {
	s := integrationServer(t, nil) // no Polar keys, no client
	seedMembership(t, s, "user_nc", "org_nc", "org:admin")

	code, _, body := postForm(t, s, "/app/billing/checkout", url.Values{"plan": {"pro"}}, sessionCookie("user_nc", "org_nc", "org:admin"))
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "Billing not configured")
	assert.Contains(t, body, "/docs/billing")
}

func TestEntitledGateInCurrentPlan(t *testing.T) {
	s := polarServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_eg", "org_eg", "org:admin")

	// Canceled sub PAST period end → plan resolves to free → 4th project 422s.
	_, err := s.q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_eg", PolarSubscriptionID: pgtype.Text{String: "sub_eg", Valid: true},
		PolarCustomerID: "cust_eg", ProductKey: "pro", Status: "canceled",
		CurrentPeriodEnd: pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	for i := range 3 {
		_, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_eg", Name: fmt.Sprintf("p%d", i)})
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id='org_eg'") })

	code, _, body := postForm(t, s, "/app/projects", url.Values{"name": {"over"}}, sessionCookie("user_eg", "org_eg", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "plan-limit", "expired sub must not keep paid limits")
}

func TestSettingsBillingPage(t *testing.T) {
	s := polarServer(t, nil)
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
		ClerkOrgID: "org_sb", PolarSubscriptionID: pgtype.Text{String: "sub_sb", Valid: true},
		PolarCustomerID: "cust_sb", ProductKey: "pro", Status: "active",
		CurrentPeriodEnd: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	code, _, body = serve(t, s, "GET", "/app/settings/billing/fragment", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "plan-badge")
	assert.NotContains(t, body, "every 2s")
}
