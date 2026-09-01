// Package billinglocal provides the zero-account billing adapter used by
// development and test environments. Checkout is an in-app confirmation URL;
// no network or provider credentials are needed.
package billinglocal

import (
  "context"
  "fmt"
  "net/url"
  "sync"

  "github.com/gogogadget/gogogadget/internal/billing"
)

type Client struct { BaseURL string; mu sync.Mutex; confirmed map[string]string }
func New(baseURL string) *Client { return &Client{BaseURL:baseURL,confirmed:make(map[string]string)} }
func (c *Client) CreateCheckout(_ context.Context,p billing.CheckoutParams)(string,error) {
  if c==nil{return "",fmt.Errorf("billing-local: nil client")}; u:=c.BaseURL+"/billing/confirm?product="+url.QueryEscape(p.ProductID)+"&customer="+url.QueryEscape(p.CustomerExternalID); return u,nil
}
func (c *Client) CreatePortalSession(_ context.Context,customer string)(string,error){return c.BaseURL+"/billing/portal?customer="+url.QueryEscape(customer),nil}
func (c *Client) RevokeSubscription(_ context.Context,sub string)error{c.mu.Lock();defer c.mu.Unlock();delete(c.confirmed,sub);return nil}
func (c *Client) IngestUsage(context.Context,string,[]billing.UsageEvent)error{return nil}
func (c *Client) Confirm(product,customer string){c.mu.Lock();defer c.mu.Unlock();c.confirmed[customer]=product}
func (c *Client) Cancel(customer string){c.mu.Lock();defer c.mu.Unlock();delete(c.confirmed,customer)}

// ConfirmedEvent is the same provider-neutral event shape consumed by the
// hosted webhook workflow. Callers persist it through billing.Processor.
func (c *Client) ConfirmedEvent(product,customer,orgID string) billing.SubscriptionEvent { c.Confirm(product,customer); return billing.SubscriptionEvent{Provider:"local",Type:"subscription.active",OrgIDHint:orgID,ProviderCustomerID:customer,ProviderProductID:product,Status:"active"} }
func (c *Client) CanceledEvent(customer,orgID string) billing.SubscriptionEvent { c.Cancel(customer); return billing.SubscriptionEvent{Provider:"local",Type:"subscription.canceled",OrgIDHint:orgID,ProviderCustomerID:customer,Status:"canceled",CancelAtPeriodEnd:true} }
var _ billing.Client=(*Client)(nil)
