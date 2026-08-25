// Alpine fragment owned by the progressively enhanced form controls.
//
// Every control here works without this file: the slug is a text input the user
// can type, the tags field is a comma-separated string, the dropzone is a real
// file input inside a label, the date range is two native date inputs, and the
// character limit is enforced by maxlength and by the server. This file only
// adds convenience on top, which is why nothing below is load-bearing.
document.addEventListener("alpine:init", () => {
  // uiCharCounter reports how much of a length limit is used. It reads maxlength
  // from the watched control rather than taking it twice, so the readout can
  // never disagree with the limit the browser enforces.
  Alpine.data("uiCharCounter", () => ({
    count: 0,
    init() {
      const name = this.$root.dataset.for;
      const input = name && document.getElementById(name);
      if (!input) return;
      this.count = (input.value || "").length;
      this._onInput = () => {
        this.count = (input.value || "").length;
      };
      input.addEventListener("input", this._onInput);
      this._input = input;
    },
    destroy() {
      if (this._input && this._onInput) {
        this._input.removeEventListener("input", this._onInput);
      }
    },
  }));

  // uiSlug mirrors a source field until the user edits the slug directly.
  // Mirroring stops permanently on first edit: silently overwriting something
  // the user typed is worse than not helping.
  Alpine.data("uiSlug", () => ({
    touched: false,
    init() {
      const source = document.getElementById(this.$root.dataset.from || "");
      this.touched = (this.$root.value || "") !== "";
      this._onEdit = () => {
        this.touched = true;
      };
      this.$root.addEventListener("input", this._onEdit);
      if (!source) return;
      this._onSource = () => {
        if (this.touched) return;
        this.$root.value = slugify(source.value || "");
      };
      source.addEventListener("input", this._onSource);
      this._source = source;
    },
    destroy() {
      this.$root.removeEventListener("input", this._onEdit);
      if (this._source && this._onSource) {
        this._source.removeEventListener("input", this._onSource);
      }
    },
  }));

  // uiTags normalizes the separator on blur so "a,b ,  c" round-trips as
  // "a, b, c". The value stays one string, so the field submits unchanged.
  Alpine.data("uiTags", () => ({
    init() {
      this._onBlur = () => {
        const parts = (this.$root.value || "")
          .split(",")
          .map((part) => part.trim())
          .filter((part) => part !== "");
        this.$root.value = parts.join(", ");
      };
      this.$root.addEventListener("blur", this._onBlur);
    },
    destroy() {
      this.$root.removeEventListener("blur", this._onBlur);
    },
  }));

  // uiDropzone adds drag-and-drop to a real file input. The input keeps working
  // by click and keyboard; this only assigns dropped files to it, so the form
  // submits through exactly the same control either way.
  Alpine.data("uiDropzone", () => ({
    over: false,
    init() {
      const input = this.$root.querySelector('input[type="file"]');
      if (!input) return;
      const stop = (event) => {
        event.preventDefault();
        event.stopPropagation();
      };
      this._enter = (event) => {
        stop(event);
        this.over = true;
        this.$root.classList.add("border-brand");
      };
      this._leave = (event) => {
        stop(event);
        this.over = false;
        this.$root.classList.remove("border-brand");
      };
      this._drop = (event) => {
        stop(event);
        this.over = false;
        this.$root.classList.remove("border-brand");
        if (event.dataTransfer && event.dataTransfer.files.length) {
          input.files = event.dataTransfer.files;
          input.dispatchEvent(new Event("change", { bubbles: true }));
        }
      };
      this.$root.addEventListener("dragover", this._enter);
      this.$root.addEventListener("dragleave", this._leave);
      this.$root.addEventListener("drop", this._drop);
    },
    destroy() {
      this.$root.removeEventListener("dragover", this._enter);
      this.$root.removeEventListener("dragleave", this._leave);
      this.$root.removeEventListener("drop", this._drop);
    },
  }));

  // uiDateRange keeps the end date from preceding the start date in the picker.
  // The server validates the ordering regardless: a constraint that exists only
  // because the browser enforced it is not a constraint.
  Alpine.data("uiDateRange", () => ({
    init() {
      const start = this.$root.querySelector("[data-range-start]");
      const end = this.$root.querySelector("[data-range-end]");
      if (!start || !end) return;
      this._sync = () => {
        end.min = start.value || "";
        if (end.value && start.value && end.value < start.value) {
          end.value = start.value;
        }
      };
      this._sync();
      start.addEventListener("change", this._sync);
      this._start = start;
    },
    destroy() {
      if (this._start && this._sync) {
        this._start.removeEventListener("change", this._sync);
      }
    },
  }));
});

// slugify is the same transform the server applies, kept deliberately simple:
// lowercase, non-alphanumerics to hyphens, no leading or trailing hyphen. The
// server slug is authoritative, so a disagreement here costs a corrected value,
// not a broken one.
function slugify(value) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
