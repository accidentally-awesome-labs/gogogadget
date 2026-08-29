package billing

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Billing has no local stand-in: unconfigured must yield a nil Client so
// billing routes answer 503 rather than charging against a fake provider.
func TestNewModuleLeavesClientNilWhenUnconfigured(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if m.Client != nil {
		t.Fatalf("unconfigured client = %T, want nil", m.Client)
	}

	configured, err := NewModule(context.Background(), h, Deps{Config: &config.Config{
		PolarAccessToken: "polar_test",
		PolarServer:      "sandbox",
	}})
	if err != nil {
		t.Fatalf("NewModule(configured): %v", err)
	}
	if configured.Client == nil {
		t.Fatal("configured client = nil, want a client")
	}
}

// Product IDs are package-level billing truth; the module constructor is the
// one place that installs them, so plan lookup works after boot.
func TestNewModuleInstallsProductIDs(t *testing.T) {
	pro, team := PlanByKey("pro").PolarProductID, PlanByKey("team").PolarProductID
	t.Cleanup(func() { SetPolarProductIDs(pro, team) })

	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{Config: &config.Config{
		PolarProductPro:  "prod_pro_module",
		PolarProductTeam: "prod_team_module",
	}}); err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if got := PlanByKey("pro").PolarProductID; got != "prod_pro_module" {
		t.Fatalf("pro product id = %q, want %q", got, "prod_pro_module")
	}
	if got := PlanByKey("team").PolarProductID; got != "prod_team_module" {
		t.Fatalf("team product id = %q, want %q", got, "prod_team_module")
	}
}
func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
