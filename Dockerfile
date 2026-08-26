# syntax=docker/dockerfile:1

# --- Toolchain: pinned Tailwind standalone binary (per-arch, sha256-verified)
# Debian (glibc) build stages: the Tailwind standalone binary is glibc-linked
# and does not run on Alpine's musl.
FROM golang:1.26-bookworm AS tools
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
ARG TARGETARCH
ARG TAILWIND_VERSION=v4.3.3
ARG TAILWIND_SHA256_X64=dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a
ARG TAILWIND_SHA256_ARM64=55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) asset=tailwindcss-linux-x64;  sha="${TAILWIND_SHA256_X64}" ;; \
      arm64) asset=tailwindcss-linux-arm64; sha="${TAILWIND_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -sfL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/${asset}"; \
    echo "${sha}  /usr/local/bin/tailwindcss" | sha256sum -c -; \
    chmod +x /usr/local/bin/tailwindcss

# --- Build
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY --from=tools /usr/local/bin/tailwindcss /usr/local/bin/tailwindcss
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# One definition of the pipeline: the same target CI and developers run, so the
# CSS/sqlc/registry output baked into the image cannot drift from the Makefile.
# TAILWIND override: the binary is on PATH here, and the build context may carry
# a host bin/tailwindcss built for another OS. golang:1.26-bookworm ships GNU
# Make 4.3, and --offline keeps ggg sync from needing the network.
RUN make generate TAILWIND=tailwindcss
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/server ./cmd/server

# --- Runtime: binary only — static/, content/, migrations are all embedded
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/server"]
