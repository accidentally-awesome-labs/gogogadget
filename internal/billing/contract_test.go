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
// the mock can't drift from a real provider client's behavior. MockClient now
// has an error hook for every method (CheckoutErr, PortalErr, RevokeErr,
// IngestErr), so the omission set is EMPTY and the whole provider-error half
// of the table runs. The two that used to be omitted were the two production
// 422 branches in internal/web/workflow_billing_checkout.go with no test at
// all — the gap sat exactly where the injection hook was missing.
func TestMockClientContract(t *testing.T) {
	errBoom := errors.New("contract boom")
	billingcontract.RunClient(t,
		func(t *testing.T) billing.Client { return &billing.MockClient{} },
		func(t *testing.T, method string) billing.Client {
			switch method {
			case "CreateCheckout":
				return &billing.MockClient{CheckoutErr: errBoom}
			case "CreatePortalSession":
				return &billing.MockClient{PortalErr: errBoom}
			case "RevokeSubscription":
				return &billing.MockClient{RevokeErr: errBoom}
			case "IngestUsage":
				return &billing.MockClient{IngestErr: errBoom}
			default:
				return nil // no error hook for this method
			}
		})
}
