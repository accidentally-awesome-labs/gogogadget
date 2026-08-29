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
// Alpine's own directives insert DOM while it walks the tree on boot - an x-if
// or x-for holding a widget root - and that walk can finish after
// DOMContentLoaded, so those roots are not in the first scan.
// alpine:initialized fires once, after that walk, which is exactly the window
// this covers and the whole of it: a root Alpine reveals LATER (an x-if that
// flips on interaction) is not covered here, because the event has already
// fired. htmx-inserted content has its own hook above; an Alpine-revealed root
// has none, and would need one before it could be claimed.
document.addEventListener("alpine:initialized", () => scan(document));

// pending holds one promise per engine, so ten charts on a page trigger one
// fetch. Keyed by name rather than by element: the second widget must wait on
// the first request, not start a second.
//
// A rejected FETCH is evicted. Caching a rejection means one dropped request -
// a flaky connection, a proxy hiccup, a cold cache under load - permanently
// disables the widget for the rest of the page's life, and every widget that
// mounts afterwards receives the cached failure instantly without a single byte
// being retried. That reads exactly like a broken build and is nothing of the
// kind.
//
// The one rejection that is KEPT is an engine the registry does not declare
// (see the !asset branch below). There is nothing to retry: the registry is a
// generated object in the page, so the answer is deterministic for this
// document's life, and re-deriving it per widget would only repeat the same
// verdict.
//
// Resolved entries are kept forever: the runtime is loaded, and loading it
// twice would re-run a bundle that defines custom elements.
const pending = new Map();

function loadEngine(name) {
  if (pending.has(name)) return pending.get(name);

  const registry = window.__gggEngines || {};
  const asset = registry[name];
  if (!asset) {
    // An undeclared engine is a manifest bug, and it must fail loudly here
    // rather than resolving to a runtime that never arrives.
    //
    // This rejection is cached and never evicted, unlike a failed fetch: the
    // registry is a generated object already in the page, so there is no
    // request to retry and the verdict cannot change while the document lives.
    const failure = Promise.reject(new Error("engine " + name + " is not installed"));
    pending.set(name, failure);
    return failure;
  }

  const promise = new Promise((resolve, reject) => {
    const existing = document.querySelector('script[data-ui-engine-src="' + name + '"]');
    if (existing) {
      // Listeners on an already-settled script never fire, so an existing tag
      // has to report the state it reached rather than be waited on blindly -
      // otherwise this promise hangs for the life of the page and the widget
      // waits for an event that happened before anyone was listening.
      const state = existing.dataset.uiEngineState;
      if (state === "loaded") {
        resolve();
        return;
      }
      if (state === "failed") {
        reject(new Error("engine " + name + " previously failed to load"));
        return;
      }
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
    script.addEventListener(
      "load",
      () => {
        script.dataset.uiEngineState = "loaded";
        resolve();
      },
      { once: true },
    );
    script.addEventListener(
      "error",
      () => {
        script.dataset.uiEngineState = "failed";
        reject(new Error("failed to load engine " + name));
      },
      { once: true },
    );
    // <head>, never #content: the shell survives navigation and the content box
    // does not.
    document.head.appendChild(script);
  });
  pending.set(name, promise);
  // Evict on failure so the next widget to mount retries. The tag is removed
  // with it: a <script> that errored will never fire another event, so leaving
  // it would make the retry take the `existing` branch and reject immediately.
  //
  // This catch handles only the eviction branch. The promise returned below
  // still rejects for the caller, which announces `ui:engine-failed` on the
  // root - so nothing is swallowed and there is no unhandled rejection either.
  promise.catch(() => {
    if (pending.get(name) === promise) {
      pending.delete(name);
    }
    const failed = document.querySelector(
      'script[data-ui-engine-src="' + name + '"][data-ui-engine-state="failed"]',
    );
    if (failed) {
      failed.remove();
    }
  });
  return promise;
}
