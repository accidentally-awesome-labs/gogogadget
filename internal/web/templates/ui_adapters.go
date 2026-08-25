package templates

import (
	"context"
	"fmt"

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

// galleryChartBase builds a two-series fixture for the chart demos. The values
// are fixed so the visual baseline is deterministic.
func galleryChartBase(id, title string, legend bool) ui.ChartBase {
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri"}
	shipped := []float64{12, 19, 14, 22, 17}
	failed := []float64{1, 0, 3, 2, 1}
	build := func(values []float64) []ui.ChartPoint {
		points := make([]ui.ChartPoint, 0, len(days))
		for i, day := range days {
			points = append(points, ui.ChartPoint{
				Key: day, Label: day,
				Display: fmt.Sprintf("%.0f", values[i]),
				Value:   values[i],
			})
		}
		return points
	}
	return ui.ChartBase{
		ID: id, Title: title,
		Summary: "Shipped runs peaked on Thursday at 22; failures stayed at or below 3.",
		XLabel:  "Day", YLabel: "Runs",
		Legend: legend, Tooltip: true,
		Series: []ui.ChartSeries{
			{ID: "shipped", Label: "Shipped", Kind: ui.KindSuccess, Points: build(shipped)},
			{ID: "failed", Label: "Failed", Kind: ui.KindDanger, Points: build(failed)},
		},
	}
}

// gallerySingleSeries builds a one-series fixture, for the shapes where a second
// series would be meaningless.
func gallerySingleSeries(id, title string) ui.ChartBase {
	labels := []string{"Free", "Pro", "Team"}
	values := []float64{48, 31, 21}
	points := make([]ui.ChartPoint, 0, len(labels))
	for i, label := range labels {
		points = append(points, ui.ChartPoint{
			Key: label, Label: label,
			Display: fmt.Sprintf("%.0f%%", values[i]),
			Value:   values[i],
		})
	}
	return ui.ChartBase{
		ID: id, Title: title,
		Summary: "Free accounts are 48% of the total, Pro 31%, Team 21%.",
		XLabel:  "Plan", YLabel: "Share",
		Legend: true, Tooltip: true,
		Series: []ui.ChartSeries{{ID: "share", Label: "Share", Kind: ui.KindBrand, Points: points}},
	}
}
