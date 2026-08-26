#!/usr/bin/env bash
# Smoke test against a running stack (default http://localhost:8080).
#
# Coverage is generated; assertions are hand-owned.
#
# smoke_cases_registry_gen.txt is the public GET surface derived from the route
# registry, so adding or removing a route changes what this run visits without
# anyone remembering to edit a list. What a page should answer — a status, a body
# marker, a redirect target — is handler behaviour that no manifest declares, so
# that stays written down here, one assertion per generated path.
#
# The run fails when the two disagree in either direction: a generated route with
# no assertion is a surface nobody checks, and an assertion for a path the
# registry no longer serves is a check that proves nothing.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
CASES="$(dirname "${BASH_SOURCE[0]}")/smoke_cases_registry_gen.txt"
fail=0

# One assertion per generated path:
#   <status>|<marker>    expect this status, and this string somewhere in the body
#                        (an empty marker asserts the status only)
#   redirect|<substring> expect a redirect whose Location contains this
#
# Redirects to the hosted account portal are asserted by path, not by full URL:
# the destination host comes from CLERK_PORTAL_URL and differs per deployment,
# while the hosted sign-in/sign-up/sign-out paths do not.
declare -A public_expect=(
  ["/"]='200|Ship your SaaS this weekend'
  ["/api/v1/openapi.yaml"]='200|openapi: 3.1.0'
  ["/blog"]='200|Blog'
  ["/changelog"]='200|Changelog'
  ["/docs"]='redirect|/docs/index'
  ["/docs/search"]='200|data-testid="docs-search-form"'
  ["/login"]='redirect|/sign-in'
  ["/logout"]='redirect|/sign-out'
  ["/pricing"]='200|Pricing'
  ["/privacy"]='200|Privacy Policy'
  ["/robots.txt"]='200|Sitemap:'
  ["/rss.xml"]='200|<rss version="2.0">'
  ["/signup"]='redirect|/sign-up'
  ["/sitemap.xml"]='200|urlset'
  ["/terms"]='200|Terms of Service'
)

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
  if [[ -n "$marker" ]] && ! grep -qF "$marker" <<<"$out"; then
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

assert() { # path assertion
  local path="$1" assertion="$2"
  case "$assertion" in
    "redirect|"*) check_redirect "$path" "${assertion#redirect|}" ;;
    *) check "$path" "${assertion%%|*}" "${assertion#*|}" ;;
  esac
}

# --- generated coverage: the public GET surface ------------------------------
if [[ ! -r "$CASES" ]]; then
  echo "FAIL missing generated case list ${CASES}: run make generate"
  exit 1
fi

while read -r id path; do
  case "$id" in '' | '#'*) continue ;; esac
  if [[ -z "${public_expect[$path]+set}" ]]; then
    echo "FAIL ${path}: route ${id} is on the public GET surface with no smoke assertion; add one to scripts/smoke.sh"
    fail=1
    continue
  fi
  assert "$path" "${public_expect[$path]}"
  # Consumed, so whatever is left over is an assertion with no route.
  unset "public_expect[$path]"
done <"$CASES"

for path in "${!public_expect[@]}"; do
  echo "FAIL ${path}: smoke assertion for a path the route registry no longer serves; remove it from scripts/smoke.sh"
  fail=1
done

# --- surfaces the registry cannot generate ----------------------------------
# Parameterized routes. The pattern is registry data but the instance is not:
# which post and which doc page exist is the seeded content corpus.
check "/blog/hello-world" 200 "Hello, GoGoGadget"
check "/docs/index" 200 "GoGoGadget"
check "/docs/getting-started" 200 "Getting started"

# Other scopes. The probes are scope probe and the app shell is scope app, so
# neither is on the generated public list, and both are load-bearing: a deploy
# whose probes answer wrongly never comes up, and /app must send a signed-out
# visitor to the login route rather than render anything.
check "/healthz" 200 '"status":"ok"'
check "/readyz" 200 '"status":"ok"'
check_redirect "/app" "/login"

# The negative case. No route matches, which is precisely why it cannot come
# from a table of routes.
check "/nope" 404 "404"

if [[ "$fail" != 0 ]]; then
  echo "SMOKE FAILED"
  exit 1
fi
echo "SMOKE OK"
