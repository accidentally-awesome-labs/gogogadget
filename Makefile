.DEFAULT_GOAL := help

# `make setup` installs the pinned Tailwind binary under bin/. The Docker build
# stage has it on PATH instead, and overriding this one variable lets that stage
# call `make generate` rather than restating the pipeline.
TAILWIND ?= bin/tailwindcss

## setup: one-time local setup — deps, tools, vendored assets, .env
setup:
	@go version | grep -q 'go1.2[6-9]' || (echo 'Go >= 1.26 required' && exit 1)
	@command -v docker >/dev/null || (echo 'Docker required (compose db)' && exit 1)
	go mod download
	./scripts/setup-tools.sh
	./scripts/vendor-frontend.sh
	@[ -f .env ] || cp .env.example .env
	@echo ''
	@echo 'Next: docker compose up -d db && make seed && make dev'

## generate: regenerate ALL generated code (registry aggregates, templ, sqlc, tailwind). Never edit generated files.
generate:
	go run ./cmd/ggg sync --offline
	$(MAKE) --no-print-directory templ
	go tool sqlc generate
	$(MAKE) --no-print-directory css

# templ and css are separate targets because two other callers need exactly one
# tool step and must not pay for the rest: air re-runs templ on every save, and
# scripts/dev.sh pre-builds templ+css once before starting its watchers. Neither
# can run `ggg sync`, which refuses with `sha256 mismatch` whenever a
# manifest-owned source file has been edited without `ggg registry build` —
# i.e. during ordinary development. `generate` calls both so each tool
# invocation still has exactly one definition.
## templ: regenerate *_templ.go only (the narrow step air's rebuild hook needs)
templ:
	go tool templ generate

## css: rebuild static/app.css from input.css with the pinned Tailwind binary
css:
	$(TAILWIND) -i input.css -o static/app.css --minify

## dev: one-terminal dev loop — templ watch + tailwind watch + air (server restarts on save)
dev:
	./scripts/dev.sh

## check: THE one-command gate — generate + no-drift + vet + test + build
check: generate
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"
	go run ./cmd/ggg sync --check --offline
	go vet ./...
	go test ./...
	go build ./...

## test: unit + integration tests (integration self-skips without TEST_DATABASE_URL)
test:
	go test ./...

## fuzz: run both fuzzers briefly (CI runs this; make check deliberately does not)
fuzz:
	go test -run=^$$ -fuzz=FuzzFakeVerifier -fuzztime=15s ./internal/identity/
	go test -run=^$$ -fuzz=FuzzSanitizeFilename -fuzztime=15s ./internal/mail/

## e2e: Playwright end-to-end suite (real server + db + browser)
e2e:
	cd e2e && npx playwright test

## e2e-ui: Playwright interactive UI mode
e2e-ui:
	cd e2e && npx playwright test --ui

## visual: compare the committed visual baselines inside the pinned Playwright Linux container
visual:
	./scripts/visual.sh

## visual-update: regenerate visual baselines inside the pinned Playwright Linux container
visual-update:
	./scripts/visual-update.sh

## seed: load demo data (demo@gogogadget.dev / org_demo / 4 projects)
seed:
	go run ./cmd/seed -registry dev

## db-reset: destroy and recreate the local database
db-reset:
	docker compose down -v
	docker compose up -d db
	@sleep 2
	go run ./cmd/seed -reset -registry dev

## build: compile the server binary
build:
	go build -ldflags "-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o tmp/server ./cmd/server

## smoke: HTTP smoke test against a running stack (BASE_URL override allowed)
smoke:
	./scripts/smoke.sh

## docker-build: build the production image
docker-build:
	docker build -t gogogadget:local .

## help: list targets
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'

.PHONY: setup generate templ css dev check test fuzz e2e e2e-ui visual visual-update seed db-reset build smoke docker-build help
