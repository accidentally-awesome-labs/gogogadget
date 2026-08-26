#!/usr/bin/env bash
# Compares the committed visual baselines. Read-only: it never writes a
# snapshot, which is what makes it safe as a required CI gate.
set -euo pipefail
exec "$(dirname "$0")/visual-run.sh" compare
