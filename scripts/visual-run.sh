#!/usr/bin/env bash
# Runs the visual suite inside the pinned Playwright Linux container — the ONLY
# supported way to compare or update baselines (macOS screenshots diff by
# design). Compare and update differ by exactly one Playwright flag, so they
# share this one harness: a second copy of the setup would let an updated
# baseline reproduce only under the command that wrote it.
#
# The server runs on the HOST (the container has no Go toolchain); Playwright
# reaches it via host.docker.internal.
#
# usage: scripts/visual-run.sh <compare|update>
set -euo pipefail

MODE="${1:-}"
case "${MODE}" in
  compare | update) ;;
  *)
    echo "usage: $0 <compare|update>" >&2
    exit 2
    ;;
esac

# Three entry points now share this script, and a wrong working directory would
# silently bind-mount the wrong tree into the container instead of failing.
if [[ ! -f e2e/package.json ]]; then
  echo "run from the repository root (e2e/package.json not found)" >&2
  exit 2
fi

# Derived, never hardcoded: the image tag and the installed Playwright must be
# the same version or the bundled browser build changes every pixel.
VERSION="$(jq -r '.devDependencies["@playwright/test"]' e2e/package.json)"
IMAGE="mcr.microsoft.com/playwright:v${VERSION}-jammy"

DB_PORT_NUM="${DB_PORT:-5432}"
HOST_DB="postgres://postgres:postgres@localhost:${DB_PORT_NUM}/gogogadget_e2e?sslmode=disable"

echo "==> Reseeding e2e database (localhost:${DB_PORT_NUM})"
DATABASE_URL="${HOST_DB}" go run ./cmd/seed -reset -registry e2e

echo "==> Building test server"
# Remove the target first. `go build -o` refuses to overwrite a path that is not
# an object file, and the failure reads "already exists and is not an object
# file" - which says nothing about the stale script or directory sitting there.
mkdir -p tmp
rm -f tmp/e2e-visual-server
go build -o tmp/e2e-visual-server ./cmd/server

echo "==> Starting test server on :18080"
APP_ENV=test PORT=18080 DATABASE_URL="${HOST_DB}" \
  DEV_AUTH_BYPASS=true CLERK_PORTAL_URL=https://accounts.example.test \
  TEST_NOW=2026-01-15T00:00:00Z RATE_LIMIT_RPM=100000 \
  ./tmp/e2e-visual-server &
SERVER_PID=$!
trap 'kill ${SERVER_PID} 2>/dev/null || true' EXIT

for _ in $(seq 1 60); do
  curl -sf http://localhost:18080/healthz >/dev/null && break
  sleep 1
done

PLAYWRIGHT="npx playwright test visual.spec.ts"
if [[ "${MODE}" == update ]]; then
  PLAYWRIGHT="${PLAYWRIGHT} --update-snapshots"
fi

echo "==> Running visual specs (${MODE}) in ${IMAGE}"
docker run --rm \
  -v "$PWD":/work -w /work \
  -e E2E_NO_WEBSERVER=1 \
  -e E2E_VISUAL=1 \
  -e E2E_BASE_URL=http://host.docker.internal:18080 \
  --add-host host.docker.internal:host-gateway \
  "${IMAGE}" \
  bash -c "cd e2e && npm ci --silent && ${PLAYWRIGHT}"

if [[ "${MODE}" == update ]]; then
  echo "==> Baselines updated in e2e/visual.spec.ts-snapshots/"
else
  echo "==> Baselines match"
fi
