package billing

import (
  "context"
  "net/http"
  "time"
)

type BillingWebhook interface { Verify(context.Context, []byte, http.Header) (SubscriptionEvent,error) }
type SubscriptionEvent struct {
  ID, Provider, Type, OrgIDHint string
  ProviderSubscriptionID, ProviderCustomerID, ProviderProductID, Status string
  CurrentPeriodEnd time.Time
  CancelAtPeriodEnd bool
}
