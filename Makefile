.DEFAULT_GOAL := help

GGG := bin/ggg

# FUZZTIME is per fuzz target, so `make fuzz` stays bounded at the number of
# targets times this. CI overrides it for a longer soak.
FUZZTIME ?= 8s

$(GGG):
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
