package web

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A provider that refuses is not a server error and not a redirect: both
// checkout entry points re-render the billing page at 422 with a sentence a
// visitor can act on. Neither branch had a test, because MockClient had no
// error hook for these two methods — the seam's double and the handler suite
// were missing the same thing, so the gap was invisible from both sides.
func TestBillingCheckoutProviderFailureRenders422(t *testing.T) {
	client := &billing.MockClient{CheckoutErr: errors.New("provider refused")}
	s := billingWebhookServer(t, func(d *Deps) { d.Billing = client })
	seedMembership(t, s, "user_ckfail", "org_ckfail", "org:admin")

	code, hdr, body := postForm(t, s, "/app/billing/checkout",
		url.Values{"plan": {"pro"}}, sessionCookie("user_ckfail", "org_ckfail", "org:admin"))

	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Empty(t, hdr.Get("HX-Redirect"), "a failed checkout must not send the visitor to a provider page")
	assert.Empty(t, hdr.Get("Location"))
	assert.Contains(t, body, "start checkout", "the page must say what failed")
	assert.NotContains(t, body, "provider refused", "the provider's own error text is logged, never rendered")
	require.Len(t, client.CheckoutCalls, 1, "the checkout was attempted; the provider refused it")
}

func TestBillingPortalProviderFailureRenders422(t *testing.T) {
	s := billingWebhookServer(t, func(d *Deps) {
		d.Billing = &billing.MockClient{PortalErr: errors.New("provider refused")}
	})
	seedMembership(t, s, "user_ptfail", "org_ptfail", "org:admin")

	code, hdr, body := postForm(t, s, "/app/billing/portal",
		url.Values{}, sessionCookie("user_ptfail", "org_ptfail", "org:admin"))

	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Empty(t, hdr.Get("HX-Redirect"))
	assert.Empty(t, hdr.Get("Location"))
	assert.Contains(t, body, "billing portal")
	assert.NotContains(t, body, "provider refused")
}
