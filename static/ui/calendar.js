// Calendar adapter, owned by component/calendar.
//
// The native input is the control and stays authoritative: visible, named,
// constrained, and submitted. Cally supplies only the calendar inside a
// first-party popover, and it writes *into* that input. With no JavaScript the
// user types a date and the form works; the platform already supplies the
// format, the keyboard entry and the mobile date wheel.
//
// Three details here are not obvious and each caused a real bug class:
//
//   - Cally's `change` does not bubble. Listening on a container never fires, so
//     the handler is attached to the calendar element itself.
//   - Writing an input's value programmatically fires no event, so htmx and any
//     listener see nothing. The adapter dispatches `input` then `change`, in
//     that order, exactly as a user edit would.
//   - Syncing both ways invites a loop: calendar writes input, input notifies
//     calendar, calendar writes input. A guard flag breaks it, because comparing
//     values is not enough once a reset makes both sides equal-but-stale.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiCalendar", () => ({
    init() {
      const root = this.$root;
      root.addEventListener("ui:engine-ready", () => this.enhance(), { once: true });
      // On failure there is nothing to do: the native input is already the
      // control, and the trigger stays hidden because it would open nothing.
    },
    enhance() {
      const root = this.$root;
      const trigger = root.querySelector("[data-calendar-trigger]");
      const popover = root.querySelector("[data-calendar-popover]");
      if (!trigger || !popover) return;

      const kind = root.dataset.calendarKind || "date";
      const calendar = buildCalendar(root, kind);
      if (!calendar) return;
      popover.appendChild(calendar);

      // Cally's change does not bubble, so this must be on the element.
      calendar.addEventListener("change", () => {
        if (root.dataset.calendarSyncing === "true") return;
        writeToInputs(root, kind, calendar);
      });

      inputsOf(root, kind).forEach((input) => {
        input.addEventListener("change", () => {
          if (root.dataset.calendarSyncing === "true") return;
          syncFromInputs(root, kind, calendar);
        });
      });

      trigger.hidden = false;
      trigger.addEventListener("click", () => {
        const open = popover.hidden;
        popover.hidden = !open;
        trigger.setAttribute("aria-expanded", open ? "true" : "false");
        if (open) {
          syncFromInputs(root, kind, calendar);
          const focusable = calendar.shadowRoot
            ? calendar
            : calendar.querySelector("button, [tabindex]");
          if (focusable && focusable.focus) focusable.focus();
        }
      });

      this._onKey = (event) => {
        if (event.key !== "Escape" || popover.hidden) return;
        popover.hidden = true;
        trigger.setAttribute("aria-expanded", "false");
        // Focus returns to the trigger: a keyboard user who dismisses a popover
        // and lands nowhere has no way back.
        trigger.focus();
      };
      root.addEventListener("keydown", this._onKey);
      syncFromInputs(root, kind, calendar);
    },
    destroy() {
      const root = this.$root;
      if (this._onKey) root.removeEventListener("keydown", this._onKey);
      const popover = root.querySelector("[data-calendar-popover]");
      // The custom element is removed explicitly. Cally's disconnect callback
      // does the rest, and leaving it attached to a detached input is how a
      // calendar ends up writing into a field that is no longer on the page.
      if (popover) popover.replaceChildren();
    },
  }));
});

function buildCalendar(root, kind) {
  const tag = kind === "range" ? "calendar-range" : "calendar-date";
  if (!window.customElements || !window.customElements.get(tag)) return null;
  const calendar = document.createElement(tag);
  const locale = root.dataset.calendarLocale;
  if (locale) calendar.setAttribute("locale", locale);
  const firstDay = root.dataset.calendarFirstDay;
  if (firstDay) calendar.setAttribute("first-day-of-week", firstDay);
  const disabled = root.dataset.calendarDisabled;
  if (disabled) calendar.setAttribute("disallowed", disabled.split(",").join(" "));

  // min and max come from the native input, so the calendar cannot offer a date
  // the field itself would reject. The server validates them again regardless.
  const primary = inputsOf(root, kind)[0];
  if (primary) {
    if (primary.min) calendar.setAttribute("min", datePart(primary.min));
    if (primary.max) calendar.setAttribute("max", datePart(primary.max));
  }
  const month = document.createElement("calendar-month");
  calendar.appendChild(month);
  return calendar;
}

function inputsOf(root, kind) {
  if (kind === "range") {
    return [
      root.querySelector("[data-range-start]"),
      root.querySelector("[data-range-end]"),
    ].filter(Boolean);
  }
  return Array.from(root.querySelectorAll('input[type="date"], input[type="datetime-local"]'));
}

// writeToInputs is the only place values reach the native fields, and it always
// notifies: a programmatic value assignment fires no event, so htmx and every
// listener would otherwise never learn the value changed.
function writeToInputs(root, kind, calendar) {
  const inputs = inputsOf(root, kind);
  if (!inputs.length) return;
  root.dataset.calendarSyncing = "true";
  try {
    if (kind === "range") {
      const [start, end] = String(calendar.value || "").split("/");
      if (inputs[0] && start) setValue(inputs[0], start);
      if (inputs[1] && end) setValue(inputs[1], end);
    } else if (kind === "datetime") {
      const date = String(calendar.value || "");
      const existing = String(inputs[0].value || "");
      const time = existing.includes("T") ? existing.split("T")[1] : "";
      // Nothing is written until date *and* time exist. A guessed midnight is a
      // wrong answer that looks like a right one, and the user never chose it.
      if (date && time) setValue(inputs[0], date + "T" + time);
    } else {
      const date = String(calendar.value || "");
      if (date) setValue(inputs[0], date);
    }
  } finally {
    root.dataset.calendarSyncing = "false";
  }
}

function setValue(input, value) {
  if (input.value === value) return;
  input.value = value;
  // input then change, in the order a user edit produces them.
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

// syncFromInputs pushes the field's value back into the calendar, so a typed
// date or a form reset moves the highlighted day. The guard keeps this from
// bouncing back through writeToInputs.
function syncFromInputs(root, kind, calendar) {
  const inputs = inputsOf(root, kind);
  if (!inputs.length) return;
  root.dataset.calendarSyncing = "true";
  try {
    if (kind === "range") {
      const start = inputs[0] ? datePart(inputs[0].value) : "";
      const end = inputs[1] ? datePart(inputs[1].value) : "";
      calendar.value = start && end ? start + "/" + end : "";
    } else {
      calendar.value = datePart(inputs[0].value);
    }
  } finally {
    root.dataset.calendarSyncing = "false";
  }
}

// datePart drops the time half. A datetime-local value is "YYYY-MM-DDTHH:MM"
// and the calendar only understands the date.
function datePart(value) {
  const text = String(value || "");
  return text.includes("T") ? text.split("T")[0] : text;
}
