// PostHog client bootstrap. Loaded only when the server emitted the ph-key
// meta tag. The consent gate is load-bearing: posthog.init runs ONLY after
// the user accepts (localStorage ph_consent=yes) — GDPR-sane by default.
(function () {
  var meta = document.querySelector('meta[name="ph-key"]');
  if (!meta || !window.posthog) return;

  function initPostHog() {
    window.posthog.init(meta.content, { api_host: "/ingest", autocapture: true });
  }

  if (localStorage.getItem("ph_consent") === "yes") {
    initPostHog();
  }

  document.addEventListener("alpine:init", function () {
    Alpine.data("phConsent", function () {
      return {
        show: localStorage.getItem("ph_consent") === null,
        accept: function () {
          localStorage.setItem("ph_consent", "yes");
          initPostHog();
          this.show = false;
        },
        decline: function () {
          localStorage.setItem("ph_consent", "no");
          this.show = false;
        },
      };
    });
  });
})();
