#!/usr/bin/env bash
# One-terminal dev loop: templ watch + tailwind watch + air (server restart).
# Browser refresh stays manual — the strict CSP forbids air's injected reload snippet.
set -euo pipefail

go tool templ generate
bin/tailwindcss -i input.css -o static/app.css

cleanup() {
  [[ -n "${TEMPL_PID:-}" ]] && kill "${TEMPL_PID}" 2>/dev/null || true
  [[ -n "${TW_PID:-}" ]] && kill "${TW_PID}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

go tool templ generate --watch &
TEMPL_PID=$!
bin/tailwindcss -i input.css -o static/app.css --watch &
TW_PID=$!

go tool air
