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
    // close takes the returned choice because the caller is not always a native
    // submit. htmx preventDefaults a click on any type="submit" control, so a
    // confirm button carrying a request never submits its form method="dialog"
    // and the platform never closes the dialog or records returnValue. Closing
    // here restores both. The button stays connected, so htmx still sends.
    close(id, value) {
      const el = document.getElementById(id);
      if (el && el.open) {
        el.close(value || "");
      }
    },
  }));

  // uiMenu drives dropdowns and popovers. The panel is a native popover and the
  // trigger opens it declaratively with popovertarget, so the platform owns
  // disclosure, light dismiss, Escape and the top layer — which is what lets a
  // menu work with this file absent. This component adds only what the platform
  // leaves out.
  //
  // aria-expanded is one of those: a popovertarget button does not write it, and
  // an expandable button that never reports its state is a bug. It is set here
  // rather than rendered server-side because with no script the browser derives
  // the state from popovertarget itself, and a server-rendered "false" would
  // then be a stale lie the moment the panel opened.
  //
  // Arrow keys and typeahead are the other half. They are what makes a long menu
  // usable: without them the only way to reach the last item is Tab past every
  // item before it, and every one of those stops is a command.
  //
  // Every reference lives in this closure rather than on the component object.
  // A property declared in the returned literal is per element, but one
  // introduced by assignment inside init() is not: with ten menus on
  // /dev/gallery they shared a single `_trigger`, so closing any menu moved
  // focus to the last menu rendered on the page. Nothing needs a destroy hook
  // either - every listener sits on a node inside this component's own subtree,
  // so Alpine removing the subtree removes them with it.
  Alpine.data("uiMenu", () => ({
    init() {
      const root = this.$root;
      const trigger = root.querySelector("[data-ui-menu-trigger]");
      const panel = root.querySelector("[data-ui-menu-panel]");
      if (!trigger || !panel) return;
      trigger.setAttribute("aria-expanded", "false");

      let cameFromPanel = false;
      let onKey = null;
      let offDismiss = null;

      // beforetoggle, not toggle: whether the user was working inside the panel
      // has to be read before the platform closes it and moves focus, because
      // that is the difference between Escape (return focus to the trigger) and
      // a light dismiss on some other control (leave focus where the user just
      // put it).
      panel.addEventListener("beforetoggle", (event) => {
        if (event.newState === "closed") cameFromPanel = root.contains(document.activeElement);
      });
      panel.addEventListener("toggle", (event) => {
        const open = event.newState === "open";
        trigger.setAttribute("aria-expanded", open ? "true" : "false");
        if (open) {
          // The first command, not the panel: landing on a non-focusable
          // container would leave the arrow keys with nothing to move from.
          const items = menuItems(root);
          if (items.length) items[0].focus();
          onKey = (keyEvent) => menuKey(root, keyEvent);
          root.addEventListener("keydown", onKey);
          // A manual panel has traded the platform's light dismiss away (see
          // uiContextMenu for why), so those two dismissals are ours to supply.
          // Checked at open time rather than at init: which panels are manual
          // is decided by another component's init, and this makes the order
          // the two run in irrelevant.
          if (panel.popover === "manual") {
            offDismiss = installManualDismiss(root, panel);
          }
          return;
        }
        if (onKey) {
          root.removeEventListener("keydown", onKey);
          onKey = null;
        }
        if (offDismiss) {
          offDismiss();
          offDismiss = null;
        }
        if (cameFromPanel) trigger.focus();
        cameFromPanel = false;
      });
    },
  }));

  // uiContextMenu adds right-click to a region whose commands are already
  // reachable through a visible trigger. Right-click is unreachable by
  // keyboard, so it can only ever be the shortcut, never the mechanism.
  Alpine.data("uiContextMenu", () => ({
    init() {
      const region = this.$root.querySelector("[data-ui-context-region]");
      const holder = this.$root.querySelector("[data-ui-context-trigger]");
      const trigger = holder && holder.querySelector("[data-ui-menu-trigger]");
      const panel = holder && holder.querySelector("[data-ui-menu-panel]");
      if (!region || !trigger || !panel) return;
      // The panel becomes a manual popover, and only this one does.
      //
      // An auto popover light-dismisses on a pointer event outside itself, and
      // the right-click that opens a context menu *is* such a gesture: whether
      // `contextmenu` fires on the press (macOS) or the release (Windows), some
      // part of that gesture lands outside a panel that was closed when it
      // began, and the platform dismisses the menu we just opened. Deferring
      // the open only changes which toggle gets cancelled. Manual keeps the top
      // layer and popovertarget while opting out of that dismissal, so the
      // shortcut works on every platform; uiMenu supplies Escape and
      // outside-click for a manual panel in exchange.
      //
      // Upgrading here rather than rendering popover="manual" keeps the
      // scripting-off path on auto, where the platform's own Escape and light
      // dismiss are the only ones available.
      panel.setAttribute("popover", "manual");
      region.addEventListener("contextmenu", (event) => {
        event.preventDefault();
        trigger.click();
      });
    },
  }));

  // uiHoverCard opens on hover *and* focus. Hover alone is unreachable by
  // keyboard and absent on touch, so focus is not an enhancement here - it is
  // half the interface.
  Alpine.data("uiHoverCard", () => ({
    // `open` is declared, so Alpine gives each element its own and x-show reads
    // the right one. Anything the handlers need lives in the init() closure
    // instead: a property introduced by assignment inside init() is NOT
    // per-element under the CSP build, so a second hover card - one per table
    // row is the obvious use - would have cross-wired its trigger with the
    // first, silently, with nothing to see on a page that renders only one.
    // Nothing needs a destroy hook: every listener is on the trigger inside
    // this component's own subtree, so Alpine removing the subtree removes them.
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
      trigger.addEventListener("mouseenter", () => sync(true));
      trigger.addEventListener("focus", () => sync(true));
      trigger.addEventListener("mouseleave", () => sync(false));
      trigger.addEventListener("blur", () => sync(false));
      trigger.addEventListener("keydown", (event) => {
        if (event.key === "Escape") sync(false);
      });
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

// installManualDismiss gives a manual popover the two dismissals an auto one
// gets for free, and returns the teardown.
//
// Only a manual panel needs this, and only one is manual: the context menu,
// which cannot use auto without the gesture that opens it dismissing it.
// pointerdown rather than click, because a menu that stays open until the mouse
// comes back up is a menu the user has already left.
function installManualDismiss(root, panel) {
  const hide = () => {
    if (panel.matches(":popover-open")) panel.hidePopover();
  };
  const onPointerDown = (event) => {
    if (!root.contains(event.target)) hide();
  };
  const onKeyDown = (event) => {
    if (event.key === "Escape") hide();
  };
  document.addEventListener("pointerdown", onPointerDown);
  document.addEventListener("keydown", onKeyDown);
  return () => {
    document.removeEventListener("pointerdown", onPointerDown);
    document.removeEventListener("keydown", onKeyDown);
  };
}

// menuKey moves focus between an open menu's commands.
//
// Arrow keys and typeahead are what make a long menu usable: without them the
// only way to reach the last item is Tab past every item before it, and every
// one of those stops is a command.
function menuKey(root, event) {
  const items = menuItems(root);
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
  // Typeahead: a single printable character jumps to the next item whose label
  // starts with it, wrapping. Modifier combinations are left alone so browser
  // shortcuts keep working.
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
