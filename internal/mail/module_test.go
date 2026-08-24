package mail

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// NewModule must fall back to the dev sender when Resend is unconfigured, and
// must select Resend the moment an API key exists. The fallback is a shipped
// contract: a fresh clone with no accounts must still deliver mail to disk.
func TestNewModuleSelectsSenderByConfiguration(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	unconfigured, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if _, ok := unconfigured.Sender.(*DevSender); !ok {
		t.Fatalf("unconfigured sender = %T, want *DevSender", unconfigured.Sender)
	}

	configured, err := NewModule(context.Background(), h, Deps{Config: &config.Config{
		ResendAPIKey: "re_test",
		EmailFrom:    "hello@example.com",
	}})
	if err != nil {
		t.Fatalf("NewModule(configured): %v", err)
	}
	if _, ok := configured.Sender.(*ResendSender); !ok {
		t.Fatalf("configured sender = %T, want *ResendSender", configured.Sender)
	}
}

// A nil Config is a wiring bug, not a runtime condition: boot must fail loudly
// rather than construct a module against zero values.
func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
