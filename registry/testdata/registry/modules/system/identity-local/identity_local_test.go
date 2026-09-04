package identitylocal

import (
	"context"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/identity"
)

func TestExampleIdentityLocalVerifies(t *testing.T) {
	claims, err := (verifier{}).Verify(context.Background(), "fixture:user_1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Provider != Provider || claims.UserSubject != "user_1" {
		t.Fatalf("Verify = %+v", claims)
	}
	if _, err := (verifier{}).Verify(context.Background(), "nonsense"); err != identity.ErrInvalidToken {
		t.Fatalf("unverifiable token error = %v, want ErrInvalidToken", err)
	}
}

func TestExampleIdentityLocalParsesItsOwnEnvelope(t *testing.T) {
	headers := http.Header{}
	headers.Set("id", "evt_1")
	event, err := (webhook{}).Verify(context.Background(),
		[]byte(`{"type":"user.created","data":{"id":"user_1"}}`), headers)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if event.ID != "evt_1" || event.Type != "user.created" || event.User == nil || event.User.Subject != "user_1" {
		t.Fatalf("Verify = %+v", event)
	}
	if _, err := (webhook{}).Verify(context.Background(), []byte(`{}`), http.Header{}); err == nil {
		t.Fatal("a delivery with no id was accepted")
	}
}
