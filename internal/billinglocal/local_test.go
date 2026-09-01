package billinglocal

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/billing"
	"testing"
)

func TestLocalBillingContract(t *testing.T) {
	c := New("http://app.test")
	u, err := c.CreateCheckout(context.Background(), billing.CheckoutParams{ProductID: "pro", CustomerExternalID: "org_1"})
	if err != nil || u != "http://app.test/app/billing/confirm?product=pro&customer=org_1" {
		t.Fatalf("checkout=%q err=%v", u, err)
	}
	e := c.ConfirmedEvent("pro", "cust_1", "org_1")
	if e.Provider != "local" || e.Type != "subscription.active" || e.OrgIDHint != "org_1" {
		t.Fatalf("event=%+v", e)
	}
	e = c.CanceledEvent("cust_1", "org_1")
	if e.Type != "subscription.canceled" || !e.CancelAtPeriodEnd {
		t.Fatalf("cancel=%+v", e)
	}
}

func TestLocalBillingSubscriptionIDsAreUniqueAcrossOrganizations(t *testing.T) {
	c := New("http://app.test")
	first := c.ConfirmedEvent("pro", "org_1", "org_1")
	second := c.ConfirmedEvent("pro", "org_2", "org_2")
	if first.ProviderSubscriptionID == "" || second.ProviderSubscriptionID == "" || first.ProviderSubscriptionID == second.ProviderSubscriptionID {
		t.Fatalf("subscription IDs are not unique: %q and %q", first.ProviderSubscriptionID, second.ProviderSubscriptionID)
	}
}
