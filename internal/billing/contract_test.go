// The seam's own conformance run. It is an external test package because the
// shared contract table imports the seam: an in-package test would be an
// import cycle.
package billing_test

import (
	"errors"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	billingcontract "github.com/gogogadget/gogogadget/internal/billing/contract"
)

// TestMockClientContract runs the shared contract against the test double so
// the mock can't drift from a real provider client's behavior. MockClient has
// error hooks only for RevokeSubscription/IngestUsage (RevokeErr/IngestErr
// fields); CreateCheckout/CreatePortalSession cannot fail, so those
// provider-error cases skip — a documented gap, with happy paths still
// enforced.
func TestMockClientContract(t *testing.T) {
	errBoom := errors.New("contract boom")
	billingcontract.RunClient(t,
		func(t *testing.T) billing.Client { return &billing.MockClient{} },
		func(t *testing.T, method string) billing.Client {
			switch method {
			case "RevokeSubscription":
				return &billing.MockClient{RevokeErr: errBoom}
			case "IngestUsage":
				return &billing.MockClient{IngestErr: errBoom}
			default:
				return nil // no error hook for this method
			}
		})
}
