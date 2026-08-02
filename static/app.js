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

// --- htmx config ---
// htmx 2.x does not swap on 4xx/5xx by default. Our conventions rely on two
// exception statuses: 422 (re-rendered form fragments) and 503 (not-configured
// fragments). htmx loads deferred after this file, so configure on
// DOMContentLoaded when the htmx global exists.
window.addEventListener("DOMContentLoaded", function () {
  if (!window.htmx) return;
  window.htmx.config.responseHandling = [
    { code: "204", swap: false },
    { code: "[23]..", swap: true },
    { code: "422", swap: true }, // validation fragments
    { code: "503", swap: true }, // not-configured fragments
    { code: "[45]..", swap: false, error: true },
  ];
});

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
  // event. flash:true toasts persist to sessionStorage and render after the
  // HX-Redirect navigation that follows; plain toasts render immediately.
  Alpine.data("toastRoot", function () {
    return {
      toasts: [],
      push: function (type, message) {
        var self = this;
        var t = { id: Date.now() + Math.random(), type: type || "info", message: message || "" };
        this.toasts.push(t);
        setTimeout(function () {
          self.toasts = self.toasts.filter(function (x) { return x.id !== t.id; });
        }, 5000);
      },
      init: function () {
        var self = this;
        var pending = sessionStorage.getItem("gg_flash");
        if (pending) {
          sessionStorage.removeItem("gg_flash");
          try {
            var d = JSON.parse(pending);
            self.push(d.type, d.message);
          } catch (e) {}
        }
        document.addEventListener("toast", function (e) {
          var d = e.detail || {};
          if (d.flash) {
            sessionStorage.setItem("gg_flash", JSON.stringify({ type: d.type, message: d.message }));
          } else {
            self.push(d.type, d.message);
          }
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
