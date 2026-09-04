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

# The architecture is the same class of variable as the version, and it was the
# one left unpinned. The Playwright image is multi-arch, so on an Apple Silicon
# host `docker run` picks arm64 while CI runs amd64 — and the two rasterize text
# differently. Most surfaces stayed under maxDiffPixelRatio and one did not, so
# the failure read as a real regression on a page nobody had touched. Pinning
# amd64 costs emulation time locally and makes the harness reproduce CI on any
# host, which is the whole promise of running it in a container.
PLATFORM="${VISUAL_PLATFORM:-linux/amd64}"

# Emulated Chromium crashes under Playwright's default parallelism: the symptom
# is "Page crashed" on a different handful of surfaces every run, which reads as
# flaky baselines rather than an exhausted host. One worker is slow and it is
# correct, and it applies ONLY when the container architecture is not the host's
# - CI runs amd64 on amd64 and keeps the default.
HOST_ARCH="$(docker version --format '{{.Server.Arch}}' 2>/dev/null || echo unknown)"
WORKERS=""
if [[ "${PLATFORM}" != */"${HOST_ARCH}" ]]; then
  echo "==> ${PLATFORM} is emulated on ${HOST_ARCH}; running single-threaded"
  WORKERS="--workers=1"
fi

# The harness pins its own database and never inherits one. VISUAL_DATABASE_URL
# is its key; empty falls through to the project's derivation, which is the
# address the test stack publishes (the selected database adapter's local
# service on the test environment's effective host port, 5432 shifted to
# 15432). Both children below get DATABASE_URL set EXPLICITLY to that value, so
# an ambient DATABASE_URL in the caller's shell cannot redirect them: an
# exported-but-empty value counts as unset everywhere downstream, which is what
# makes the fall-through work while still shadowing what was inherited.
#
# That matters more here than anywhere else. `-reset` DROPS and recreates the
# database it is given, and this script is the only thing in the repository
# allowed to write a visual baseline.
#
# It used to build the DSN here from DB_PORT with a localhost:5432 default,
# which is the DEVELOPMENT port. On a machine running its own Postgres there,
# this harness reset, migrated and served a database the project had nothing to
# do with.
HOST_DB="${VISUAL_DATABASE_URL:-}"

if [[ -n "${HOST_DB}" ]]; then
  echo "==> Reseeding e2e database (VISUAL_DATABASE_URL)"
else
  echo "==> Reseeding e2e database (derived from the test stack)"
fi
APP_ENV=test DATABASE_URL="${HOST_DB}" go run ./cmd/seed -reset -registry e2e

echo "==> Building test server"
# Remove the target first. `go build -o` refuses to overwrite a path that is not
# an object file, and the failure reads "already exists and is not an object
# file" - which says nothing about the stale script or directory sitting there.
mkdir -p tmp
rm -f tmp/e2e-visual-server
go build -o tmp/e2e-visual-server ./cmd/server

echo "==> Starting test server on :18080"
# CLERK_* are blanked deliberately. The server auto-loads `.env` in development,
# so a developer with a real Clerk dev key boots clerk-js into every page - which
# mounts a user button and a portal that CI, having no `.env`, never renders.
# Baselines recorded here would then differ from CI's by real pixels, and the
# person who regenerated them would have no idea why.
APP_ENV=test PORT=18080 DATABASE_URL="${HOST_DB}" \
  DEV_AUTH_BYPASS=true CLERK_PORTAL_URL=https://accounts.example.test \
  CLERK_PUBLISHABLE_KEY= CLERK_SECRET_KEY= \
  TEST_NOW=2026-01-15T00:00:00Z RATE_LIMIT_RPM=100000 \
  ./tmp/e2e-visual-server &
SERVER_PID=$!
trap 'kill ${SERVER_PID} 2>/dev/null || true' EXIT

for _ in $(seq 1 60); do
  curl -sf http://localhost:18080/healthz >/dev/null && break
  sleep 1
done

PLAYWRIGHT="npx playwright test visual.spec.ts ${WORKERS}"
if [[ "${MODE}" == update ]]; then
  PLAYWRIGHT="${PLAYWRIGHT} --update-snapshots"
fi

echo "==> Running visual specs (${MODE}) in ${IMAGE} (${PLATFORM})"
# --ipc=host is Playwright's own requirement for Chromium in Docker: the default
# 64 MB /dev/shm is not enough for its shared-memory allocations, and the symptom
# is "Page crashed" on an arbitrary subset of surfaces rather than an out-of-
# memory error. It went unnoticed while the suite was small and native; three
# viewports and an emulated architecture made it reproducible.
docker run --rm \
  --platform "${PLATFORM}" \
  --ipc=host \
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
