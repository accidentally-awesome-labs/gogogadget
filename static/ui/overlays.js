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
  //
  // Scroll lock is ours to add, because the platform does not stop the page
  // behind a modal from scrolling under a trackpad or wheel gesture.
  Alpine.data("uiDialog", () => ({
    open(id) {
      const el = document.getElementById(id);
      if (el && typeof el.showModal === "function" && !el.open) {
        el.showModal();
        lockScroll(el);
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
  //
  // Arrow keys and typeahead are added on top. They are what makes a long menu
  // usable: without them the only way to reach the last item is Tab past every
  // item before it, and every one of those stops is a command.
  Alpine.data("uiMenu", () => ({
    open: false,
    toggle() {
      this.open ? this.close() : this.show();
    },
    show() {
      this.open = true;
      this.$nextTick(() => {
        const items = menuItems(this.$root);
        if (items.length) items[0].focus();
        this._onKey = (event) => this.onKey(event);
        this.$root.addEventListener("keydown", this._onKey);
      });
    },
    onKey(event) {
      const items = menuItems(this.$root);
      if (!items.length) return;
      const current = items.indexOf(document.activeElement);
      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          items[(current + 1 + items.length) % items.length].focus();
          return;
        case "ArrowUp":
          event.preventDefault();
          items[(current - 1 + items.length) % items.length].focus();
          return;
        case "Home":
          event.preventDefault();
          items[0].focus();
          return;
        case "End":
          event.preventDefault();
          items[items.length - 1].focus();
          return;
      }
      // Typeahead: a single printable character jumps to the next item whose
      // label starts with it, wrapping. Modifier combinations are left alone so
      // browser shortcuts keep working.
      if (event.key.length !== 1 || event.metaKey || event.ctrlKey || event.altKey) return;
      const needle = event.key.toLowerCase();
      for (let step = 1; step <= items.length; step++) {
        const candidate = items[(current + step + items.length) % items.length];
        if ((candidate.textContent || "").trim().toLowerCase().startsWith(needle)) {
          event.preventDefault();
          candidate.focus();
          return;
        }
      }
    },
    close() {
      if (!this.open) return;
      this.open = false;
      if (this._onKey) {
        this.$root.removeEventListener("keydown", this._onKey);
        this._onKey = null;
      }
      // $root, not $el: the method is invoked from the trigger's @click, so
      // $el is that button and the panel is its sibling, not its descendant.
      const trigger = this.$root.querySelector("[data-ui-menu-trigger]");
      if (trigger) trigger.focus();
    },
  }));

  // uiContextMenu adds right-click to a region whose commands are already
  // reachable through a visible trigger. Right-click is unreachable by
  // keyboard, so it can only ever be the shortcut, never the mechanism.
  Alpine.data("uiContextMenu", () => ({
    init() {
      const region = this.$root.querySelector("[data-ui-context-region]");
      const trigger = this.$root.querySelector("[data-ui-context-trigger] [data-ui-menu-trigger]");
      if (!region || !trigger) return;
      this._onContext = (event) => {
        event.preventDefault();
        trigger.click();
      };
      region.addEventListener("contextmenu", this._onContext);
      this._region = region;
    },
    destroy() {
      if (this._region && this._onContext) {
        this._region.removeEventListener("contextmenu", this._onContext);
      }
    },
  }));

  // uiHoverCard opens on hover *and* focus. Hover alone is unreachable by
  // keyboard and absent on touch, so focus is not an enhancement here - it is
  // half the interface.
  Alpine.data("uiHoverCard", () => ({
    open: false,
    init() {
      const trigger = this.$root.querySelector("[data-ui-hovercard-trigger]");
      if (!trigger) return;
      // aria-expanded is kept in step here rather than bound in markup: the
      // controller already owns open/close on hover, focus and Escape, and a
      // second owner would let the attribute drift from the panel.
      const sync = (open) => {
        this.open = open;
        trigger.setAttribute("aria-expanded", open ? "true" : "false");
      };
      this._show = () => sync(true);
      this._hide = () => sync(false);
      this._onKey = (event) => {
        if (event.key === "Escape") sync(false);
      };
      trigger.addEventListener("mouseenter", this._show);
      trigger.addEventListener("focus", this._show);
      trigger.addEventListener("mouseleave", this._hide);
      trigger.addEventListener("blur", this._hide);
      trigger.addEventListener("keydown", this._onKey);
      this._trigger = trigger;
    },
    destroy() {
      const trigger = this._trigger;
      if (!trigger) return;
      trigger.removeEventListener("mouseenter", this._show);
      trigger.removeEventListener("focus", this._show);
      trigger.removeEventListener("mouseleave", this._hide);
      trigger.removeEventListener("blur", this._hide);
      trigger.removeEventListener("keydown", this._onKey);
    },
  }));
});

// menuItems returns the focusable commands in a menu panel, in DOM order.
// Separators and disabled items are excluded because they are not commands: an
// arrow key that lands on a divider is a keystroke the user has to repeat.
function menuItems(root) {
  const panel = root.querySelector("[data-ui-menu-panel]");
  if (!panel) return [];
  return Array.from(
    panel.querySelectorAll('a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])'),
  );
}

// lockScroll stops the page behind a modal from scrolling and restores the
// previous value when the dialog closes. The platform does not do this, so a
// wheel gesture over the backdrop scrolls the page the user cannot see.
function lockScroll(dialog) {
  const previous = document.body.style.overflow;
  document.body.style.overflow = "hidden";
  dialog.addEventListener(
    "close",
    () => {
      document.body.style.overflow = previous;
    },
    { once: true },
  );
}
