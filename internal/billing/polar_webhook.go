package billing

import (
  "context"
  "encoding/json"
  "net/http"
  "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

type PolarWebhook struct { Secret string }
func (w PolarWebhook) Verify(_ context.Context,payload []byte,headers http.Header)(SubscriptionEvent,error){
  wh,err:=standardwebhooks.NewWebhookRaw([]byte(w.Secret)); if err!=nil{return SubscriptionEvent{},err}; if err=wh.Verify(payload,headers);err!=nil{return SubscriptionEvent{},err}
  var envelope struct{Type string `json:"type"`; Data SubscriptionPayload `json:"data"`}; if err=json.Unmarshal(payload,&envelope);err!=nil{return SubscriptionEvent{},err}
  id:=headers.Get("webhook-id"); org:=envelope.Data.OrgID()
  return SubscriptionEvent{ID:id,Provider:"polar",Type:envelope.Type,OrgIDHint:org,ProviderSubscriptionID:envelope.Data.ID,ProviderCustomerID:envelope.Data.CustomerID,ProviderProductID:envelope.Data.ProductID,Status:envelope.Data.Status,CurrentPeriodEnd:envelope.Data.CurrentPeriodEnd,CancelAtPeriodEnd:envelope.Data.CancelAtPeriodEnd},nil
}
