package templates

import (
	"context"

	"github.com/a-h/templ"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// Adapters between this package's domain values and the leaf ui package. The
// direction is one-way by design: ui knows nothing about i18n, billing, or the
// page struct, so everything it needs arrives as plain strings resolved here.

// pagerLabels resolves the pager's localized strings for the request. ui takes
// functions rather than strings because the caption interpolates the page
// numbers, and ui must not import i18n to do it.
func pagerLabels(ctx context.Context) ui.PagerLabels {
	return ui.PagerLabels{
		Aria: func(int, int) string { return i18n.T(ctx, "activity.pagination_aria") },
		Prev: func(int, int) string { return i18n.T(ctx, "activity.pagination_prev") },
		Next: func(int, int) string { return i18n.T(ctx, "activity.pagination_next") },
		PageOf: func(page, totalPages int) string {
			return i18n.T(ctx, "activity.pagination_page_of", page, totalPages)
		},
	}
}

// uiNavItems resolves chrome nav items into ui items: label keys become labels
// in the request locale, because ui renders text and does not look it up.
func uiNavItems(ctx context.Context, items []NavItem) []ui.NavItem {
	out := make([]ui.NavItem, 0, len(items))
	for _, item := range items {
		out = append(out, ui.NavItem{
			Label: NavLabel(ctx, item),
			Href:  item.Href,
			Match: item.Match,
		})
	}
	return out
}

// uiPlanCard maps a billing plan onto the presentation-only card options. The
// price is formatted here: ui renders the string it is given.
func uiPlanCard(ctx context.Context, plan billing.Plan, current bool) ui.PlanCardOpts {
	return ui.PlanCardOpts{
		Name:         plan.Name,
		Price:        plan.PriceDisplay,
		Features:     plan.Features,
		Current:      current,
		CurrentLabel: i18n.T(ctx, "common.current_plan"),
	}
}

// dataTableToolbar builds the gallery's table toolbar. It lives here rather than
// inline because DataTable takes components as slots, and a slot argument
// cannot be written as templ markup at the call site.
func dataTableToolbar() templ.Component {
	return ui.TableToolbar(ui.TableToolbarOpts{Label: "Project table controls"})
}
