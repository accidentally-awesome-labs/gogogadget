// Alpine fragment owned by the navigation components that genuinely need
// script. Everything else in the family - breadcrumbs, steps, skip link, back
// link, table of contents, disclosure, accordion, collapsible - is plain markup
// or native <details>, and works with this file absent.
document.addEventListener("alpine:init", () => {
  // uiTabs implements the ARIA tab contract the markup claims: one tab stop for
  // the whole set, arrows to move, Home/End to jump. Without this the tablist
  // role would be a promise the widget breaks - which is worse for a screen
  // reader user than plain buttons, because they would expect arrows to work.
  //
  // It also owns the transition from the server's fallback: the page arrives
  // with every panel open and the tab bar hidden, because collapsing to one
  // panel before this file runs would lose the rest of the content and a tab
  // bar nothing can operate is a row of dead buttons.
  //
  // Every reference lives in this closure rather than on the component object.
  // A property declared in the returned literal is per element, but one
  // introduced by assignment inside init() is not - two tab widgets on one page
  // would share a single `tabs` array and each would drive the other's panels.
  Alpine.data("uiTabs", () => ({
    init() {
      const root = this.$root;
      const tabs = Array.from(root.querySelectorAll("[data-ui-tab]"));
      const panels = Array.from(root.querySelectorAll("[data-ui-tabpanel]"));
      if (!tabs.length) return;

      const select = (index) => {
        tabs.forEach((tab, i) => {
          const selected = i === index;
          tab.setAttribute("aria-selected", selected ? "true" : "false");
          // Exactly one tab is tabbable: a tablist where every tab is reachable
          // with Tab makes a keyboard user press it once per tab to get out.
          tab.setAttribute("tabindex", selected ? "0" : "-1");
        });
        panels.forEach((panel, i) => {
          if (i === index) {
            panel.removeAttribute("hidden");
          } else {
            panel.setAttribute("hidden", "");
          }
        });
      };

      tabs.forEach((tab, index) => {
        tab.addEventListener("click", () => select(index));
        tab.addEventListener("keydown", (event) => {
          let next = null;
          switch (event.key) {
            case "ArrowRight":
              next = (index + 1) % tabs.length;
              break;
            case "ArrowLeft":
              next = (index - 1 + tabs.length) % tabs.length;
              break;
            case "Home":
              next = 0;
              break;
            case "End":
              next = tabs.length - 1;
              break;
            default:
              return;
          }
          // Arrow keys move selection *and* focus, which is the automatic
          // activation pattern. It is the right choice here because switching a
          // panel is cheap and local; with expensive panels the manual pattern
          // (move focus, activate with Enter) would be correct instead.
          event.preventDefault();
          select(next);
          tabs[next].focus();
        });
      });

      const tablist = root.querySelector("[data-ui-tablist]");
      if (tablist) tablist.removeAttribute("hidden");
      // The server's aria-selected is the selection, not a separate copy of it
      // held here: reading it back is what stops the two disagreeing when a
      // caller renders a tab other than the first as selected.
      const selected = tabs.findIndex((tab) => tab.getAttribute("aria-selected") === "true");
      select(selected < 0 ? 0 : selected);
    },
  }));
});
