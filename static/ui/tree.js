// Tree keyboard controller, owned by component/tree.
//
// The markup is nested `details` elements, which already open, close and read
// correctly with no script. This file adds the APG keyboard model on top: one
// tab stop, arrow navigation, Home/End, and typeahead. It upgrades the existing
// semantics rather than replacing them, so every failure mode still leaves a
// working disclosure tree.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiTree", () => ({
    init() {
      const root = this.$root;
      this.root = root;
      this.typed = "";
      this.typedAt = 0;

      // One tab stop for the whole tree: tabbing through a two-hundred-node
      // tree should not take two hundred presses.
      const items = this.items();
      items.forEach((item, index) => {
        item.tabIndex = index === 0 ? 0 : -1;
      });

      this._onKey = (event) => this.onKey(event);
      root.addEventListener("keydown", this._onKey);
      this._onFocus = (event) => {
        const item = event.target.closest("[data-tree-item]");
        if (item) this.rove(item, false);
      };
      root.addEventListener("focusin", this._onFocus);

      // Lazy children: a branch declaring HasChildren fetches on first open.
      this._onToggle = (event) => {
        const branch = event.target;
        if (!branch.matches || !branch.matches("[data-tree-branch]")) return;
        if (!branch.open) return;
        this.load(branch);
      };
      // "toggle" does not bubble, so it is captured.
      root.addEventListener("toggle", this._onToggle, true);
    },

    // items are the focusable rows, in document order, skipping anything inside
    // a closed branch - an invisible row must never receive focus.
    //
    // offsetParent is not the test. Chromium renders closed `details` content
    // with content-visibility:hidden, which leaves offsetParent set while the
    // content is genuinely inert: focus() on such a row silently fails, so the
    // arrow keys would step onto rows that cannot be reached. Walking the
    // ancestor chain is exact and needs no browser-specific API.
    items() {
      return Array.from(this.root.querySelectorAll("[data-tree-item]")).filter((item) =>
        this.reachable(item),
      );
    },

    // reachable reports whether every branch above this row is open. A row's own
    // summary is exempt: a closed branch's summary is exactly the control the
    // user needs in order to open it.
    reachable(item) {
      let node = item.parentElement;
      const ownBranch = item.tagName === "SUMMARY" ? item.parentElement : null;
      while (node && node !== this.root) {
        if (node.tagName === "DETAILS" && !node.open && node !== ownBranch) return false;
        node = node.parentElement;
      }
      return true;
    },

    rove(item, move) {
      this.items().forEach((other) => {
        other.tabIndex = other === item ? 0 : -1;
      });
      if (move) item.focus();
    },

    onKey(event) {
      const item = event.target.closest("[data-tree-item]");
      if (!item) return;

      const items = this.items();
      const index = items.indexOf(item);
      const branch = item.closest("details[data-tree-branch]");
      const isSummary = item.tagName === "SUMMARY";

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          if (items[index + 1]) this.rove(items[index + 1], true);
          return;
        case "ArrowUp":
          event.preventDefault();
          if (items[index - 1]) this.rove(items[index - 1], true);
          return;
        case "ArrowRight":
          // On a closed branch, open it. On an open one, move to its first
          // child. On a leaf, do nothing - which is the APG behaviour and also
          // the honest one: there is nowhere to go.
          if (isSummary && branch && !branch.open) {
            event.preventDefault();
            branch.open = true;
          } else if (isSummary && branch && items[index + 1]) {
            event.preventDefault();
            this.rove(items[index + 1], true);
          }
          return;
        case "ArrowLeft":
          // On an open branch, close it. Otherwise move to the parent branch,
          // so Left always means "out".
          if (isSummary && branch && branch.open) {
            event.preventDefault();
            branch.open = false;
            return;
          }
          {
            const parent = item.closest("ul")?.closest("details[data-tree-branch]");
            const summary = parent && parent.querySelector(":scope > summary");
            if (summary) {
              event.preventDefault();
              this.rove(summary, true);
            }
          }
          return;
        case "Home":
          event.preventDefault();
          if (items[0]) this.rove(items[0], true);
          return;
        case "End":
          event.preventDefault();
          if (items[items.length - 1]) this.rove(items[items.length - 1], true);
          return;
        default:
          break;
      }

      // Typeahead: printable characters jump to the next matching row. A long
      // tree is unusable without it, and the buffer resets after a pause so a
      // new search does not inherit the last one.
      if (event.key.length !== 1 || event.metaKey || event.ctrlKey || event.altKey) return;
      const now = Date.now();
      this.typed = now - this.typedAt > 800 ? event.key : this.typed + event.key;
      this.typedAt = now;
      const needle = this.typed.toLowerCase();
      const ordered = items.slice(index + 1).concat(items.slice(0, index + 1));
      const hit = ordered.find((candidate) =>
        (candidate.textContent || "").trim().toLowerCase().startsWith(needle),
      );
      if (hit) {
        event.preventDefault();
        this.rove(hit, true);
      }
    },

    // load fetches a branch's children once. htmx owns the request so the swap
    // follows the same rules as every other fragment.
    load(branch) {
      const pending = branch.querySelector("[data-tree-pending]");
      const url = this.root.dataset.treeLazyUrl;
      if (!pending || !url) return;
      const node = branch.closest("[data-tree-node]");
      if (!node || node.dataset.treeLoaded === "true") return;
      node.dataset.treeLoaded = "true";

      if (!window.htmx) return;
      window.htmx
        .ajax("GET", url + (url.includes("?") ? "&" : "?") + "node=" + encodeURIComponent(node.dataset.treeNode), {
          target: pending,
          swap: "outerHTML",
        })
        .then(() => {
          // New rows joined the tree, so the single tab stop has to be
          // recomputed or Tab returns to a row that no longer exists.
          const items = this.items();
          items.forEach((item, index) => {
            item.tabIndex = index === 0 ? 0 : -1;
          });
        });
    },

    destroy() {
      this.root.removeEventListener("keydown", this._onKey);
      this.root.removeEventListener("focusin", this._onFocus);
      this.root.removeEventListener("toggle", this._onToggle, true);
    },
  }));
});
