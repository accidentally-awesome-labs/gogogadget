package billinglocal

import "github.com/gogogadget/gogogadget/internal/billing"

// LocalPlanCatalog uses stable plan keys as provider product IDs so local
// checkout exercises the same product resolution path as hosted billing.
func LocalPlanCatalog() billing.PlanCatalog {
	plans := billing.DefaultPlanCatalog().All()
	for i := range plans {
		if plans[i].Key != "free" {
			plans[i].ProviderProductID = plans[i].Key
		}
	}
	catalog, _ := billing.NewPlanCatalog(plans)
	return catalog
}
