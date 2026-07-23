# PipeRouter build (PRD §21.1). `make build` produces dist/piperouter with
# the WebUI embedded; no Node runtime is needed at run time (§20.2).
#
# Frontend embed pipeline (no manual copy / no content-hash patching):
#   web/ ──vite──▶ internal/webui/dist/ ──go:embed──▶ binary
#
# dist/ is fully gitignored. `ensure-embed` drops a throwaway .gitkeep when
# no real UI is present so //go:embed always has at least one file.

VERSION ?= 0.3.5

GO      ?= go
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build ensure-embed frontend generate backend test vet run clean

all: build

# Full release build: frontend (into embed dir) → tests → binary.
build: frontend test backend

# Ensure internal/webui/dist exists and is non-empty for //go:embed.
# - After `make frontend`: real index.html + assets/app.* (leave untouched).
# - Otherwise: create dist/.gitkeep so go test/build compile without Node.
ensure-embed:
	mkdir -p internal/webui/dist
	@if [ ! -f internal/webui/dist/index.html ] && [ ! -f internal/webui/dist/.gitkeep ]; then \
		touch internal/webui/dist/.gitkeep; \
	fi

# Install frontend deps, regenerate the OpenAPI TypeScript client from
# api/openapi.yaml, then Vite-build straight into internal/webui/dist
# (see web/vite.config.ts outDir).
frontend:
	cd web && npm install && npm run generate:api && npm run build

# Regenerate the OpenAPI TypeScript client from api/openapi.yaml.
generate:
	cd web && npm run generate:api

# Compile the release binary (static flags, stamped version).
backend: ensure-embed
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/piperouter ./cmd/piperouter

test: ensure-embed
	$(GO) test -race ./...

vet: ensure-embed
	$(GO) vet ./...

# Build the backend only and run it with the example configuration.
# For a current WebUI, run `make frontend` first (or `make build`).
run: backend
	./dist/piperouter serve --config configs/example.yaml

# Drop compiled binary and the embed dir (fully regenerated next build).
clean:
	rm -rf dist web/dist internal/webui/dist
