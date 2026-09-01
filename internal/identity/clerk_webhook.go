package identity

import (
  "errors"
  "net/http"
  svix "github.com/svix/svix-webhooks/go"
)

// VerifyClerkWebhook is adapter-owned signature verification. Generic web
// handlers receive only the verified provider-neutral event payload.
func VerifyClerkWebhook(secret string, payload []byte, headers http.Header) error {
  if secret=="" { return errors.New("identity: webhook secret is required") }
  wh,err:=svix.NewWebhook(secret); if err!=nil{return err}; return wh.Verify(payload,headers)
}
