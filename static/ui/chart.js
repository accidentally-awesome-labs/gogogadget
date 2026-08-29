// Chart adapter, owned by component/chart.
//
// The server renders a complete figure: caption, summary and a semantic table.
// This file's only job is to add a picture on top of it, and every decision here
// follows from that being an *enhancement*:
//
//   - Data is parsed out of the rendered table, never from a second payload.
//     Two sources would drift, and the one a sighted user sees is the one that
//     would drift silently.
//   - The canvas is revealed only after Chart.js initialises. A visible empty
//     canvas above a table is worse than no canvas.
//   - Colours resolve from the semantic tokens at draw time, for whichever theme
//     is active, so a chart never carries its own palette.
//   - Instances are destroyed before htmx swaps the content box away. Chart.js
//     holds a canvas reference and a resize observer; leaking those on every
//     navigation is a slow leak that only shows up after ten minutes of use.
// Chart instances live in a WeakMap keyed by their root, never on the Alpine
// component. Alpine wraps component state in a reactive Proxy, and Chart.js
// walks its own deeply nested internals - through that proxy it recurses until
// the stack overflows and then corrupts the objects it did reach. The symptom is
// "Maximum call stack size exceeded" inside alpine-csp followed by "cannot set
// fullSize", which points at Chart.js and is caused by us.
const charts = new WeakMap();

document.addEventListener("alpine:init", () => {
  Alpine.data("uiChart", () => ({
    init() {
      const root = this.$root;
      // The engine adapter announces readiness on this same root.
      root.addEventListener("ui:engine-ready", () => this.draw(), { once: true });
      root.addEventListener(
        "ui:engine-failed",
        () => {
          // Nothing to do: the table is already the chart. Leaving the mount
          // hidden is the correct end state, not a degraded one.
        },
        { once: true },
      );
      this._onTheme = () => {
        const chart = charts.get(root);
        if (!chart) return;
        repaint(chart);
        // "none" skips the animation: a theme flip is not a data change, and
        // animating it makes the whole page look like it is reloading.
        chart.update("none");
      };
      document.addEventListener("ui:theme-changed", this._onTheme);
    },
    draw() {
      const root = this.$root;
      const canvas = root.querySelector("[data-chart-canvas]");
      const mount = root.querySelector("[data-chart-mount]");
      if (!canvas || !mount || typeof window.Chart === "undefined") return;

      const parsed = parseTable(root);
      if (!parsed || !parsed.labels.length) return;

      const shape = root.dataset.chartShape || "bar";
      const palette = seriesColors(parsed.series);
      const axis = axisColors();
      const chart = new window.Chart(canvas, {
        type: chartType(shape),
        data: {
          labels: parsed.labels,
          datasets: parsed.series.map((series, index) => ({
            label: series.label,
            data: series.values,
            // Colours are passed in at construction rather than assigned
            // afterwards. Chart.js resolves options into proxied objects, and
            // replacing one of those post-hoc corrupts its internals - the
            // symptom is a stack overflow and "cannot set fullSize", not a
            // wrong colour.
            borderColor: palette[index],
            backgroundColor: palette[index],
            pointBackgroundColor: palette[index],
            // The kind rides on the dataset so a theme flip can re-resolve the
            // same token without re-reading the table.
            __uiKind: series.kind,
            fill: shape === "area",
            tension: shape === "sparkline" ? 0.3 : 0,
            pointRadius: shape === "sparkline" ? 0 : 3,
          })),
        },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          // The picture is decorative: the table carries the data, so Chart.js
          // must not add its own accessibility description on top of it.
          plugins: {
            legend: { display: false },
            tooltip: { enabled: root.dataset.chartTooltip !== "false" },
          },
          scales:
            shape === "doughnut"
              ? {}
              : {
                  x: { grid: { display: false }, ticks: { color: axis.text } },
                  y: { grid: { color: axis.grid }, ticks: { color: axis.text } },
                },
          // Honour the OS setting rather than animating regardless: motion here
          // is decoration, and the user has already said no.
          animation: prefersReducedMotion() ? false : undefined,
        },
      });
      charts.set(root, chart);
      // Reveal only after construction succeeded: a visible empty canvas above
      // a table is worse than no canvas at all.
      mount.removeAttribute("hidden");
      registerInstance(root);
    },
    destroy() {
      document.removeEventListener("ui:theme-changed", this._onTheme);
      const chart = charts.get(this.$root);
      if (chart) {
        chart.destroy();
        charts.delete(this.$root);
      }
      unregisterInstance(this.$root);
    },
  }));
});

// parseTable reads the rendered table. This is the single source of truth: the
// numbers a sighted user sees in the picture are the numbers in the table,
// because they are literally the same values.
function parseTable(root) {
  const table = root.querySelector("[data-chart-table] table");
  if (!table) return null;
  const headers = Array.from(table.querySelectorAll("thead th[data-series-id]"));
  const rows = Array.from(table.querySelectorAll("tbody tr"));
  const labels = rows.map((row) => {
    const cell = row.querySelector("th");
    return cell ? cell.textContent.trim() : "";
  });
  const series = headers.map((header, index) => ({
    id: header.dataset.seriesId,
    kind: header.dataset.seriesKind,
    label: header.textContent.trim(),
    values: rows.map((row) => {
      const cells = row.querySelectorAll("td");
      const cell = cells[index];
      if (!cell) return null;
      const raw = cell.dataset.value;
      // An empty cell stays null rather than becoming zero: "no data" and
      // "zero" are different facts and the chart must show the gap.
      return raw === undefined || raw === "" ? null : Number(raw);
    }),
  }));
  return { labels, series };
}

function chartType(shape) {
  switch (shape) {
    case "line":
    case "area":
    case "sparkline":
      return "line";
    case "doughnut":
      return "doughnut";
    default:
      return "bar";
  }
}

// Colours resolve from the semantic tokens at draw time, for whichever theme is
// active, so a chart follows a rebrand and a theme flip without carrying its own
// palette. The series kind comes from the table header the server rendered.
function seriesColors(series) {
  const styles = getComputedStyle(document.documentElement);
  const token = (name) => styles.getPropertyValue(name).trim();
  const fallback = token("--color-chart-1");
  return series.map((entry, index) => {
    const byKind = entry.kind ? token("--color-" + entry.kind) : "";
    return byKind || token("--color-chart-" + ((index % 6) + 1)) || fallback;
  });
}

function axisColors() {
  const styles = getComputedStyle(document.documentElement);
  return {
    grid: styles.getPropertyValue("--color-border").trim(),
    text: styles.getPropertyValue("--color-fg-muted").trim(),
  };
}

// repaint assigns leaf properties only. Replacing a resolved options object
// instead - even with a merged copy - breaks Chart.js internals, so every write
// here targets a single value.
function repaint(chart) {
  const palette = seriesColors(
    chart.data.datasets.map((dataset, index) => ({ kind: dataset.__uiKind, label: dataset.label, index })),
  );
  chart.data.datasets.forEach((dataset, index) => {
    dataset.borderColor = palette[index];
    dataset.backgroundColor = palette[index];
    dataset.pointBackgroundColor = palette[index];
  });
  const axis = axisColors();
  const scales = chart.options.scales || {};
  if (scales.x && scales.x.ticks) scales.x.ticks.color = axis.text;
  if (scales.y && scales.y.ticks) scales.y.ticks.color = axis.text;
  if (scales.y && scales.y.grid) scales.y.grid.color = axis.grid;
}

function prefersReducedMotion() {
  return window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

// Instances are tracked so they can be destroyed before htmx replaces the
// content box. Alpine's own destroy() also fires on removal, but htmx swaps the
// subtree out first on some paths, and a Chart.js instance whose canvas is gone
// keeps its resize observer alive.
const roots = new Set();

function registerInstance(root) {
  roots.add(root);
}

function unregisterInstance(root) {
  roots.delete(root);
}

document.addEventListener("htmx:before:swap", () => {
  roots.forEach((root) => {
    const chart = charts.get(root);
    if (chart) {
      chart.destroy();
      charts.delete(root);
    }
  });
  roots.clear();
});
