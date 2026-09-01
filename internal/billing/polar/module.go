// Package polar implements the hosted Polar billing adapter boundary.
package polar

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
)

type Deps struct{ Config *config.Config }
type Module struct {
	Client  billing.Client
	Catalog billing.PlanCatalog
	Webhook billing.BillingWebhook
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("billing polar: config dependency is required")
	}
	if d.Config.PolarAccessToken == "" {
		return nil, fmt.Errorf("billing polar: POLAR_ACCESS_TOKEN is required")
	}
	if d.Config.PolarWebhookSecret == "" {
		return nil, fmt.Errorf("billing polar: POLAR_WEBHOOK_SECRET is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plans := make([]billing.Plan, 0, 3)
	for _, p := range billing.DefaultPlanCatalog().All() {
		switch p.Key {
		case "pro":
			p.ProviderProductID = d.Config.PolarProductPro
		case "team":
			p.ProviderProductID = d.Config.PolarProductTeam
		}
		plans = append(plans, p)
	}
	catalog, err := billing.NewPlanCatalog(plans)
	if err != nil {
		return nil, err
	}
	return &Module{Client: billing.NewPolarClient(d.Config.PolarAccessToken, d.Config.PolarServer), Catalog: catalog, Webhook: billing.PolarWebhook{Secret: d.Config.PolarWebhookSecret}}, nil
}
