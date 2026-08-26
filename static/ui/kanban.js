// Kanban drag adapter, owned by component/kanban.
//
// Dragging is a shortcut, not the interface. Every card already carries a
// keyboard-operable "Move to..." menu that posts to the same endpoint, and this
// file adds a pointer gesture on top of it. That ordering is the whole design:
// a board operable only by dragging excludes keyboard users, screen reader
// users, and anyone on a touch screen where a drag competes with scrolling.
//
// The drop therefore does not invent a request. It fills in the card's own move
// form and submits it, so both paths hit one endpoint with one payload shape and
// cannot drift apart. The server's response is authoritative: it re-renders the
// board, which is what reverts a move it rejected.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiKanban", () => ({
    init() {
      const root = this.$root;
      this.root = root;
      this.instances = [];

      // Sortable arrives lazily. The board is already fully operable through
      // the card menus, so this waits for the engine instead of failing - and if
      // the engine never loads, nothing is lost but the shortcut.
      this._onEngine = () => this.attach();
      root.addEventListener("ui:engine-ready", this._onEngine, { once: true });
      // Already loaded from an earlier board on this page.
      if (window.Sortable) this.attach();
    },

    attach() {
      const Sortable = window.Sortable;
      if (!Sortable || this.instances.length) return;

      // Respect the user's motion setting: an animated card flight is
      // decoration, and for someone who gets motion sick it is harm.
      const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

      this.root.querySelectorAll("[data-kanban-list]").forEach((list) => {
        this.instances.push(
          new Sortable(list, {
            group: this.root.id || "kanban",
            animation: reduced ? 0 : 150,
            ghostClass: "opacity-50",
            // The card is the handle. A separate grip would be one more thing
            // to find, and the menu is already the precise path.
            draggable: "[data-kanban-card]",
            onEnd: (event) => this.submit(event),
          }),
        );
      });
    },

    // submit fills the card's own move form and sends it. Building a fetch here
    // would be a second implementation of the move, free to disagree with the
    // menu about parameter names, target, or swap style.
    submit(event) {
      const card = event.item;
      const form = card.querySelector("[data-kanban-form]");
      const to = event.to.closest("[data-kanban-column]");
      const from = event.from.closest("[data-kanban-column]");
      if (!form || !to) return;

      // A drop back where it started is not a move. Sending it would make the
      // server re-render for nothing and flash the board.
      if (to === from && event.oldIndex === event.newIndex) return;

      form.querySelector("[data-kanban-to]").value = to.dataset.kanbanColumn;
      const position = form.querySelector("[data-kanban-position]");
      if (position) position.value = String(event.newIndex);

      // htmx owns the request, so the response swaps under the same rules as
      // every other fragment - including the rejection path.
      if (window.htmx) {
        window.htmx.trigger(form, "submit");
      } else {
        form.submit();
      }
    },

    destroy() {
      this.root.removeEventListener("ui:engine-ready", this._onEngine);
      // Sortable installs document-level listeners per instance; leaving them
      // attached after a swap means every navigation adds another board's worth
      // of handlers.
      this.instances.forEach((instance) => instance.destroy());
      this.instances = [];
    },
  }));
});
