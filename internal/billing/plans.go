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

// Ordered slice, rendered in this order — a Go map would shuffle pricing
// cards per run.
var Plans = []Plan{
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

// MRR sums monthly recurring revenue in USD over revenue subscriptions
// (active, trialing, past_due), priced from the plan truth.
func MRR(rows []sqlc.ListRevenueSubscriptionsRow) int {
	total := 0
	for _, r := range rows {
		total += PlanByKey(r.ProductKey).PriceUSDMonthly * int(r.N)
	}
	return total
}

// PlanByKey looks up a plan; unknown keys fall back to free.
func PlanByKey(key string) Plan {
	for _, p := range Plans {
		if p.Key == key {
			return p
		}
	}
	return Plans[0]
}

// SetProviderProductIDs injects product IDs from config at boot. The catalog API is preferred for new code.
func SetProviderProductIDs(pro, team string) {
	for i := range Plans {
		switch Plans[i].Key {
		case "pro":
			Plans[i].ProviderProductID = pro
		case "team":
			Plans[i].ProviderProductID = team
		}
	}
}
