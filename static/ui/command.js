// Command palette controller, owned by component/command.
//
// The markup is a search form inside a native dialog, and every command is a
// link. With no script the trigger is inert but the form still submits to the
// fallback search page, and the commands are still links. This file adds the
// dialog, the shortcut, and the combobox keyboard model.
//
// The combobox roles are applied here rather than rendered in the markup on
// purpose: an input claiming role="combobox" with no active descendant and no
// key handling is worse for a screen-reader user than a plain search field,
// because it promises a listbox that never responds.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiCommand", () => ({
    init() {
      const root = this.$root;
      const dialog = root.querySelector("[data-command-dialog]");
      const input = root.querySelector("[data-command-input]");
      const results = root.querySelector("[data-command-results]");
      if (!dialog || !input || !results) return;

      this.root = root;
      this.dialog = dialog;
      this.input = input;
      this.results = results;

      // Now the roles are real: the handlers below implement them.
      results.setAttribute("role", "listbox");
      results.id = results.id || "cmd-results";
      input.setAttribute("role", "combobox");
      input.setAttribute("aria-expanded", "true");
      input.setAttribute("aria-controls", results.id);
      input.setAttribute("aria-autocomplete", "list");
      this.markOptions();

      this._onOpen = (event) => {
        if (!event.target.closest("[data-command-open]")) return;
        event.preventDefault();
        this.open();
      };
      root.addEventListener("click", this._onOpen);

      this._onShortcut = (event) => {
        if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;
        event.preventDefault();
        this.open();
      };
      document.addEventListener("keydown", this._onShortcut);

      this._onKey = (event) => this.onKey(event);
      input.addEventListener("keydown", this._onKey);

      // Results arrive from the server, so the options are re-marked after
      // every swap - otherwise aria-activedescendant points at a removed node.
      this._onSwap = () => {
        this.markOptions();
        this.activate(null);
      };
      results.addEventListener("htmx:afterSwap", this._onSwap);
    },

    open() {
      if (typeof this.dialog.showModal === "function") this.dialog.showModal();
      else this.dialog.setAttribute("open", "");
      // Re-marked on open: results may have been swapped in since init, and a
      // listbox whose children lost their option role is structurally invalid.
      this.markOptions();
      this.input.focus();
      this.input.select();
    },

    // options are every command, visible or not. What an element *is* does not
    // depend on whether it is on screen: filtering by visibility here meant
    // markOptions() assigned no roles at all when it first ran, because the
    // palette's dialog is closed and display:none makes every offsetParent
    // null - leaving a listbox whose children were plain anchors.
    options() {
      return Array.from(this.results.querySelectorAll("[data-command-item]"));
    },

    // visibleOptions is what the arrow keys walk. Visibility belongs here, in
    // the navigation path, not in the role assignment.
    visibleOptions() {
      return this.options().filter((item) => item.offsetParent !== null);
    },

    // markOptions gives each command the option role and a stable id, which is
    // what aria-activedescendant needs to name.
    markOptions() {
      this.options().forEach((item, index) => {
        item.setAttribute("role", "option");
        item.setAttribute("aria-selected", "false");
        if (!item.id) item.id = this.results.id + "-opt-" + index;
        // Focus stays in the input for a combobox, so the options must not be
        // separate tab stops.
        item.tabIndex = -1;
      });
      // The wrapping markup sits between the listbox and its options, and a
      // listbox child may only be an option or a group.
      //
      // The group's ul becomes role="group" rather than presentation: it carries
      // aria-labelledby, and presentation is ignored on any element with a
      // global ARIA attribute - so it fell back to its implicit "list" role and
      // made the listbox structurally invalid. A group is a legal child and
      // keeps the section name.
      this.results.querySelectorAll("ul").forEach((list) => {
        list.setAttribute("role", "group");
      });
      // The group's own wrapper is a direct child of the listbox, and a listbox
      // child may only be an option or a group. It carries no ARIA of its own,
      // so presentation applies and it dissolves, promoting the ul.
      this.results.querySelectorAll('[data-ui="command-group"]').forEach((group) => {
        group.setAttribute("role", "presentation");
      });
      // The list items carry no ARIA of their own, so presentation applies and
      // they drop out of the tree as intended.
      this.results.querySelectorAll("li").forEach((item) => {
        item.setAttribute("role", "presentation");
      });
    },

    activate(item) {
      this.options().forEach((option) => {
        option.setAttribute("aria-selected", option === item ? "true" : "false");
        option.classList.toggle("bg-surface-raised", option === item);
      });
      if (item) {
        this.input.setAttribute("aria-activedescendant", item.id);
        item.scrollIntoView({ block: "nearest" });
      } else {
        this.input.removeAttribute("aria-activedescendant");
      }
      this.active = item || null;
    },

    onKey(event) {
      const options = this.visibleOptions();
      if (!options.length) return;
      const index = this.active ? options.indexOf(this.active) : -1;

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          this.activate(options[Math.min(index + 1, options.length - 1)]);
          return;
        case "ArrowUp":
          event.preventDefault();
          // Up from the first option releases the selection rather than
          // wrapping: the user is heading back to what they typed.
          this.activate(index <= 0 ? null : options[index - 1]);
          return;
        case "Home":
          event.preventDefault();
          this.activate(options[0]);
          return;
        case "End":
          event.preventDefault();
          this.activate(options[options.length - 1]);
          return;
        case "Enter":
          // With nothing highlighted, Enter submits the search form - which is
          // the same thing it does with no script at all.
          if (!this.active) return;
          event.preventDefault();
          this.active.click();
          return;
        default:
          return;
      }
    },

    destroy() {
      this.root.removeEventListener("click", this._onOpen);
      document.removeEventListener("keydown", this._onShortcut);
      this.input.removeEventListener("keydown", this._onKey);
      this.results.removeEventListener("htmx:afterSwap", this._onSwap);
    },
  }));
});
