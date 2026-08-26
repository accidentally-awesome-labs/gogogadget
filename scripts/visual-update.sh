#!/usr/bin/env bash
# Regenerates the visual baselines. The deliberate, separate command: it is the
# only thing in the repository allowed to overwrite a committed screenshot.
set -euo pipefail
exec "$(dirname "$0")/visual-run.sh" update
