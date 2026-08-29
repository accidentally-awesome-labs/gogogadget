// Panel resize controller, owned by component/panels.
//
// The layout already works: panels are stacked blocks, and on a wide screen a
// flex row. This file makes the dividers draggable and, more importantly,
// operable by keyboard - a pointer-only splitter is a control most people
// cannot use.
//
// Sizes are percentages so the split survives a narrower viewport, and they are
// stored per user per device: a layout preference is not data, and sending it to
// the server would make it look like one.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiPanels", () => ({
    init() {
      const root = this.$root;
      this.root = root;
      this.vertical = root.dataset.panelOrientation === "vertical";

      this.restore();
      this.apply();

      this._onKey = (event) => this.onKey(event);
      root.addEventListener("keydown", this._onKey);
      this._onDown = (event) => this.startDrag(event);
      root.addEventListener("pointerdown", this._onDown);
    },

    panels() {
      return Array.from(this.root.querySelectorAll("[data-panel]"));
    },
    handles() {
      return Array.from(this.root.querySelectorAll("[data-panel-handle]"));
    },

    // apply writes the sizes as flex-basis. A panel keeps its own floor, so no
    // amount of dragging can hide its content entirely.
    apply() {
      const panels = this.panels();
      panels.forEach((panel, index) => {
        const handle = this.handles()[index];
        const size = handle ? Number(handle.getAttribute("aria-valuenow")) : null;
        if (size === null || Number.isNaN(size)) return;
        panel.style.flex = "0 0 " + size + "%";
        const next = panels[index + 1];
        if (next) next.style.flex = "1 1 auto";
      });
    },

    set(handle, value) {
      const min = Number(handle.getAttribute("aria-valuemin"));
      const max = Number(handle.getAttribute("aria-valuemax"));
      const clamped = Math.max(min, Math.min(max, Math.round(value)));
      handle.setAttribute("aria-valuenow", String(clamped));
      this.apply();
      this.store();
    },

    // Arrow keys move by one percent and Page keys by ten, which is what makes
    // the splitter usable without a pointer. Home and End go to the bounds.
    onKey(event) {
      const handle = event.target.closest("[data-panel-handle]");
      if (!handle) return;
      const now = Number(handle.getAttribute("aria-valuenow"));
      const min = Number(handle.getAttribute("aria-valuemin"));
      const max = Number(handle.getAttribute("aria-valuemax"));
      const back = this.vertical ? "ArrowUp" : "ArrowLeft";
      const forward = this.vertical ? "ArrowDown" : "ArrowRight";

      let next = null;
      if (event.key === back) next = now - 1;
      else if (event.key === forward) next = now + 1;
      else if (event.key === "PageUp") next = now - 10;
      else if (event.key === "PageDown") next = now + 10;
      else if (event.key === "Home") next = min;
      else if (event.key === "End") next = max;
      if (next === null) return;

      event.preventDefault();
      this.set(handle, next);
    },

    startDrag(event) {
      const handle = event.target.closest("[data-panel-handle]");
      if (!handle) return;
      event.preventDefault();
      // Focus follows the drag, so a pointer user who then reaches for the
      // keyboard is already on the control they just moved.
      handle.focus();

      const box = this.root.getBoundingClientRect();
      const total = this.vertical ? box.height : box.width;
      if (total <= 0) return;

      const move = (moveEvent) => {
        const offset = this.vertical
          ? moveEvent.clientY - box.top
          : moveEvent.clientX - box.left;
        this.set(handle, (offset / total) * 100);
      };
      const up = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", up);
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", up);
    },

    key() {
      const name = this.root.dataset.panelPersist;
      return name ? "ggg.panels." + name : null;
    },
    store() {
      const key = this.key();
      if (!key) return;
      const sizes = this.handles().map((handle) => handle.getAttribute("aria-valuenow"));
      try {
        localStorage.setItem(key, JSON.stringify(sizes));
      } catch (e) {
        // A blocked or full storage must not break the layout.
      }
    },
    restore() {
      const key = this.key();
      if (!key) return;
      let sizes = null;
      try {
        sizes = JSON.parse(localStorage.getItem(key) || "null");
      } catch (e) {
        sizes = null;
      }
      if (!Array.isArray(sizes)) return;
      this.handles().forEach((handle, index) => {
        const value = Number(sizes[index]);
        if (Number.isNaN(value)) return;
        // Clamped on the way back in: a stored size from an older layout could
        // otherwise place a handle outside its own bounds.
        const min = Number(handle.getAttribute("aria-valuemin"));
        const max = Number(handle.getAttribute("aria-valuemax"));
        handle.setAttribute("aria-valuenow", String(Math.max(min, Math.min(max, value))));
      });
    },

    destroy() {
      this.root.removeEventListener("keydown", this._onKey);
      this.root.removeEventListener("pointerdown", this._onDown);
    },
  }));
});
