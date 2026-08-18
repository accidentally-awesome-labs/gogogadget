package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time seam conformance for every implementation.
var (
	_ Client = (*PolarClient)(nil)
	_ Client = (*MockClient)(nil)
)

// runClientContract is the Client seam contract: every implementation must
// pass it, so a provider swap can't drift from the behavior handlers rely on.
//
// factory returns a healthy client. errFactory returns a client whose next
// call to the named method fails with a provider error, or nil when the
// implementation cannot inject a failure for that method — the error case is
// then skipped loudly instead of silently dropped.
func runClientContract(t *testing.T, factory func(t *testing.T) Client, errFactory func(t *testing.T, method string) Client) {
	t.Helper()

	methods := []struct {
		name string
		call func(t *testing.T, ctx context.Context, c Client) error
	}{
		{"CreateCheckout", func(t *testing.T, ctx context.Context, c Client) error {
			url, err := c.CreateCheckout(ctx, CheckoutParams{
				ProductID:          "prod_contract",
				SuccessURL:         "https://app.test/billing/return",
				CustomerExternalID: "org_contract",
				Metadata:           map[string]string{"org_id": "org_contract"},
			})
			if err == nil {
				assert.NotEmpty(t, url, "success must return a checkout URL")
			}
			return err
		}},
		{"CreatePortalSession", func(t *testing.T, ctx context.Context, c Client) error {
			url, err := c.CreatePortalSession(ctx, "org_contract")
			if err == nil {
				assert.NotEmpty(t, url, "success must return a portal URL")
			}
			return err
		}},
		{"RevokeSubscription", func(t *testing.T, ctx context.Context, c Client) error {
			return c.RevokeSubscription(ctx, "sub_contract")
		}},
		{"IngestUsage", func(t *testing.T, ctx context.Context, c Client) error {
			return c.IngestUsage(ctx, "org_contract", []UsageEvent{
				{Name: "ai_tokens", ExternalID: "ue-contract", Value: 150, Metadata: map[string]any{"model": "gpt"}},
			})
		}},
	}

	for _, m := range methods {
		t.Run(m.name+"/success", func(t *testing.T) {
			require.NoError(t, m.call(t, context.Background(), factory(t)))
		})
		t.Run(m.name+"/provider_error", func(t *testing.T) {
			if errFactory == nil {
				t.Skip("implementation cannot inject provider errors")
			}
			c := errFactory(t, m.name)
			if c == nil {
				t.Skipf("implementation cannot inject a provider error for %s", m.name)
			}
			require.Error(t, m.call(t, context.Background(), c))
		})
	}
}

// TestPolarClientContract runs the seam contract against the real HTTP
// client pointed at an httptest fake: a healthy fake for success paths, a
// 500-ing fake for provider-error paths.
func TestPolarClientContract(t *testing.T) {
	runClientContract(t,
		func(t *testing.T) Client {
			var cap capturedRequest
			// One body satisfying both URL-returning methods.
			return newPolarFake(t, http.StatusOK,
				`{"url":"https://checkout.polar.test/c/x","customer_portal_url":"https://portal.polar.test/x"}`, &cap)
		},
		func(t *testing.T, _ string) Client {
			var cap capturedRequest
			return newPolarFake(t, http.StatusInternalServerError, `{"detail":"boom"}`, &cap)
		})
}

// TestMockClientContract runs the same contract against the test double so
// the mock can't drift from the real client's behavior. MockClient has error
// hooks only for RevokeSubscription/IngestUsage (RevokeErr/IngestErr fields);
// CreateCheckout/CreatePortalSession cannot fail, so those provider-error
// cases skip — a documented gap, with happy paths still enforced.
func TestMockClientContract(t *testing.T) {
	errBoom := errors.New("contract boom")
	runClientContract(t,
		func(t *testing.T) Client { return &MockClient{} },
		func(t *testing.T, method string) Client {
			switch method {
			case "RevokeSubscription":
				return &MockClient{RevokeErr: errBoom}
			case "IngestUsage":
				return &MockClient{IngestErr: errBoom}
			default:
				return nil // no error hook for this method
			}
		})
}
