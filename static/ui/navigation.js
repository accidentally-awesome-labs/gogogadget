// Alpine fragment owned by the navigation components that genuinely need
// script. Everything else in the family - breadcrumbs, steps, skip link, back
// link, table of contents, disclosure, accordion - is plain markup or native
// <details>, and works with this file absent.
document.addEventListener("alpine:init", () => {
  // uiCollapsible toggles a region whose trigger lives elsewhere in the layout.
  // The region uses the `hidden` attribute rather than a class, so when closed
  // it leaves both the accessibility tree and the tab order instead of being an
  // invisible focus trap.
  Alpine.data("uiCollapsible", () => ({
    open: false,
    init() {
      this.open = this.$root.dataset.open === "true";
    },
    toggle() {
      this.open = !this.open;
    },
  }));

  // uiTabs implements the ARIA tab contract the markup claims: one tab stop for
  // the whole set, arrows to move, Home/End to jump. Without this the tablist
  // role would be a promise the widget breaks - which is worse for a screen
  // reader user than plain buttons, because they would expect arrows to work.
  Alpine.data("uiTabs", () => ({
    init() {
      this.tabs = Array.from(this.$root.querySelectorAll("[data-ui-tab]"));
      this.panels = Array.from(this.$root.querySelectorAll("[data-ui-tabpanel]"));
      this._onClick = (event) => {
        const index = this.tabs.indexOf(event.currentTarget);
        if (index >= 0) this.select(index);
      };
      this._onKey = (event) => {
        const current = this.tabs.indexOf(event.currentTarget);
        if (current < 0) return;
        let next = null;
        switch (event.key) {
          case "ArrowRight":
            next = (current + 1) % this.tabs.length;
            break;
          case "ArrowLeft":
            next = (current - 1 + this.tabs.length) % this.tabs.length;
            break;
          case "Home":
            next = 0;
            break;
          case "End":
            next = this.tabs.length - 1;
            break;
          default:
            return;
        }
        // Arrow keys move selection *and* focus, which is the automatic
        // activation pattern. It is the right choice here because switching a
        // panel is cheap and local; with expensive panels the manual pattern
        // (move focus, activate with Enter) would be correct instead.
        event.preventDefault();
        this.select(next);
        this.tabs[next].focus();
      };
      this.tabs.forEach((tab) => {
        tab.addEventListener("click", this._onClick);
        tab.addEventListener("keydown", this._onKey);
      });
    },
    select(index) {
      this.tabs.forEach((tab, i) => {
        const selected = i === index;
        tab.setAttribute("aria-selected", selected ? "true" : "false");
        // Exactly one tab is tabbable: a tablist where every tab is reachable
        // with Tab makes a keyboard user press it once per tab to get out.
        tab.setAttribute("tabindex", selected ? "0" : "-1");
      });
      this.panels.forEach((panel, i) => {
        if (i === index) {
          panel.removeAttribute("hidden");
        } else {
          panel.setAttribute("hidden", "");
        }
      });
    },
    destroy() {
      this.tabs.forEach((tab) => {
        tab.removeEventListener("click", this._onClick);
        tab.removeEventListener("keydown", this._onKey);
      });
    },
  }));
});
