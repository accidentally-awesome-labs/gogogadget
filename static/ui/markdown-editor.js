// Markdown editor controller, owned by component/markdown-editor.
//
// The textarea is the form value and this file only edits its text. There is no
// WYSIWYG engine and no client-side Markdown renderer, deliberately: a rich-text
// editor stores HTML, which is exactly what the server refuses to trust, and a
// second Markdown renderer would be a second set of escaping rules disagreeing
// with goldmark. The preview comes from the server.
//
// The one genuinely tricky requirement is undo. Assigning `textarea.value`
// wipes the browser's native undo stack, so Ctrl+Z after a toolbar click would
// throw away the author's typing instead of the formatting. `insertText` via
// execCommand is the only way to edit a textarea *through* the browser's own
// history, which is why a deprecated API is the right choice here - and there is
// a documented fallback for engines that refuse it.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiMarkdownEditor", () => ({
    init() {
      const root = this.$root;
      const input = root.querySelector("[data-editor-input]");
      const toolbar = root.querySelector("[data-editor-toolbar]");
      const media = root.querySelector("[data-editor-media]");
      if (!input || !toolbar) return;

      // Revealed only now: the buttons do nothing without this controller, and
      // a row of inert buttons is worse than none. The media panel is the same
      // bargain - its insert buttons write at the caret, which nothing but this
      // controller can do.
      toolbar.hidden = false;
      if (media) media.hidden = false;

      this._onClick = (event) => {
        // Anywhere inside this editor, not only the toolbar: the media panel is
        // swapped into the root and its buttons insert into this textarea.
        const button = event.target.closest("[data-editor-action]");
        if (!button || !root.contains(button)) return;
        this.run(input, button, root);
      };
      root.addEventListener("click", this._onClick);

      this._onKey = (event) => {
        if (!(event.metaKey || event.ctrlKey)) return;
        const shortcut = { b: ["**", "**"], i: ["_", "_"], k: ["[", "](url)"] }[
          event.key.toLowerCase()
        ];
        if (!shortcut) return;
        event.preventDefault();
        wrapSelection(input, shortcut[0], shortcut[1]);
      };
      input.addEventListener("keydown", this._onKey);

      // Paste and drop insert a Markdown image reference rather than uploading
      // silently: the author names the file, and an upload nobody asked for is
      // a surprise request.
      this._onPaste = (event) => {
        const url = (event.clipboardData && event.clipboardData.getData("text/uri-list")) || "";
        if (!/^https?:\/\/\S+\.(png|jpe?g|gif|webp|svg)$/i.test(url)) return;
        event.preventDefault();
        insert(input, "![](" + url + ")");
      };
      input.addEventListener("paste", this._onPaste);
    },
    run(input, button, root) {
      const action = button.dataset.editorAction;
      switch (action) {
        case "wrap":
          wrapSelection(input, button.dataset.editorPrefix || "", button.dataset.editorSuffix || "");
          return;
        case "line":
          prefixLines(input, button.dataset.editorPrefix || "");
          return;
        case "insert":
          insert(input, button.dataset.editorInsert || "");
          return;
        case "split":
          toggleSplit(root, button);
          return;
        default:
          // "media" is handled by htmx on the button itself; nothing to do.
          return;
      }
    },
    destroy() {
      const root = this.$root;
      const input = root.querySelector("[data-editor-input]");
      root.removeEventListener("click", this._onClick);
      if (input) {
        input.removeEventListener("keydown", this._onKey);
        input.removeEventListener("paste", this._onPaste);
      }
    },
  }));
});

// insert writes text at the caret through the browser's own editing history, so
// Ctrl+Z undoes the insertion and leaves the author's typing intact. Assigning
// .value would clear that history entirely.
function insert(input, text) {
  input.focus();
  // insertText is a real edit, so the browser fires its own input event and
  // htmx already sees it. Dispatching another would double every preview
  // request and double-count every change listener.
  if (document.execCommand && document.execCommand("insertText", false, text)) return;

  // Fallback for engines that reject execCommand. Undo is lost on this path -
  // measured: after assigning a value, undo does nothing at all - so it is the
  // fallback, never the route.
  input.setRangeText(text, input.selectionStart, input.selectionEnd, "end");
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

// wrapSelection surrounds the selection, or inserts the markers and places the
// caret between them when nothing is selected - so a bold button with no
// selection starts bold text rather than emitting four stray asterisks.
function wrapSelection(input, prefix, suffix) {
  const start = input.selectionStart;
  const end = input.selectionEnd;
  const selected = input.value.slice(start, end);
  insert(input, prefix + selected + suffix);
  if (selected === "") {
    const caret = start + prefix.length;
    input.setSelectionRange(caret, caret);
  } else {
    input.setSelectionRange(start + prefix.length, start + prefix.length + selected.length);
  }
}

// prefixLines prepends a marker to every selected line. Toggling is deliberate:
// clicking the list button twice removes the markers rather than nesting them,
// because nesting is what the author would have to undo by hand.
function prefixLines(input, prefix) {
  const value = input.value;
  const start = value.lastIndexOf("\n", input.selectionStart - 1) + 1;
  const endBreak = value.indexOf("\n", input.selectionEnd);
  const end = endBreak === -1 ? value.length : endBreak;
  const block = value.slice(start, end);
  const lines = block.split("\n");
  const allPrefixed = lines.every((line) => line.startsWith(prefix));
  const next = lines
    .map((line) => (allPrefixed ? line.slice(prefix.length) : prefix + line))
    .join("\n");

  input.focus();
  input.setSelectionRange(start, end);
  insert(input, next);
  input.setSelectionRange(start, start + next.length);
}

// toggleSplit switches between stacked and side-by-side panes. aria-pressed
// carries the state, because the layout change is invisible to a screen reader
// and the button is a toggle either way.
function toggleSplit(root, button) {
  const panes = root.querySelector("[data-editor-panes]");
  if (!panes) return;
  const on = button.getAttribute("aria-pressed") !== "true";
  button.setAttribute("aria-pressed", on ? "true" : "false");
  panes.classList.toggle("md:grid-cols-2", on);
}
