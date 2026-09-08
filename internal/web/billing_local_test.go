package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/stretchr/testify/require"
)

func TestLocalBillingConfirmCancelReactivates(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Billing = billinglocal.New(d.Config.AppURL)
		d.BillingCatalog = billinglocal.LocalPlanCatalog()
	})
	seedMembership(t, s, "user_local_bill", "org_local_bill", "org:admin")
	cookie := sessionCookie("user_local_bill", "org_local_bill", "org:admin")

	checkout := func(id string) {
		token, csrfCookies := csrfFor(t, s)
		form := url.Values{"product": {"pro"}, "customer": {"org_local_bill"}, "checkout": {id}, "csrf_token": {token}}
		h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "X-CSRF-Token": {token}}
		code, _, _ := serve(t, s, http.MethodPost, "/app/billing/confirm", []byte(form.Encode()), h, append(csrfCookies, cookie)...)
		require.Equal(t, http.StatusSeeOther, code)
	}
	cancel := func() {
		token, csrfCookies := csrfFor(t, s)
		form := url.Values{"customer": {"org_local_bill"}, "csrf_token": {token}}
		h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "X-CSRF-Token": {token}}
		code, _, _ := serve(t, s, http.MethodPost, "/app/billing/cancel", []byte(form.Encode()), h, append(csrfCookies, cookie)...)
		require.Equal(t, http.StatusSeeOther, code)
	}

	checkout("checkout-one")
	sub, err := s.q.GetSubscriptionByOrg(t.Context(), "org_local_bill")
	require.NoError(t, err)
	require.Equal(t, "active", sub.Status)
	require.Equal(t, "pro", sub.ProductKey)

	cancel()
	sub, err = s.q.GetSubscriptionByOrg(t.Context(), "org_local_bill")
	require.NoError(t, err)
	require.Equal(t, "canceled", sub.Status)

	checkout("checkout-two")
	sub, err = s.q.GetSubscriptionByOrg(t.Context(), "org_local_bill")
	require.NoError(t, err)
	require.Equal(t, "active", sub.Status)
	cancel()
	sub, err = s.q.GetSubscriptionByOrg(t.Context(), "org_local_bill")
	require.NoError(t, err)
	require.Equal(t, "canceled", sub.Status)

}

// Both local billing POSTs used to declare RoutePolicy.Idempotent, which is the
// /api transport's Idempotency-Key contract and is applied only under the
// api-read and api-write scopes — so on a ScopeApp route it documented a retry
// contract nothing enforced, and the validator now refuses the declaration
// outright. The retry safety these routes actually have is a different and
// stronger property, and this is where it is stated: processLocalBillingEvent
// inserts a SERVER-DERIVED id into the webhook_events ledger, the same ledger
// the hosted webhook receivers dedupe on, so a repeat acts once with no client
// header at all. A no-script <form> cannot send a header, which is why the
// middleware was never the right answer here.
//
// updated_at is the observable: the processor's UPDATE always moves it, so an
// unmoved timestamp is proof the second request never reached the workflow.
func TestLocalBillingConfirmAndCancelActOnceOnARepeat(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Billing = billinglocal.New(d.Config.AppURL)
		d.BillingCatalog = billinglocal.LocalPlanCatalog()
	})
	seedMembership(t, s, "user_local_retry", "org_local_retry", "org:admin")
	cookie := sessionCookie("user_local_retry", "org_local_retry", "org:admin")

	post := func(pattern string, form url.Values) {
		t.Helper()
		token, csrfCookies := csrfFor(t, s)
		form.Set("csrf_token", token)
		h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "X-CSRF-Token": {token}}
		code, _, _ := serve(t, s, http.MethodPost, pattern, []byte(form.Encode()), h, append(csrfCookies, cookie)...)
		require.Equal(t, http.StatusSeeOther, code)
	}
	confirm := url.Values{"product": {"pro"}, "customer": {"org_local_retry"}, "checkout": {"checkout-retry"}}
	cancel := url.Values{"customer": {"org_local_retry"}}

	post("/app/billing/confirm", confirm)
	first, err := s.q.GetSubscriptionByOrg(t.Context(), "org_local_retry")
	require.NoError(t, err)
	require.Equal(t, "active", first.Status)

	post("/app/billing/confirm", confirm)
	again, err := s.q.GetSubscriptionByOrg(t.Context(), "org_local_retry")
	require.NoError(t, err)
	require.Equal(t, "active", again.Status)
	require.Equal(t, first.UpdatedAt.Time, again.UpdatedAt.Time,
		"the repeated confirm reached the subscription workflow, so the ledger did not deduplicate it")

	post("/app/billing/cancel", cancel)
	canceled, err := s.q.GetSubscriptionByOrg(t.Context(), "org_local_retry")
	require.NoError(t, err)
	require.Equal(t, "canceled", canceled.Status)

	post("/app/billing/cancel", cancel)
	stillCanceled, err := s.q.GetSubscriptionByOrg(t.Context(), "org_local_retry")
	require.NoError(t, err)
	require.Equal(t, "canceled", stillCanceled.Status)
	require.Equal(t, canceled.UpdatedAt.Time, stillCanceled.UpdatedAt.Time,
		"the repeated cancel reached the subscription workflow, so the ledger did not deduplicate it")
}

var _ billing.Client = (*billinglocal.Client)(nil)
