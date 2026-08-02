// GoGoGadget client runtime. Everything lives here because the CSP is
// script-src 'self': no inline scripts, no inline Alpine expressions.
// This file is loaded WITHOUT defer so theme init runs pre-paint.

// --- Theme init (pre-paint, prevents dark-mode flash) ---
(function () {
  var t = localStorage.getItem("theme");
  if (t === "dark" || (!t && window.matchMedia("(prefers-color-scheme: dark)").matches)) {
    document.documentElement.classList.add("dark");
  }
})();

// --- Alpine CSP components (registered before Alpine boots) ---
document.addEventListener("alpine:init", function () {
  Alpine.data("themeToggle", function () {
    return {
      dark: document.documentElement.classList.contains("dark"),
      toggle: function () {
        this.dark = !this.dark;
        document.documentElement.classList.toggle("dark", this.dark);
        localStorage.setItem("theme", this.dark ? "dark" : "light");
      },
    };
  });

  Alpine.data("dropdown", function () {
    return {
      open: false,
      toggle: function () { this.open = !this.open; },
      close: function () { this.open = false; },
    };
  });

  Alpine.data("modal", function () {
    return {
      open: false,
      show: function () { this.open = true; },
      hide: function () { this.open = false; },
    };
  });

  Alpine.data("tabs", function () {
    return {
      active: 0,
      set: function (i) { this.active = i; },
      is: function (i) { return this.active === i; },
    };
  });

  Alpine.data("mobileNav", function () {
    return {
      open: false,
      toggle: function () { this.open = !this.open; },
      close: function () { this.open = false; },
    };
  });

  Alpine.data("clipboard", function () {
    return {
      copied: false,
      copy: function (text) {
        var self = this;
        navigator.clipboard.writeText(text).then(function () {
          self.copied = true;
          setTimeout(function () { self.copied = false; }, 2000);
        });
      },
    };
  });

  // Toast root: htmx HX-Trigger {"toast": {...}} dispatches a bubbling "toast"
  // event; we render and auto-dismiss.
  Alpine.data("toastRoot", function () {
    return {
      toasts: [],
      init: function () {
        var self = this;
        document.addEventListener("toast", function (e) {
          var d = e.detail || {};
          var t = { id: Date.now() + Math.random(), type: d.type || "info", message: d.message || "" };
          self.toasts.push(t);
          setTimeout(function () {
            self.toasts = self.toasts.filter(function (x) { return x.id !== t.id; });
          }, 5000);
        });
      },
    };
  });
});
