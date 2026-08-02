#!/usr/bin/env bash
# Downloads the Tailwind CSS v4 standalone CLI to bin/tailwindcss.
# Pinned version + sha256: an unpinned compiler turns the CI `git diff --exit-code`
# check red at random. Update the version and the hashes together.
set -euo pipefail

TAILWIND_VERSION="v4.3.3"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "${OS}-${ARCH}" in
  darwin-arm64)
    ASSET="tailwindcss-macos-arm64"
    SHA256="cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d"
    ;;
  linux-x86_64)
    ASSET="tailwindcss-linux-x64"
    SHA256="dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a"
    ;;
  *)
    echo "unsupported platform: ${OS}-${ARCH}" >&2
    echo "add the ${TAILWIND_VERSION} asset + sha256 for your platform to this script" >&2
    exit 1
    ;;
esac

DEST="bin/tailwindcss"
URL="https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/${ASSET}"

verify() {
  echo "${SHA256}  $1" | shasum -a 256 -c - >/dev/null 2>&1
}

if [[ -x "${DEST}" ]] && verify "${DEST}"; then
  echo "tailwindcss ${TAILWIND_VERSION} already present at ${DEST}"
  exit 0
fi

mkdir -p bin
echo "downloading ${URL}"
curl -sfL -o "${DEST}.tmp" "${URL}"
if ! verify "${DEST}.tmp"; then
  rm -f "${DEST}.tmp"
  echo "sha256 mismatch for ${ASSET} — refusing to install" >&2
  exit 1
fi
chmod +x "${DEST}.tmp"
mv "${DEST}.tmp" "${DEST}"
echo "installed tailwindcss ${TAILWIND_VERSION} → ${DEST}"
