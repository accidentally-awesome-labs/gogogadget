// The lazy engine loader, owned by element/ui-core.
//
// An advanced widget needs a third-party runtime, and loading every such
// runtime with the shell would make a page that renders no chart pay for the
// charting library. This loader fetches a versioned vendor file the first time
// a matching `data-ui-engine` root appears, then tells that root it is ready.
//
// Four constraints shape it, and each one rules out an easier design:
//
//   - Scripts are only ever appended to <head>. htmx replaces `#content` on
//     every navigation, so a script injected there is re-executed on each swap
//     and removed from under a running instance.
//   - Head assets are never conditioned on the initially requested page. The
//     shell is persistent and the first URL is an accident of where the user
//     entered; deciding what to load from it means the same app has different
//     capabilities depending on the entry point.
//   - Every injected script carries its integrity hash from the generated
//     registry. A lazily injected script with no integrity is a file that can
//     be swapped without anything noticing.
//   - `script-src 'self'` is never widened. The vendor file is installed into
//     `static/vendor/` by the module, so it is same-origin and needs no CSP
//     change at all.
// The loader is plain DOM code, not an Alpine component, and that is a
// correction rather than a preference: an element can carry only one x-data, and
// every widget that needs an engine already uses that slot for its own
// behaviour. Making the loader a component meant a chart could be a chart or
// could request its engine, never both.
//
// Scanning also covers content htmx inserts, which an Alpine-only adapter would
// have missed on any fragment swap.
function scan(root) {
  const scope = root && root.querySelectorAll ? root : document;
  scope.querySelectorAll("[data-ui-engine]").forEach((el) => {
    const name = el.dataset.uiEngine;
    if (!name || el.dataset.uiEngineRequested === "true") return;
    // Marked before the request so a re-scan of the same subtree - htmx
    // processes nested content more than once - does not queue it twice.
    el.dataset.uiEngineRequested = "true";
    loadEngine(name).then(
      () => {
        el.dispatchEvent(
          new CustomEvent("ui:engine-ready", { detail: { engine: name }, bubbles: false }),
        );
      },
      (error) => {
        // Failure is announced on the root rather than thrown: the widget is
        // already showing its accessible fallback, and an unhandled rejection
        // would say nothing to the component that needs to know.
        el.dispatchEvent(
          new CustomEvent("ui:engine-failed", {
            detail: { engine: name, error: String(error) },
            bubbles: false,
          }),
        );
      },
    );
  });
}

// Both hooks are needed: the first paint is not an htmx event, and an htmx swap
// is not a document load.
document.addEventListener("DOMContentLoaded", () => scan(document));
document.addEventListener("htmx:after:process", (event) => scan(event.target));
// A widget mounted by Alpine after boot - a dialog's contents, say - still needs
// its engine, and alpine:initialized fires once Alpine has walked the tree.
document.addEventListener("alpine:initialized", () => scan(document));

// pending holds one promise per engine, so ten charts on a page trigger one
// fetch. Keyed by name rather than by element: the second widget must wait on
// the first request, not start a second.
const pending = new Map();

function loadEngine(name) {
  if (pending.has(name)) return pending.get(name);

  const registry = window.__gggEngines || {};
  const asset = registry[name];
  if (!asset) {
    // An undeclared engine is a manifest bug, and it must fail loudly here
    // rather than resolving to a runtime that never arrives.
    const failure = Promise.reject(new Error("engine " + name + " is not installed"));
    pending.set(name, failure);
    return failure;
  }

  const promise = new Promise((resolve, reject) => {
    const existing = document.querySelector('script[data-ui-engine-src="' + name + '"]');
    if (existing) {
      existing.addEventListener("load", () => resolve(), { once: true });
      existing.addEventListener("error", () => reject(new Error(name)), { once: true });
      return;
    }
    const script = document.createElement("script");
    script.src = asset.src;
    script.integrity = asset.integrity;
    if (asset.esm) {
      // A module is not a classic script: injected without type="module" the
      // browser rejects it outright for its export statement. Modules are
      // deferred by definition, so `defer` is meaningless here.
      script.type = "module";
    } else {
      script.defer = true;
    }
    script.dataset.uiEngineSrc = name;
    script.addEventListener("load", () => resolve(), { once: true });
    script.addEventListener("error", () => reject(new Error("failed to load engine " + name)), {
      once: true,
    });
    // <head>, never #content: the shell survives navigation and the content box
    // does not.
    document.head.appendChild(script);
  });
  pending.set(name, promise);
  return promise;
}
