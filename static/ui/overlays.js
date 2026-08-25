// Alpine fragment owned by element/ui-core: the named components the overlay
// renderers reference. Under CSP (`script-src 'self'`) Alpine cannot evaluate
// expression strings, so every x-data name must resolve to a component
// registered here — an unregistered name is silently inert.
//
// Registration happens on `alpine:init` so this file can load before
// alpine-csp.min.js without ordering assumptions.
document.addEventListener("alpine:init", () => {
  // uiDialog drives native <dialog> elements. showModal() is what supplies the
  // top-layer, the backdrop, the focus trap and Escape-to-close, so this
  // component only opens and closes it: reimplementing any of that in JS would
  // be strictly worse than the platform behaviour.
  Alpine.data("uiDialog", () => ({
    open(id) {
      const el = document.getElementById(id);
      if (el && typeof el.showModal === "function" && !el.open) {
        el.showModal();
      }
    },
    close(id) {
      const el = document.getElementById(id);
      if (el && el.open) {
        el.close();
      }
    },
  }));

  // uiMenu drives dropdowns and popovers: a disclosure whose trigger owns
  // aria-expanded, closing on Escape and on outside click. Focus moves into the
  // panel on open and returns to the trigger on close, because a keyboard user
  // who opens a menu and lands nowhere has no way back.
  Alpine.data("uiMenu", () => ({
    open: false,
    toggle() {
      this.open ? this.close() : this.show();
    },
    show() {
      this.open = true;
      this.$nextTick(() => {
        // $root, not $el: the method is invoked from the trigger's @click, so
        // $el is that button and the panel is its sibling, not its descendant.
        const panel = this.$root.querySelector("[data-ui-menu-panel]");
        const first = panel && panel.querySelector(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
        );
        if (first) first.focus();
      });
    },
    close() {
      if (!this.open) return;
      this.open = false;
      const trigger = this.$root.querySelector("[data-ui-menu-trigger]");
      if (trigger) trigger.focus();
    },
  }));
});
