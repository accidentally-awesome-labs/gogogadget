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

  // Dismissible dashboard checklist (persisted per browser).
  Alpine.data("checklist", function () {
    return {
      dismissed: localStorage.getItem("gg_checklist_dismissed") === "1",
      dismiss: function () {
        this.dismissed = true;
        localStorage.setItem("gg_checklist_dismissed", "1");
      },
    };
  });

  // SelectOrg page: switch the active org via clerk-js, then reload.
  Alpine.data("selectOrg", function () {
    return {
      choose: function (orgId) {
        if (!window.Clerk) return;
        window.Clerk.setActive({ organization: orgId }).then(function () {
          window.location.assign("/app");
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

// --- Clerk bootstrap (load-bearing) ---
// clerk-js owns the __session JWT refresh cycle (~60s tokens). Without this,
// auth expires ~60s after login. The publishable key arrives via a meta tag;
// e2e/test envs leave it empty and skip clerk-js entirely.
window.addEventListener("DOMContentLoaded", function () {
  var meta = document.querySelector('meta[name="clerk-publishable-key"]');
  if (!meta || !window.Clerk) return;
  window.Clerk.load().then(function () {
    var clerk = window.Clerk;
    if (!clerk.user) return;
    var ub = document.getElementById("user-button");
    if (ub) clerk.mountUserButton(ub);
    var os = document.getElementById("org-switcher");
    if (os) clerk.mountOrganizationSwitcher(os);
  });
});
