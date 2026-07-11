# PipeRouter build (PRD §21.1). `make build` produces dist/piperouter with
# the WebUI embedded; no Node runtime is needed at run time (§20.2).

VERSION ?= 0.1.0

GO      ?= go
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build frontend embed generate backend test vet run clean

all: build

# Full release build: frontend → embed → tests → binary.
build: frontend embed test backend

# Install frontend dependencies, regenerate the OpenAPI TypeScript client
# from api/openapi.yaml (PRD §21.1 step 2, keeps the client in sync with the
# contract), then build the WebUI bundle into web/dist.
frontend:
	cd web && npm install && npm run generate:api && npm run build

# Copy the frontend build output into the Go embed directory.
embed:
	rm -rf internal/webui/dist
	cp -r web/dist internal/webui/dist

# Regenerate the OpenAPI TypeScript client from api/openapi.yaml.
generate:
	cd web && npm run generate:api

# Compile the release binary (static flags, stamped version).
backend:
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/piperouter ./cmd/piperouter

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

# Build the backend only and run it with the example configuration.
run: backend
	./dist/piperouter serve --config configs/example.yaml

clean:
	rm -rf dist web/dist
