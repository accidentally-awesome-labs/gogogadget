// Data grid controller, owned by component/data-grid.
//
// Everything here is an enhancement over a table that already works. Sort,
// search and pagination are server-owned URLs, so this file never holds a copy
// of the data - it adds cell navigation, column controls, and fetching the next
// window when the user scrolls to it.
//
// Two refusals shape the file:
//
//   - The pager is never removed. Windowed fetching is an addition, so a
//     keyboard user or a script-less page still reaches every row by paging.
//   - The table keeps table semantics. role="grid" promises a full
//     two-dimensional keyboard model; claiming it and delivering less is worse
//     for a screen-reader user than the table they already understand.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiGrid", () => ({
    init() {
      const root = this.$root;
      const table = root.querySelector("[data-grid-table]");
      if (!table) return;

      this.root = root;
      this.table = table;
      this.key = "ggg.grid." + (root.id || "grid");

      // Column controls only appear now: hiding a column does nothing without
      // this controller, so an inert picker would be a dead end.
      //
      // A picker may sit inside this root or beside it - a toolbar is often
      // rendered next to the table - so it is found by the grid it names. That
      // is what ColumnPicker.For is for; resolving only inside the root would
      // leave an outside picker permanently dead.
      this.pickers().forEach((picker) => {
        picker.hidden = false;
      });

      this.applyStoredPreferences();

      // One roving tab stop across the cells, so tabbing past a thousand-row
      // grid takes one press instead of a thousand. Controls inside cells keep
      // their own stops - a sort link has to be a link - so the table is not
      // literally a single stop, and it should not be: those controls are
      // reachable precisely because they are real.
      this.cells().forEach((cell, index) => {
        cell.tabIndex = index === 0 ? 0 : -1;
      });

      this._onKey = (event) => this.onKey(event);
      table.addEventListener("keydown", this._onKey);

      this._onFocus = (event) => {
        const cell = event.target.closest("td, th");
        if (cell && table.contains(cell)) this.focusCell(cell, false);
      };
      table.addEventListener("focusin", this._onFocus);

      this._onToggle = (event) => {
        const box = event.target.closest("[data-grid-toggle-column]");
        if (!box) return;
        const picker = box.closest("[data-grid-column-picker]");
        // Two grids on one page must not answer each other's pickers.
        if (!this.pickers().includes(picker)) return;
        this.setColumnHidden(box.dataset.gridToggleColumn, !box.checked);
        this.storePreferences();
      };
      document.addEventListener("change", this._onToggle);

      this._onPointerDown = (event) => this.startResize(event);
      table.addEventListener("pointerdown", this._onPointerDown);

      this.watchScroll();
    },

    // pickers finds every column picker that names this grid, whether it is
    // nested in the root or rendered beside it.
    pickers() {
      const inside = Array.from(this.root.querySelectorAll("[data-grid-column-picker]"));
      const named = this.root.id
        ? Array.from(
            document.querySelectorAll(
              `[data-grid-column-picker][data-grid-for="${this.root.id}"]`,
            ),
          )
        : [];
      return Array.from(new Set(inside.concat(named)));
    },

    // --- cell navigation ---------------------------------------------------

    cells() {
      return Array.from(this.table.querySelectorAll("th, td")).filter(
        (cell) => cell.offsetParent !== null,
      );
    },
    grid() {
      return Array.from(this.table.rows)
        .map((row) => Array.from(row.cells).filter((cell) => cell.offsetParent !== null))
        .filter((row) => row.length > 0);
    },
    position(cell) {
      const rows = this.grid();
      for (let r = 0; r < rows.length; r++) {
        const c = rows[r].indexOf(cell);
        if (c !== -1) return { rows, r, c };
      }
      return null;
    },
    onKey(event) {
      const cell = event.target.closest("td, th");
      if (!cell) return;

      // A cell containing a control belongs to that control: arrow keys inside
      // a text field move the caret, and stealing them breaks editing.
      if (event.target !== cell && event.target.closest("input, textarea, select")) return;

      const at = this.position(cell);
      if (!at) return;
      const { rows, r, c } = at;
      let target = null;

      switch (event.key) {
        case "ArrowRight":
          target = rows[r][c + 1];
          break;
        case "ArrowLeft":
          target = rows[r][c - 1];
          break;
        case "ArrowDown":
          target = rows[r + 1] && rows[r + 1][Math.min(c, rows[r + 1].length - 1)];
          break;
        case "ArrowUp":
          target = rows[r - 1] && rows[r - 1][Math.min(c, rows[r - 1].length - 1)];
          break;
        case "Home":
          target = event.ctrlKey || event.metaKey ? rows[0][0] : rows[r][0];
          break;
        case "End":
          target = event.ctrlKey || event.metaKey
            ? rows[rows.length - 1][rows[rows.length - 1].length - 1]
            : rows[r][rows[r].length - 1];
          break;
        case "PageDown":
          target = (rows[Math.min(r + 10, rows.length - 1)] || [])[c];
          break;
        case "PageUp":
          target = (rows[Math.max(r - 10, 0)] || [])[c];
          break;
        default:
          return;
      }
      if (!target) return;
      event.preventDefault();
      this.focusCell(target, true);
    },
    focusCell(cell, move) {
      this.cells().forEach((other) => {
        other.tabIndex = other === cell ? 0 : -1;
      });
      if (move) cell.focus();
    },

    // --- column visibility, order and width -------------------------------

    setColumnHidden(key, hidden) {
      const index = this.columnIndex(key);
      if (index === -1) return;
      Array.from(this.table.rows).forEach((row) => {
        const cell = row.cells[index];
        if (cell) cell.hidden = hidden;
      });
      // A hidden cell must lose its tab stop, or focus lands somewhere the user
      // cannot see.
      this.cells().forEach((cell, i) => {
        cell.tabIndex = i === 0 ? 0 : -1;
      });
    },
    columnIndex(key) {
      const headers = Array.from(this.table.querySelectorAll("thead th"));
      return headers.findIndex((th) => th.dataset.gridColumn === key);
    },

    startResize(event) {
      const header = event.target.closest("th[data-grid-resizable='true']");
      if (!header) return;
      // Only the last few pixels are the resize handle; the rest of the header
      // is the sort link, which must keep working.
      const box = header.getBoundingClientRect();
      if (box.right - event.clientX > 8) return;

      event.preventDefault();
      const startX = event.clientX;
      const startWidth = box.width;
      const floor = parseInt(header.dataset.gridMinWidth || "", 10) || 48;

      const move = (moveEvent) => {
        // Clamped: a column dragged to zero is data the user can no longer read.
        const width = Math.max(floor, startWidth + (moveEvent.clientX - startX));
        header.style.width = width + "px";
      };
      const up = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", up);
        this.storePreferences();
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", up);
    },

    // --- preferences ------------------------------------------------------

    // Column choices are this user's, on this device, so they live in
    // localStorage rather than the server. Losing them is harmless; sending
    // them to the server would make a display preference look like data.
    storePreferences() {
      const hidden = [];
      const widths = {};
      Array.from(this.table.querySelectorAll("thead th")).forEach((th) => {
        const key = th.dataset.gridColumn;
        if (!key) return;
        if (th.hidden) hidden.push(key);
        if (th.style.width) widths[key] = th.style.width;
      });
      try {
        localStorage.setItem(this.key, JSON.stringify({ hidden, widths }));
      } catch (e) {
        // A full or blocked storage must not break the grid.
      }
    },
    applyStoredPreferences() {
      let saved = null;
      try {
        saved = JSON.parse(localStorage.getItem(this.key) || "null");
      } catch (e) {
        saved = null;
      }
      if (!saved) return;
      (saved.hidden || []).forEach((key) => {
        this.setColumnHidden(key, true);
        const box = this.root.querySelector(`[data-grid-toggle-column="${key}"]`);
        if (box) box.checked = false;
      });
      Object.entries(saved.widths || {}).forEach(([key, width]) => {
        const index = this.columnIndex(key);
        const header = this.table.querySelectorAll("thead th")[index];
        if (header) header.style.width = width;
      });
    },

    // --- windowed fetching ------------------------------------------------

    // Fetching the next window on scroll is additive. The pager stays in the
    // page, so this never becomes the only route to later rows.
    watchScroll() {
      const url = this.root.dataset.gridRowsUrl;
      const rows = this.root.querySelector("[data-grid-rows]");
      if (!url || !rows || typeof IntersectionObserver !== "function") return;

      const total = parseInt(this.root.dataset.gridTotal || "0", 10);
      const sentinel = document.createElement("tr");
      sentinel.innerHTML = '<td aria-hidden="true"></td>';
      rows.appendChild(sentinel);

      this.observer = new IntersectionObserver((entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return;
        if (rows.querySelectorAll("tr[data-grid-row]").length >= total) {
          this.observer.disconnect();
          return;
        }
        if (this.pending) return;
        this.pending = true;
        const offset = rows.querySelectorAll("tr[data-grid-row]").length;
        // htmx owns the request so the swap follows the same rules as every
        // other fragment in the product.
        window.htmx
          .ajax("GET", url + (url.includes("?") ? "&" : "?") + "offset=" + offset, {
            target: rows,
            swap: "beforeend",
          })
          .then(() => {
            this.pending = false;
            rows.appendChild(sentinel);
            this.announce(rows.querySelectorAll("tr[data-grid-row]").length, total);
          });
      });
      this.observer.observe(sentinel);
    },
    announce(loaded, total) {
      const status = this.root.querySelector("[data-grid-status]");
      if (status) status.textContent = `Showing ${loaded} of ${total} rows`;
    },

    destroy() {
      this.table.removeEventListener("keydown", this._onKey);
      this.table.removeEventListener("focusin", this._onFocus);
      this.table.removeEventListener("pointerdown", this._onPointerDown);
      document.removeEventListener("change", this._onToggle);
      if (this.observer) this.observer.disconnect();
    },
  }));
});
