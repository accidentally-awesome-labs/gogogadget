.DEFAULT_GOAL := help

GGG := bin/ggg

# FUZZTIME is per fuzz target, so `make fuzz` stays bounded at the number of
# targets times this. CI overrides it for a longer soak.
FUZZTIME ?= 8s

# bin/ggg is a real file target, so it needs real prerequisites. Without them
# make never rebuilds it once it exists, and every target below runs a stale
# engine against fresh manifests.
#
# `go list -deps` is the only accurate source for that list: the CLI compiles
# in far more than cmd/ggg, internal/gggcli and internal/modkit — internal/
# remote, internal/provision/*, internal/deploy/*, internal/database/ops/* and
# the generated command registry are all in the binary — and the set changes
# as modules are installed. Test files are excluded: .GoFiles omits them.
#
# Paths are made project-relative because make splits prerequisites on
# whitespace, so an absolute path under a directory whose name contains a
# space would break the list. A tree too incomplete to list (a fresh genesis,
# before generation has produced the sqlc and registry packages) yields no
# prerequisites, which is the right answer there: build the binary if it is
# absent, then let it generate. `ggg setup` builds bin/ggg itself and does not
# depend on this.
GGG_SOURCE := $(shell go list -deps -f '{{if and .Module (eq .Module.Path "github.com/gogogadget/gogogadget")}}{{$$dir := .Dir}}{{range .GoFiles}}{{$$dir}}/{{.}}{{"\n"}}{{end}}{{range .EmbedFiles}}{{$$dir}}/{{.}}{{"\n"}}{{end}}{{end}}' ./cmd/ggg 2>/dev/null | sed -e 's|^$(CURDIR)/||')

$(GGG): go.mod go.sum $(GGG_SOURCE)
	go build -o $(GGG) ./cmd/ggg

setup: $(GGG)
	$(GGG) setup

generate: $(GGG)
	$(GGG) generate

dev: $(GGG)
	$(GGG) dev

check: $(GGG)
	$(GGG) check

test: $(GGG)
	$(GGG) test unit

e2e: $(GGG)
	$(GGG) test e2e

visual: $(GGG)
	$(GGG) test visual

smoke: $(GGG)
	$(GGG) test smoke

seed: $(GGG)
	$(GGG) db seed

db-reset: $(GGG)
	$(GGG) db reset --yes

build: $(GGG)
	$(GGG) build

services-up: $(GGG)
	$(GGG) services up

services-down: $(GGG)
	$(GGG) services down

# Compatibility gates retained as single fixed operations until their dedicated
# typed modes are promoted; orchestration lives in ggg, never in Make.
#
# Every trust-boundary fuzz target runs in this one gate: a target no gate
# invokes is an unfuzzed parser.
fuzz:
	go test -run=^$$ -fuzz=FuzzFakeVerifier -fuzztime=$(FUZZTIME) ./internal/identity/
	go test -run=^$$ -fuzz=FuzzSanitizeFilename -fuzztime=$(FUZZTIME) ./internal/mail/dev/

e2e-ui:
	cd e2e && npx playwright test --ui

visual-update:
	./scripts/visual-update.sh

docker-build:
	docker build -t gogogadget:local .

help:
	@printf '%s\n' 'setup generate dev check test e2e visual smoke seed db-reset build services-up services-down'

.PHONY: setup generate dev check test e2e visual smoke seed db-reset build services-up services-down fuzz e2e-ui visual-update docker-build help
