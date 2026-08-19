#!/usr/bin/env bash
# Regenerates visual baselines inside the pinned Playwright Linux container —
# the ONLY supported way to update snapshots (macOS screenshots diff by design).
#
# The server runs on the HOST (the container has no Go toolchain); Playwright
# reaches it via host.docker.internal.
set -euo pipefail

VERSION="$(jq -r '.devDependencies["@playwright/test"]' e2e/package.json)"
IMAGE="mcr.microsoft.com/playwright:v${VERSION}-jammy"

DB_PORT_NUM="${DB_PORT:-5432}"
HOST_DB="postgres://postgres:postgres@localhost:${DB_PORT_NUM}/gogogadget_e2e?sslmode=disable"
CONTAINER_DB="postgres://postgres:postgres@host.docker.internal:${DB_PORT_NUM}/gogogadget_e2e?sslmode=disable"

echo "==> Reseeding e2e database (localhost:${DB_PORT_NUM})"
DATABASE_URL="${HOST_DB}" go run ./cmd/seed -reset internal/db/testdata/seed_e2e.sql

echo "==> Building test server"
go build -o tmp/e2e-visual-server ./cmd/server

echo "==> Starting test server on :18080"
APP_ENV=test PORT=18080 DATABASE_URL="${HOST_DB}" \
  DEV_AUTH_BYPASS=true CLERK_PORTAL_URL=https://accounts.example.test \
  TEST_NOW=2026-01-15T00:00:00Z RATE_LIMIT_RPM=100000 \
  ./tmp/e2e-visual-server &
SERVER_PID=$!
trap 'kill ${SERVER_PID} 2>/dev/null || true' EXIT

for i in $(seq 1 60); do
  curl -sf http://localhost:18080/healthz >/dev/null && break
  sleep 1
done

echo "==> Running visual specs in ${IMAGE}"
docker run --rm \
  -v "$PWD":/work -w /work \
  -e E2E_NO_WEBSERVER=1 \
  -e E2E_VISUAL=1 \
  -e E2E_BASE_URL=http://host.docker.internal:18080 \
  --add-host host.docker.internal:host-gateway \
  "${IMAGE}" \
  bash -c "cd e2e && npm ci --silent && npx playwright test visual.spec.ts --update-snapshots"

echo "==> Baselines updated in e2e/visual.spec.ts-snapshots/"
