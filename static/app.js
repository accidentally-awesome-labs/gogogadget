// GoGoGadget client runtime. Everything lives here because the CSP is
// script-src 'self': no inline scripts, no inline Alpine expressions.
// This file is loaded WITHOUT defer so theme init runs pre-paint.

// --- Theme init (pre-paint, prevents dark-mode flash) ---
// Order matters: an explicit light/dark in the `theme` cookie is what the
// SERVER knows (it mirrors the signed-in user's saved preference, so a fresh
// device is already correct on first paint); "system" means the account made
// no choice, so this browser's localStorage wins; the OS setting is the last
// resort. The server renders class="dark" itself when it can — this runs for
// the cases it cannot answer, and keeps static/cached pages honest.
(function () {
  function cookie(name) {
    var m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
    return m ? decodeURIComponent(m[1]) : "";
  }
  var c = cookie("theme");
  // "system" in the cookie means the ACCOUNT has no explicit preference, so a
  // per-browser choice is more specific and wins. Only an explicit light/dark
  // from the server outranks localStorage.
  var t = c === "light" || c === "dark" ? c : localStorage.getItem("theme") || "system";
  var dark = t === "dark" || (t === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
})();

// --- Theme persistence ---
// A plain fetch rather than htmx: htmx.ajax's `source` option silently drops
// the request under htmx 4, and this call has no swap to perform anyway. The
// CSRF token comes from the same body attribute every hx-post inherits, so
// there is exactly one token in the page.
function persistTheme(theme) {
  var raw = document.body.getAttribute("hx-headers:inherited") || "{}";
  var token = "";
  try {
    token = JSON.parse(raw)["X-CSRF-Token"] || "";
  } catch (e) {
    token = "";
  }
  fetch("/set-theme", {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      "X-CSRF-Token": token,
    },
    body: "theme=" + encodeURIComponent(theme),
    credentials: "same-origin",
  }).catch(function () {
    // A failed write only costs cross-device persistence; the local flip and
    // the localStorage copy already keep THIS browser correct.
  });
}

// --- Navigation progress ---
//
// Boosted navigation swaps #content, which means the old page sits there
// untouched until the response lands: on anything slower than a local
// network, a click looked like nothing happened. htmx marks the *triggering
// element* with .htmx-request, which is right for a button's spinner and
// useless for a whole-page change.
//
// This flags the document while a navigation is in flight and lets CSS draw
// the feedback (see input.css). The bar is delayed there, so fast swaps —
// the common case — never flash it.
//
// The event names and payload shape are htmx 4's: colon-namespaced events
// (htmx:before:request, not htmx:beforeRequest) whose detail carries a single
// `ctx`, with the resolved swap target on ctx.target. htmx:finally:request
// always fires — success, error or abort — so the counter cannot get stuck.
(function () {
  var inFlight = 0;

  function isNavigation(evt) {
    var ctx = (evt.detail || {}).ctx;
    var target = ctx && ctx.target;
    // A navigation is a request that replaces the content box itself; the
    // badge fragment and in-page table swaps target something smaller.
    return !!target && target.id === "content";
  }

  // Clearing happens once per request, on whichever comes first: the swap
  // (the normal path) or the request finishing (abort, network error, a
  // response that swaps nothing). The flag on ctx keeps the pair balanced.
  function done(evt) {
    var ctx = (evt.detail || {}).ctx;
    if (!isNavigation(evt) || !ctx || ctx.__navSettled) return;
    ctx.__navSettled = true;
    inFlight = Math.max(0, inFlight - 1);
    if (inFlight === 0) document.documentElement.removeAttribute("data-navigating");
  }

  document.addEventListener("htmx:before:request", function (evt) {
    if (!isNavigation(evt)) return;
    inFlight++;
    document.documentElement.setAttribute("data-navigating", "");
  });

  // BEFORE the swap, not after the request: the swap replaces #content
  // wholesale, and an element inserted while the flag is still set paints
  // dimmed from birth — CSS transitions do not apply to initial values, so
  // the new page would flash grey before snapping to full opacity.
  document.addEventListener("htmx:before:swap", done);
  document.addEventListener("htmx:finally:request", done);
})();

// --- Live notification stream ---
//
// A native EventSource rather than htmx's SSE extension: that extension is
// built for htmx 2 (it calls htmx.defineExtension, which htmx 4 removed), so
// vendoring it here meant the script threw on every authed page load and the
// stream was never opened at all — the badge only ever updated on navigation.
//
// The element carrying data-notification-stream lives in the app shell, which
// boosted navigation never swaps, so one connection survives page changes.
// EventSource reconnects on its own after a drop; nothing here needs to.
(function () {
  function connect() {
    var el = document.querySelector("[data-notification-stream]");
    if (!el || el.dataset.streamOpen === "1") return;
    var url = el.getAttribute("data-notification-stream");
    if (!url) return;
    el.dataset.streamOpen = "1";

    var source = new EventSource(url);
    source.addEventListener("notifications", function () {
      // Let htmx do the fetch: it owns the swap, the headers and the CSRF
      // token. A plain DOM event is all hx-trigger needs.
      var badge = document.getElementById("notif-badge");
      if (badge) badge.dispatchEvent(new CustomEvent("notifications"));
    });
    window.addEventListener("pagehide", function () {
      source.close();
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", connect);
  } else {
    connect();
  }
})();

// --- htmx notes (config lives in the <meta name="htmx-config"> tag) ---
// Attribute inheritance is EXPLICIT in htmx 4: the only inherited attribute
// this app needs is the body's CSRF header, declared as hx-headers:inherited,
// so no implicitInheritance compatibility shim is required.
// noSwap stays at the htmx 4 default ([204, 304]) on purpose: our 422 (form
// validation) and 503 (not-configured) fragments MUST swap, and every other
// error response renders a page whose #content is a valid swap target.

// --- App nav state ---
// Navigation swaps ONLY #content, so the sidebar/topbar shell is never touched
// (that is what keeps clerk-js's body-level dropdown portals and the Alpine
// components in the shell alive). The trade-off is that server-rendered
// aria-current in the persistent sidebar would go stale, so sync it client-side
// using the longest matching data-nav-match prefix.
function syncAppNavigation() {
  var path = window.location.pathname;
  var links = Array.prototype.slice.call(document.querySelectorAll("[data-app-nav]"));
  var current = null;
  var bestLength = -1;
  links.forEach(function (link) {
    var match = link.dataset.navMatch || new URL(link.href, window.location.href).pathname;
    var isMatch = path === match || path.indexOf(match.replace(/\/$/, "") + "/") === 0;
    if (isMatch && match.length > bestLength) {
      current = link;
      bestLength = match.length;
    }
  });
  links.forEach(function (link) {
    if (link === current) {
      link.setAttribute("aria-current", "page");
    } else {
      link.removeAttribute("aria-current");
    }
  });
}

window.addEventListener("DOMContentLoaded", syncAppNavigation);
document.addEventListener("htmx:after:settle", syncAppNavigation);

// --- Alpine CSP components (registered before Alpine boots) ---
document.addEventListener("alpine:init", function () {
  // Flip locally for instant feedback, then persist the RESULTING value.
  //
  // The value has to come from the client: when the stored preference is
  // "system", only the browser knows which way the OS points, so a server
  // that flipped on its own would disagree with what the user just saw.
  Alpine.data("themeToggle", function () {
    return {
      dark: document.documentElement.classList.contains("dark"),
      toggle: function () {
        this.dark = !this.dark;
        var next = this.dark ? "dark" : "light";
        document.documentElement.classList.toggle("dark", this.dark);
        localStorage.setItem("theme", next);
        persistTheme(next);
      },
    };
  });

  // Dismissible banners keyed per-element (data-key): the announcement
  // banner stays hidden for this browser once dismissed, until a new
  // announcement (new id → new key) arrives. Checklist pattern.
  Alpine.data("dismissible", function () {
    return {
      key: "",
      hidden: false,
      // init() owns $el (verified against the vendored Alpine CSP build);
      // expressions under CSP cannot touch globals, so all localStorage
      // access lives here in real JS.
      init: function () {
        this.key = this.$el.dataset.key || "";
        this.hidden = localStorage.getItem(this.key) === "1";
      },
      dismiss: function () {
        this.hidden = true;
        if (this.key) localStorage.setItem(this.key, "1");
      },
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

  // Copy a markdown snippet carried on the button itself. Per-button x-data
  // so this.$el IS the button: the CSP build cannot read $el from an
  // expression, and a table of rows cannot share one root.
  Alpine.data("copyMarkdown", function () {
    return {
      copied: false,
      copy: function () {
        var self = this;
        navigator.clipboard.writeText(this.$el.dataset.md || "").then(function () {
          self.copied = true;
          setTimeout(function () { self.copied = false; }, 2000);
        });
      },
    };
  });

  // Content editor: derive the slug from the title until the slug is touched.
  // Locked from the start when editing an existing entry — changing a
  // published URL should be a deliberate act, never a side effect of fixing
  // a typo in the headline.
  Alpine.data("slugify", function () {
    return {
      locked: false,
      init: function () {
        var slug = this.$refs.slug;
        this.locked = !!slug && (slug.dataset.slugLocked === "true" || slug.value !== "");
      },
      onTitle: function (event) {
        if (this.locked) return;
        var slug = this.$refs.slug;
        if (!slug) return;
        slug.value = event.target.value
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-+|-+$/g, "")
          .slice(0, 200);
      },
      onSlug: function () {
        this.locked = true;
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

        var showFailure = function (error) {
          if (error) console.error("Clerk organization switch failed", error);
          document.dispatchEvent(new CustomEvent("toast", {
            detail: { type: "error", message: "Unable to switch organizations. Please try again." },
          }));
        };

        mountClerkWidgets();
        if (!clerkLoadPromise) {
          showFailure();
          return;
        }
        clerkLoadPromise.then(function (loaded) {
          if (!loaded) {
            showFailure();
            return;
          }
          window.Clerk.setActive({ organization: orgId })
            .then(function () { window.location.assign("/app"); })
            .catch(showFailure);
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
var clerkLoadPromise;
var mountedUserButton;
var mountedOrganizationSwitcher;
var clerkRetriedLoad = false;
// clerk-js mounts React components (UserButton, OrganizationSwitcher) into these
// roots AND renders their dropdown menus as portals appended directly to
// <body>. Those portals are siblings of the mount roots, not children, so no
// per-element attribute can protect them: any swap or morph of <body> deletes
// them and the dropdowns go dead. Alpine bindings in the shell break the same
// way. Therefore navigation swaps ONLY #content and never touches <body>'s
// other children — the mount roots, the portals, and the shell's Alpine
// components all survive untouched. Clerk mounts once per full page load.

function mountClerkWidgets() {
  var meta = document.querySelector('meta[name="clerk-publishable-key"]');
  if (!meta || !window.Clerk) return;

  if (!clerkLoadPromise) {
    clerkLoadPromise = window.Clerk.load()
      .then(function () { return true; })
      .catch(function (error) {
        clerkLoadPromise = undefined;
        console.error("Clerk failed to load", error);
        // Retry a transient failure exactly once, self-driven: waiting on the
        // next htmx event would race the rejection and re-await the same
        // already-failed promise instead of starting a fresh attempt.
        if (!clerkRetriedLoad) {
          clerkRetriedLoad = true;
          setTimeout(mountClerkWidgets, 0);
        }
        return false;
      });
  }
  clerkLoadPromise.then(function (loaded) {
    if (!loaded) return;

    var clerk = window.Clerk;
    // after-auth: the hosted portal redirected back to "/" so this page could
    // render and complete the dev handshake; with a session in place, forward
    // to the app. (Pointing redirect_url at /app would loop: /app 303s
    // without rendering, so clerk-js never gets to run.)
    if (clerk.session && new URLSearchParams(window.location.search).has("after-auth")) {
      window.location.replace("/app");
      return;
    }

    var userButton = document.getElementById("user-button");
    var organizationSwitcher = document.getElementById("org-switcher");
    if (!clerk.user) return;

    // Mount once per page load; several lifecycle hooks call this on a fresh
    // document, so the guard below prevents a double mount.
    if (userButton && !mountedUserButton) {
      userButton.replaceChildren();
      clerk.mountUserButton(userButton);
      mountedUserButton = userButton;
    }
    if (organizationSwitcher && !mountedOrganizationSwitcher) {
      organizationSwitcher.replaceChildren();
      clerk.mountOrganizationSwitcher(organizationSwitcher);
      mountedOrganizationSwitcher = organizationSwitcher;
    }
  });
}

// Mount once per full page load. Navigation only swaps #content, so the mounted
// widgets are never disturbed and must NOT be re-mounted. mountClerkWidgets is
// idempotent (it no-ops while a mount is live), so the htmx hooks below only
// ever RETRY a mount that never succeeded — e.g. after a transient
// Clerk.load() rejection. htmx 4 renamed the 2.x load event to
// htmx:after:init; htmx.onLoad() listens on htmx:after:process, used here.
window.addEventListener("DOMContentLoaded", mountClerkWidgets);
document.addEventListener("htmx:after:process", mountClerkWidgets);
document.addEventListener("htmx:after:settle", mountClerkWidgets);
