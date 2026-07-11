# PipeRouter release image (PRD §21.2): 3-stage build.
#
#   1. node    — Vite-build the Vue WebUI into internal/webui/dist
#   2. go      — //go:embed that dist and compile the static binary
#   3. runtime — minimal distroless image with just the binary + default config
#
# Build from the REPO ROOT (the build context must be the repository):
#   docker build -f deploy/Dockerfile -t piperouter:0.1.0 .
#
# Run (admin published to host loopback only — it has no auth):
#   docker run --rm -p 127.0.0.1:8080:8080 -p 127.0.0.1:9090:9090 piperouter:0.1.0

ARG VERSION=0.1.0

# ---------- Stage 1: frontend --------------------------------------------
# Monorepo layout so Vite can write outDir=../internal/webui/dist.
FROM node:24-alpine AS frontend
WORKDIR /src

COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci

# api/ is needed by the OpenAPI client generator (../api/openapi.yaml).
COPY api/ ./api/
COPY web/ ./web/
RUN cd web && npm run generate:api && npm run build
# → /src/internal/webui/dist/{index.html,assets/app.js,assets/app.css,...}

# ---------- Stage 2: backend ----------------------------------------------
FROM golang:1.26-alpine AS backend
ARG VERSION
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# dist/ is gitignored — inject the Vite output from the frontend stage.
COPY --from=frontend /src/internal/webui/dist/ internal/webui/dist/

RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/piperouter ./cmd/piperouter

# ---------- Stage 3: runtime ----------------------------------------------
# Distroless static: no shell, no package manager, runs as nonroot (65532).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=backend /out/piperouter /usr/local/bin/piperouter
# Default config baked into the image; docker-compose bind-mounts the same
# path so it can be edited without rebuilding. Inside a container the admin
# API listens on 0.0.0.0 (see deploy/piperouter.yaml for why, and why the
# startup security warning is expected).
COPY deploy/piperouter.yaml /etc/piperouter/piperouter.yaml

# 8080 = proxy (data plane), 9090 = admin API + WebUI (control plane, NO AUTH:
# publish it to host loopback only, e.g. -p 127.0.0.1:9090:9090).
EXPOSE 8080 9090

ENTRYPOINT ["/usr/local/bin/piperouter", "serve", "--config", "/etc/piperouter/piperouter.yaml"]
