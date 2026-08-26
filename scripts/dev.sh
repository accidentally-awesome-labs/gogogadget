#!/usr/bin/env bash
# One-terminal dev loop: templ watch + tailwind watch + air (server restart).
# Browser refresh stays manual — the strict CSP forbids air's injected reload snippet.
set -euo pipefail

# One-shot pre-build through the Makefile, so the templ and Tailwind invocations
# have one definition shared with `make generate`. Deliberately NOT
# `make generate`: that also runs `ggg sync --offline`, which refuses with
# `sha256 mismatch` whenever a manifest-owned source file has been edited
# without `ggg registry build` — the normal state mid-change — so it would make
# `make dev` fail exactly when a developer needs it. Registry aggregates are
# `make generate`/`make check`'s job.
make templ css

cleanup() {
  [[ -n "${TEMPL_PID:-}" ]] && kill "${TEMPL_PID}" 2>/dev/null || true
  [[ -n "${TW_PID:-}" ]] && kill "${TW_PID}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# The watchers stay as direct invocations on purpose. A watcher is not the
# one-shot command: it is a long-lived process whose PID this script owns and
# kills, and `make` would hand back its own PID, not templ's or Tailwind's.
# There is also nothing for ggg sync or sqlc to watch here.
go tool templ generate --watch &
TEMPL_PID=$!
bin/tailwindcss -i input.css -o static/app.css --watch &
TW_PID=$!

go tool air
