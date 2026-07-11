# PipeRouter

PipeRouter is a lightweight HTTP distribution proxy with path rewriting and per-route outbound proxy selection.

It matches incoming requests by path prefix, rewrites the path onto a target URL, and forwards the request transparently — optionally through a per-route HTTP or SOCKS5 proxy. One static binary, one YAML file, no database.

```text
Request → Route Match → Path Rewrite → Transport Selection → Forward → Stream Response Back
```

A typical use case is a single self-hosted entry point for many upstream APIs, each reached over its own network link:

```text
/openai/*    → https://api.openai.com/v1/*   (via an HTTP proxy)
/deepseek/*  → https://api.deepseek.com/*    (direct)
/gemini/*    → https://generativelanguage.googleapis.com/*  (via SOCKS5)
```

PipeRouter never reads or rewrites request bodies, never touches your API keys, and adds no `X-Forwarded-*` headers — callers keep sending their own credentials straight to the upstream.

## Features

- **Prefix routing** — longest-prefix match on path-segment boundaries (`/openai` matches `/openai/models`, never `/openai2`)
- **Path rewrite** — strip or keep the matched prefix, join with the target base path; query strings and percent-encoding pass through untouched
- **Per-route transports** — `direct`, `http` proxy (CONNECT for HTTPS) and `socks5` outbound links, with pooled, reused connections
- **Streaming first** — SSE and WebSocket work out of the box; request and response bodies are streamed, never buffered, so gigabyte uploads and hour-long event streams are fine
- **Hot reload** — edit the YAML (or use the WebUI) and changes apply atomically without restarting; in-flight requests and open streams are never interrupted
- **Embedded WebUI** — dashboard, route/transport editors, recent request log and diagnostics, served from the binary itself
- **Single binary** — Go binary with the Vue 3 frontend embedded; no Node, no nginx at runtime
- **No database** — the YAML file is the only persistent state; metrics and recent logs live in bounded memory

## Quick start

**1. Get the binary** — download a release, or build from source (requires Go ≥ 1.26 and Node.js):

```bash
git clone https://github.com/kites262/piperouter
cd piperouter
make build          # → dist/piperouter (WebUI embedded)
```

**2. Create `piperouter.yaml`:**

```yaml
version: 1

server:
  proxy:
    listen: ":8080"
  admin:
    enabled: true
    listen: "127.0.0.1:9090"

transports:
  - name: jp-proxy
    type: http
    url: http://127.0.0.1:7890

routes:
  - name: openai
    prefix: /openai
    target: https://api.openai.com/v1
    strip_prefix: true
    transport: jp-proxy

  - name: github
    prefix: /github
    target: https://api.github.com
```

**3. Run it:**

```bash
./dist/piperouter serve --config piperouter.yaml
```

**4. Test it:**

```bash
# → https://api.github.com/repos/golang/go (direct)
curl http://127.0.0.1:8080/github/repos/golang/go

# → https://api.openai.com/v1/models (through jp-proxy);
#   PipeRouter passes your Authorization header through untouched
curl http://127.0.0.1:8080/openai/models -H "Authorization: Bearer $OPENAI_API_KEY"
```

Open the WebUI at [http://127.0.0.1:9090](http://127.0.0.1:9090).

A fully commented configuration lives at [`configs/example.yaml`](configs/example.yaml); the complete field reference is in [`docs/configuration.md`](docs/configuration.md).

## WebUI

The admin server embeds a Vue 3 single-page console:

- **Dashboard** — service status, uptime, config revision, request totals, error rate, P95 latency, route summaries and recent errors at a glance
- **Routes** — list, create, edit, enable/disable, delete and test routes; the editor validates names, prefixes and targets live and previews the final URL mapping before you save
- **Route detail** — the pipeline (prefix → rewrite → transport → target) plus per-route request counts, error rate, P50/P95/P99 latency and recent requests
- **Transports** — manage HTTP/SOCKS5 outbound proxies and see which routes use them; the built-in `direct` transport is shown but immutable
- **Logs** — the in-memory recent-request ring buffer, filterable by route and status class (bodies and sensitive headers are never recorded)
- **Diagnostics** — send a test request through a real route or transport and see each stage: route resolution, final URL, connection, TLS, HTTP status, timing
- **Settings** — listeners, TLS paths, log level and log-buffer capacity

The console is a dark-first "infrastructure console" and ships a light theme too — toggle it from the sidebar; your choice is remembered and the first visit follows your OS preference.

Everything the WebUI does goes through the JSON Admin API under `/api/v1` (spec: [`api/openapi.yaml`](api/openapi.yaml)), so you can script the same operations with `curl`.

## CLI reference

```text
piperouter [serve] [flags]    run the proxy (default command)
piperouter validate [flags]   validate a configuration file and exit
piperouter version            print version information
piperouter help               print usage
```

Flags for `serve`:

| Flag | Description |
| --- | --- |
| `--config string` | path to the configuration file (default `piperouter.yaml`) |
| `--proxy-listen string` | override `server.proxy.listen` (runtime only, not persisted) |
| `--admin-listen string` | override `server.admin.listen` (runtime only, not persisted) |
| `--disable-admin` | disable the admin API and WebUI |
| `--disable-web` | disable the WebUI (admin API stays on) |
| `--log-level string` | override `runtime.log_level` (`debug`\|`info`\|`warn`\|`error`) |

Flags for `validate`:

| Flag | Description |
| --- | --- |
| `--config string` | path to the configuration file (default `piperouter.yaml`) |

CLI flags take precedence over the configuration file and are never written back to it. `validate` exits `0` and prints `configuration valid`, or exits non-zero listing every problem, one per line.

## Configuration overview

The YAML file is the single source of truth. The two core objects:

**Route** — one prefix → target mapping:

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | string | yes | — | unique name, `[A-Za-z0-9][A-Za-z0-9._-]{0,63}` |
| `enabled` | bool | no | `true` | disabled routes never match |
| `prefix` | string | yes | — | path prefix, starts with `/`, unique; non-root prefix must not end with `/` |
| `target` | string | yes | — | absolute `http`/`https` URL; no query, fragment or userinfo |
| `strip_prefix` | bool | no | `true` | remove the matched prefix before joining with the target path |
| `transport` | string | no | `direct` | name of the outbound transport to use |

**Transport** — one outbound link:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | unique name (`direct` is reserved for the built-in transport) |
| `type` | enum | yes | `http` or `socks5` |
| `url` | string | yes | proxy URL (`http://host:port` / `socks5://host:port`); credentials are not supported |

The built-in `direct` transport (no proxy) always exists and must not be declared.

Global sections cover listeners, optional proxy TLS, log level, the recent-log buffer size and outbound timeouts. See [`docs/configuration.md`](docs/configuration.md) for every field, default, validation rule, the exact matching/rewrite semantics and the error-mapping table.

## Security

PipeRouter v0.1 has **no authentication** on the admin API or WebUI. Design accordingly:

- The admin listener defaults to `127.0.0.1:9090` — **loopback only**. Keep it that way on any shared or internet-facing machine. Binding a non-loopback address makes PipeRouter log a prominent security warning.
- To reach the WebUI remotely, put something you trust in front of it: an SSH tunnel (`ssh -N -L 9090:127.0.0.1:9090 you@server`), a VPN (WireGuard/Tailscale), or a reverse proxy such as Caddy with authentication and TLS. See [`docs/deployment.md`](docs/deployment.md).
- The admin API never emits CORS headers and rejects mutating requests with a cross-origin `Origin` header.
- Access logs and the WebUI never record request/response bodies, query strings, or sensitive headers such as `Authorization` and `Cookie`.
- Proxy URLs and targets must not contain credentials (`user:pass@`) — validation rejects them.

> **YAML caveat:** saving from the WebUI re-serializes the file as canonical YAML. **Comments, key order and hand formatting are lost.** The previous version is kept as `<config>.bak`, but keep a commented master copy elsewhere if you care about it.

## Deployment

See [`docs/deployment.md`](docs/deployment.md) for systemd units, Docker/Compose, a Caddy-in-front TLS walkthrough and admin-exposure guidance. Deployment files live in [`deploy/`](deploy/).

PipeRouter does not do ACME. For public HTTPS, either terminate TLS in Caddy (recommended) or point `server.proxy.tls` at existing certificate files.

## Development

```bash
make build      # full release build: frontend → embed → go test -race → dist/piperouter
make backend    # compile the Go binary only (uses whatever is in internal/webui/dist)
make frontend   # npm install + build the WebUI into web/dist
make embed      # copy web/dist → internal/webui/dist for go:embed
make generate   # regenerate the OpenAPI TypeScript client from api/openapi.yaml
make test       # go test -race ./...
make vet        # go vet ./...
make run        # build backend and run with configs/example.yaml
make clean      # remove dist/ and web/dist
```

For frontend work, run the backend and the Vite dev server side by side:

```bash
# terminal 1 — backend (admin API on 127.0.0.1:9090)
make run

# terminal 2 — WebUI with hot module reload
cd web && npm install && npm run dev
```

The dev server proxies `/api` to `http://127.0.0.1:9090` (see `web/vite.config.ts`), keeping the backend's same-origin checks happy.

Repository layout:

```text
cmd/piperouter/    CLI entry point (serve | validate | version)
internal/          Go packages: proxy, router, transport, config, runtime,
                   metrics, logging, diagnostics, api, webui, app
web/               Vue 3 + TypeScript + Vite + Tailwind WebUI
api/openapi.yaml   Admin API contract (frontend client is generated from it)
configs/           example configuration
deploy/            Dockerfile, docker-compose, Caddy examples
docs/              configuration & deployment guides, architecture contract
test/              integration tests
```

## Non-goals

PipeRouter stays a simple, quiet pipe. v0.1 deliberately does **not** do: credential management or header injection, user accounts/RBAC/multi-tenancy, body parsing or rewriting, token counting/billing/quotas/rate limiting, load balancing, retries or transport fallback, health checks or service discovery, TCP/UDP proxying, plugins, ACME/automatic certificates, or persistent log/metric storage. If a feature requires understanding *who* is calling or *what* the request means, it does not belong here.

## License

Released under the [MIT License](LICENSE).
