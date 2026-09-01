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
}

var _ billing.Client = (*billinglocal.Client)(nil)
