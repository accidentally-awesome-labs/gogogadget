package identity

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// The three identity ports move together, and which triple you get is decided
// by exactly one precedence rule: dev bypass wins, then Clerk, then nothing.
// "Nothing" is a real shipped state — /app answers 503 instead of trusting a
// synthetic user in production.
func TestNewModuleSelectsPortsByPrecedence(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	bypass, err := NewModule(context.Background(), h, Deps{Config: &config.Config{DevAuthBypass: true}})
	if err != nil {
		t.Fatalf("NewModule(bypass): %v", err)
	}
	if _, ok := bypass.Verifier.(FakeVerifier); !ok {
		t.Fatalf("bypass verifier = %T, want FakeVerifier", bypass.Verifier)
	}
	if _, ok := bypass.Fetcher.(DevUserFetcher); !ok {
		t.Fatalf("bypass fetcher = %T, want DevUserFetcher", bypass.Fetcher)
	}
	if _, ok := bypass.Deleter.(DevDeleter); !ok {
		t.Fatalf("bypass deleter = %T, want DevDeleter", bypass.Deleter)
	}

	// Bypass outranks a configured Clerk: the e2e suite sets both.
	both, err := NewModule(context.Background(), h, Deps{Config: &config.Config{
		DevAuthBypass:  true,
		ClerkSecretKey: "sk_test",
	}})
	if err != nil {
		t.Fatalf("NewModule(both): %v", err)
	}
	if _, ok := both.Verifier.(FakeVerifier); !ok {
		t.Fatalf("bypass+clerk verifier = %T, want FakeVerifier", both.Verifier)
	}

	clerk, err := NewModule(context.Background(), h, Deps{Config: &config.Config{ClerkSecretKey: "sk_test"}})
	if err != nil {
		t.Fatalf("NewModule(clerk): %v", err)
	}
	if _, ok := clerk.Verifier.(*ClerkVerifier); !ok {
		t.Fatalf("clerk verifier = %T, want *ClerkVerifier", clerk.Verifier)
	}

	none, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(none): %v", err)
	}
	if none.Verifier != nil || none.Fetcher != nil || none.Deleter != nil {
		t.Fatalf("unconfigured ports = %T/%T/%T, want all nil", none.Verifier, none.Fetcher, none.Deleter)
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
