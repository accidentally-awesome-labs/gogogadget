package identityhosted

import (
	"context"
	"errors"
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
	got, err := n.LoginURL("/app?x=a b")
	if err != nil {
		t.Fatalf("LoginURL: %v", err)
	}
	if got != "https://accounts.example.invalid/sign-in?redirect_url=%2Fapp%3Fx%3Da+b" {
		t.Fatalf("LoginURL = %q", got)
	}
	if got, err = n.AccountURL(""); err != nil || got != "https://accounts.example.invalid/account" {
		t.Fatalf("AccountURL = %q, %v", got, err)
	}
	if got, err = n.CreateOrganizationURL(""); err != nil || got != "https://accounts.example.invalid/create-organization" {
		t.Fatalf("CreateOrganizationURL = %q, %v", got, err)
	}
	if _, err = (navigator{}).LogoutURL(""); !errors.Is(err, identity.ErrNoDestination) {
		t.Fatalf("an unconfigured base must refuse, got %v", err)
	}
}
