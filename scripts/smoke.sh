#!/usr/bin/env bash
# Smoke test against a running stack (default http://localhost:8080).
# Asserts status + marker on the public surface and key behaviors.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
fail=0

check() { # path expected_status marker
  local path="$1" want_status="$2" marker="$3"
  local out code
  out="$(curl -s -w '\n%{http_code}' "${BASE}${path}")"
  code="$(tail -n1 <<<"$out")"
  if [[ "$code" != "$want_status" ]]; then
    echo "FAIL ${path}: status ${code}, want ${want_status}"
    fail=1
    return
  fi
  if [[ -n "$marker" ]] && ! grep -q "$marker" <<<"$out"; then
    echo "FAIL ${path}: marker ${marker@Q} not found"
    fail=1
    return
  fi
  echo "ok   ${path} (${want_status})"
}

check_redirect() { # path expected_location
  local path="$1" want="$2" loc
  loc="$(curl -s -o /dev/null -w '%{redirect_url}' "${BASE}${path}")"
  if [[ "$loc" != *"$want"* ]]; then
    echo "FAIL ${path}: redirect ${loc@Q}, want *${want}*"
    fail=1
    return
  fi
  echo "ok   ${path} → ${loc}"
}

check "/" 200 "Ship your SaaS this weekend"
check "/pricing" 200 "Pricing"
check "/blog" 200 "Blog"
check "/blog/hello-world" 200 "Hello, GoGoGadget"
check_redirect "/docs" "/docs/index"
check "/docs/index" 200 "GoGoGadget"
check "/docs/getting-started" 200 "Getting started"
check "/rss.xml" 200 '<rss version="2.0">'
check "/sitemap.xml" 200 "urlset"
check "/healthz" 200 '"status":"ok"'
check "/readyz" 200 '"status":"ok"'
check_redirect "/app" "/login"
check "/nope" 404 "404"

if [[ "$fail" != 0 ]]; then
  echo "SMOKE FAILED"
  exit 1
fi
echo "SMOKE OK"
