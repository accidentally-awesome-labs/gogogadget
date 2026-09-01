// Package billing holds the plan truth and (from the billing step onward) the
// Polar client, webhook sync, and entitlements. plans.go is the single source
// of truth for plan definitions — rendered on the pricing page, enforced on
// project creation, and shown as usage limits.
package billing

import "github.com/gogogadget/gogogadget/internal/db/sqlc"

type Plan struct {
	Key, Name, PriceDisplay string
	PriceUSDMonthly         int
	ProviderProductID       string   // provider product identifier; "" for free
	MaxProjects             int      // -1 = unlimited
	MaxMembers              int      // informational only (invitations are Clerk-hosted)
	MaxStorageMB            int      // -1 = unlimited
	Meters                  []Meter  // monthly usage meters (rendered on the billing page)
	Features                []string // pricing-page bullets, reused by the upgrade-CTA fragment
}

// Meter is a monthly usage cap against usage_events.
type Meter struct {
	Key, Label    string
	LimitPerMonth int64 // -1 = unlimited
}

// defaultPlans is immutable source data. Callers receive plans only through a
// PlanCatalog, whose accessors deep-copy nested slices.
var defaultPlans = []Plan{
	{Key: "free", Name: "Free", PriceDisplay: "$0", MaxProjects: 3, MaxMembers: 1, MaxStorageMB: 50,
		Meters:   []Meter{{Key: "ai_tokens", Label: "AI tokens", LimitPerMonth: 100_000}},
		Features: []string{"3 projects", "1 team member", "50 MB storage", "100k AI tokens/mo", "Community support"}},
	{Key: "pro", Name: "Pro", PriceDisplay: "$20/mo", PriceUSDMonthly: 20, MaxProjects: -1, MaxMembers: 10, MaxStorageMB: 5000,
		Meters:   []Meter{{Key: "ai_tokens", Label: "AI tokens", LimitPerMonth: 2_500_000}},
		Features: []string{"Unlimited projects", "10 team members", "5 GB storage", "2.5M AI tokens/mo", "Priority support"}},
	{Key: "team", Name: "Team", PriceDisplay: "$50/mo", PriceUSDMonthly: 50, MaxProjects: -1, MaxMembers: -1, MaxStorageMB: -1,
		Meters:   []Meter{{Key: "ai_tokens", Label: "AI tokens", LimitPerMonth: -1}},
		Features: []string{"Unlimited everything", "Unlimited members", "Unlimited storage", "Unlimited AI tokens", "SSO via Clerk"}},
}

func DefaultPlanCatalog() PlanCatalog {
	plans := make([]Plan, len(defaultPlans))
	for i, p := range defaultPlans {
		plans[i] = clonePlan(p)
	}
	catalog, _ := NewPlanCatalog(plans)
	return catalog
}

// FreePlan returns a copy of the catalog's free plan for contexts that have no
// subscription loaded yet.
func FreePlan() Plan { return clonePlan(defaultPlans[0]) }

// PlanByKey is retained as a read-only compatibility helper for non-request
// callers. Request handlers use their injected PlanCatalog.
func PlanByKey(key string) Plan { return DefaultPlanCatalog().ByKey(key) }

func MRRWithCatalog(rows []sqlc.ListRevenueSubscriptionsRow, catalog PlanCatalog) int {
	if catalog == nil {
		catalog = DefaultPlanCatalog()
	}
	total := 0
	for _, r := range rows {
		total += catalog.ByKey(r.ProductKey).PriceUSDMonthly * int(r.N)
	}
	return total
}
func MRR(rows []sqlc.ListRevenueSubscriptionsRow) int {
	return MRRWithCatalog(rows, DefaultPlanCatalog())
}
