#!/usr/bin/env bash
# Vendors the frontend runtime into static/ from pinned jsDelivr URLs.
# Every asset is sha256-verified: a CDN surprise must never reach production.
# The results are COMMITTED to the repo — this script only runs on upgrades.
set -euo pipefail

fetch() { # name url dest sha256
  local name="$1" url="$2" dest="$3" sha="$4"
  echo "fetching ${name} → ${dest}"
  curl -sfL -o "${dest}.tmp" "${url}"
  if ! echo "${sha}  ${dest}.tmp" | shasum -a 256 -c - >/dev/null 2>&1; then
    rm -f "${dest}.tmp"
    echo "sha256 mismatch for ${name} (${url}) — refusing to install" >&2
    exit 1
  fi
  mv "${dest}.tmp" "${dest}"
}

mkdir -p static/vendor static/fonts

fetch "htmx@2.0.4" \
  "https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js" \
  "static/vendor/htmx.min.js" \
  "e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447"

fetch "@alpinejs/csp@3.15.12" \
  "https://cdn.jsdelivr.net/npm/@alpinejs/csp@3.15.12/dist/cdn.min.js" \
  "static/vendor/alpine-csp.min.js" \
  "566167134bb2347110904e2ced6e816d2e8d837200c158f98b72372b3bb0b9a6"

fetch "@alpinejs/focus@3.15.12" \
  "https://cdn.jsdelivr.net/npm/@alpinejs/focus@3.15.12/dist/cdn.min.js" \
  "static/vendor/alpine-focus.min.js" \
  "ea7e215444f5110619549621cd0760cedfe273f708b144d4e658a87b702555f9"

fetch "@clerk/clerk-js@5.127.1" \
  "https://cdn.jsdelivr.net/npm/@clerk/clerk-js@5.127.1/dist/clerk.browser.js" \
  "static/vendor/clerk.browser.js" \
  "d92e69c91eeb10ec1558b79376a35520ead6e358811319366c6c28a4fb88d5a0"

fetch "inter-variable@5.3.0" \
  "https://cdn.jsdelivr.net/npm/@fontsource-variable/inter@5.3.0/files/inter-latin-wght-normal.woff2" \
  "static/fonts/inter-var.woff2" \
  "3100e775e8616cd2611beecfa23a4263d7037586789b43f035236a2e6fbd4c62"

echo "vendored frontend OK"
