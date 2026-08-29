package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func galleryChart() ChartBase {
	return ChartBase{
		ID: "runs", Title: "Runs per day",
		Summary: "Shipped peaked at 22 on Thursday.",
		XLabel:  "Day", YLabel: "Runs", Legend: true,
		Series: []ChartSeries{
			{ID: "shipped", Label: "Shipped", Kind: KindSuccess, Points: []ChartPoint{
				{Key: "mon", Label: "Mon", Display: "12", Value: 12},
				{Key: "tue", Label: "Tue", Display: "19", Value: 19},
			}},
			{ID: "failed", Label: "Failed", Kind: KindDanger, Points: []ChartPoint{
				{Key: "mon", Label: "Mon", Display: "1", Value: 1},
				{Key: "tue", Label: "Tue", Display: "0", Value: 0},
			}},
		},
	}
}

// The table is the chart. It is rendered on the server, always present, and
// never hidden - so a reader with no JavaScript, a screen-reader user, and a
// printed page all get the data rather than an empty box.
func TestChartRendersItsDataWithoutScript(t *testing.T) {
	html := renderComponent(t, BarChart(BarChartOpts{Chart: galleryChart()}))

	assert.Contains(t, html, "<table")
	assert.Contains(t, html, "Runs per day")
	assert.Contains(t, html, "Shipped peaked at 22 on Thursday.")
	for _, value := range []string{">Mon<", ">Tue<", ">12<", ">19<"} {
		assert.Contains(t, html, value, "the table must carry the real values")
	}

	// The table is not hidden and carries no aria-hidden.
	tableAt := strings.Index(html, "data-chart-table")
	require.Positive(t, tableAt)
	tableTag := html[tableAt-60 : tableAt+40]
	assert.NotContains(t, tableTag, "hidden")

	// The figure names and describes itself from elements that exist.
	assert.Contains(t, html, `aria-labelledby="runs-title"`)
	assert.Contains(t, html, `id="runs-title"`)
	assert.Contains(t, html, `aria-describedby="runs-summary"`)
	assert.Contains(t, html, `id="runs-summary"`)
}

// A visible empty canvas above a table is worse than no canvas, so the mount
// starts hidden and the adapter reveals it only after the engine initialises.
func TestChartCanvasStartsHiddenAndSilent(t *testing.T) {
	html := renderComponent(t, LineChart(LineChartOpts{Chart: galleryChart()}))

	mountAt := strings.Index(html, "data-chart-mount")
	require.Positive(t, mountAt)
	mount := html[mountAt : mountAt+80]
	assert.Contains(t, mount, "hidden")
	assert.Contains(t, mount, `aria-hidden="true"`,
		"everything the canvas shows is already in the table; exposing it duplicates the dataset")
	assert.Contains(t, html, "<canvas")
}

// The engine is requested by the root's presence alone: no chart on the page
// means no Chart.js fetch anywhere.
func TestChartDeclaresItsEngineOnTheRoot(t *testing.T) {
	html := renderComponent(t, AreaChart(AreaChartOpts{Chart: galleryChart()}))
	assert.Contains(t, html, `data-ui-engine="chartjs"`)
	assert.Contains(t, html, `x-data="uiChart"`)
	assert.Contains(t, html, `data-chart-shape="area"`)

	assert.Contains(t, renderComponent(t, DonutChart(DonutChartOpts{Chart: galleryChart()})),
		`data-chart-shape="doughnut"`)
	assert.Contains(t, renderComponent(t, Sparkline(SparklineOpts{Chart: galleryChart()})),
		`data-chart-shape="sparkline"`)
}

// The adapter reads the rendered table, so the values it draws are the values a
// sighted reader sees. Each cell therefore carries its machine value alongside
// the formatted display string.
func TestChartCellsCarryBothDisplayAndValue(t *testing.T) {
	html := renderComponent(t, BarChart(BarChartOpts{Chart: galleryChart()}))
	assert.Contains(t, html, `data-value="12"`)
	assert.Contains(t, html, `>12<`)
	assert.Contains(t, html, `data-series-id="shipped"`)
	assert.Contains(t, html, `data-series-kind="success"`)
	assert.Contains(t, html, `data-point-key="mon"`)
}

// "No data" and "zero" are different facts. A ragged series renders an empty
// cell rather than a zero, because padding it would invent data.
func TestChartMissingPointIsNotZero(t *testing.T) {
	chart := galleryChart()
	chart.Series[1].Points = chart.Series[1].Points[:1]
	html := renderComponent(t, BarChart(BarChartOpts{Chart: chart}))

	assert.Contains(t, html, "—", "a gap is rendered as a gap")
	assert.Equal(t, 1, strings.Count(html, `data-value="0"`),
		"the real zero survives and the missing point does not become one")
}

// Colour alone cannot distinguish series for a colour-blind reader, so the
// legend's swatch is decoration and the label carries the identity.
func TestChartLegendNamesEverySeries(t *testing.T) {
	html := renderComponent(t, ChartLegend(ChartLegendOpts{Series: galleryChart().Series}))
	assert.Contains(t, html, "Shipped")
	assert.Contains(t, html, "Failed")
	assert.Equal(t, 2, strings.Count(html, `aria-hidden="true"`), "both swatches are decorative")
	assert.Contains(t, html, "bg-success")
	assert.Contains(t, html, "bg-danger")
}

// An empty series set must render the figure without inventing rows: a chart
// with no data is a caption and an empty table, not a crash.
func TestChartWithNoSeriesStillRenders(t *testing.T) {
	html := renderComponent(t, BarChart(BarChartOpts{Chart: ChartBase{ID: "empty", Title: "No data yet"}}))
	assert.Contains(t, html, "No data yet")
	assert.Contains(t, html, "<table")
	assert.NotContains(t, html, "<tr><th scope=\"row\"")
}
