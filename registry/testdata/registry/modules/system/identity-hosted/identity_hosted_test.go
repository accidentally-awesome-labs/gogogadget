package identityhosted

import (
	"context"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/identity"
)

func TestExampleIdentityHostedVerifies(t *testing.T) {
	claims, err := (verifier{}).Verify(context.Background(), "hosted:user_1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Provider != Provider || claims.UserSubject != "user_1" {
		t.Fatalf("Verify = %+v", claims)
	}
	if _, err := (verifier{}).Verify(context.Background(), "fixture:user_1"); err != identity.ErrInvalidToken {
		t.Fatalf("another adapter's token error = %v, want ErrInvalidToken", err)
	}
}

func TestExampleIdentityHostedOwnsItsHeaderFamily(t *testing.T) {
	headers := http.Header{}
	headers.Set("hosted-delivery-id", "evt_1")
	event, err := (webhook{}).Verify(context.Background(),
		[]byte(`{"type":"user.created","data":{"id":"user_1"}}`), headers)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if event.ID != "evt_1" || event.Provider != Provider {
		t.Fatalf("Verify = %+v", event)
	}
	// The local fixture's header family must not be honoured here: a header
	// name is part of an adapter's contract, not the seam's.
	other := http.Header{}
	other.Set("id", "evt_1")
	if _, err := (webhook{}).Verify(context.Background(), []byte(`{}`), other); err == nil {
		t.Fatal("another adapter's delivery header was accepted")
	}
}

func TestExampleIdentityHostedNavigates(t *testing.T) {
	n := navigator{appURL: "https://accounts.example.invalid"}
	if got := n.LoginURL("/app"); got != "https://accounts.example.invalid/sign-in?redirect_url=/app" {
		t.Fatalf("LoginURL = %q", got)
	}
	if got := n.AccountURL(); got != "https://accounts.example.invalid/account" {
		t.Fatalf("AccountURL = %q", got)
	}
}
