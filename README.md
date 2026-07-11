<div align="center">

# PipeRouter

**A lightweight, single-binary HTTP distribution proxy.**
Match by path prefix, rewrite onto a target, and forward transparently — each route over its own outbound link (`direct` / HTTP / SOCKS5).

One static binary. One YAML file. No database.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![WebUI](https://img.shields.io/badge/WebUI-Vue_3-42b883?logo=vuedotjs&logoColor=white)](web/)
[![Release](https://img.shields.io/badge/release-v0.1.0-6d7cff)](#)
[![Single binary](https://img.shields.io/badge/single_binary-~8MB-2ea043)](#)
[![Database](https://img.shields.io/badge/database-none-lightgrey)](#)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

```text
 Request ─▶ Route Match ─▶ Path Rewrite ─▶ Transport Select ─▶ Forward ─▶ Stream Response Back
             prefix           strip/keep      direct·http·socks5     no body buffering, ever
```

</div>

---

PipeRouter is one self-hosted entry point in front of many upstreams — each reached over its own
network path, without touching what flows through it. It **never reads or rewrites request bodies,
never touches your API keys, and adds no `X-Forwarded-*` headers.** Callers keep sending their own
credentials straight to the upstream; PipeRouter just decides *where the request goes, how the path
changes, and which link it travels on.*

```text
/openai/*    →  https://api.openai.com/v1/*                     via jp-proxy   (HTTP proxy)
/deepseek/*  →  https://api.deepseek.com/*                      direct
/gemini/*    →  https://generativelanguage.googleapis.com/*     via us-socks   (SOCKS5)
/github/*    →  https://api.github.com/*                        direct
```

That's the whole idea: a quiet, high-performance pipe you can see through — configured in YAML or
from an embedded web console, reloaded live, with zero moving parts to operate.

## ✨ Features

| | |
| --- | --- |
| 🎯 **Prefix routing** | Longest-prefix match on path-segment boundaries — `/openai` matches `/openai/models`, never `/openai2`. Declaration order never matters. |
| ✂️ **Path rewrite** | Strip or keep the matched prefix, join with the target base path. Query strings and percent-encoding (`%2F`, spaces) pass through byte-for-byte. |
| 🔀 **Per-route transports** | `direct`, `http` (CONNECT tunnelling for HTTPS) and `socks5` outbound links — connection-pooled and reused across routes. |
| 🌊 **Streaming first** | SSE and WebSocket work out of the box. Request and response bodies are streamed, never buffered — gigabyte uploads and hour-long event streams are fine. |
| ♻️ **Hot reload** | Edit the YAML or use the WebUI; changes apply atomically. In-flight requests and open streams are never interrupted. Invalid config? It keeps serving the last good one. |
| 🖥️ **Embedded WebUI** | Dashboard, route/transport editors, live logs and diagnostics — a Vue 3 console served straight from the binary (dark & light). |
| 📦 **Single binary** | ~8 MB Go binary with the frontend embedded. No Node, no nginx, no sidecar at runtime. |
| 🗄️ **No database** | The YAML file is the only persistent state. Metrics and recent logs live in bounded memory and reset on restart. |
| 🔒 **Quiet & transparent** | Upstream status codes pass through unchanged; hop-by-hop headers handled per spec; no retries, no surprises. |

## ⚡ Performance

Data plane is allocation-light and never buffers a body. On an Apple M1 against a local upstream:

- **~47 µs** median added latency per request — the PRD target is p99 **< 5 ms**, a ~100× margin
- **~2.4 GB/s** streamed throughput
- **256 MiB** request bodies forwarded with an essentially **flat heap** (streamed, not buffered)
- **200+** concurrent SSE streams held open with no goroutine growth

Numbers vary by machine — reproduce them with `go test -bench . ./test/integration`.

## 🚀 Quick start

**1. Build** (needs Go ≥ 1.26 and Node.js) — or grab a release binary:

```bash
git clone https://github.com/kites262/piperouter
cd piperouter
make build          # → dist/piperouter  (WebUI embedded)
```

**2. Write `piperouter.yaml`:**

```yaml
version: 1

server:
  proxy:
    listen: ":8080"
  admin:
    listen: "127.0.0.1:9090"   # admin API + WebUI, loopback only

transports:
  - name: jp-proxy
    type: http
    url: http://127.0.0.1:7890

routes:
  - name: openai
    prefix: /openai
    target: https://api.openai.com/v1
    strip_prefix: true
    transport: jp-proxy        # this route goes out through the HTTP proxy

  - name: github
    prefix: /github
    target: https://api.github.com   # transport defaults to built-in "direct"
```

**3. Run & test:**

```bash
./dist/piperouter serve --config piperouter.yaml

# → https://api.github.com/repos/golang/go   (direct)
curl http://127.0.0.1:8080/github/repos/golang/go

# → https://api.openai.com/v1/models   (through jp-proxy);
#   your Authorization header is forwarded untouched
curl http://127.0.0.1:8080/openai/models -H "Authorization: Bearer $OPENAI_API_KEY"
```

Then open the console at **[http://127.0.0.1:9090](http://127.0.0.1:9090)**.

> A fully-commented config lives at [`configs/example.yaml`](configs/example.yaml); every field, default
> and validation rule is documented in [`docs/configuration.md`](docs/configuration.md).

## 🖥️ WebUI

The admin server embeds a Vue 3 single-page console — a dark-first "infrastructure console" with a
light theme too (toggle in the sidebar; your choice is remembered, first visit follows your OS).

| Module | What it does |
| --- | --- |
| **Dashboard** | Service status, uptime, config revision, request totals, error rate, P95 latency, route summaries and recent errors — health at a glance. |
| **Routes** | List, create, edit, enable/disable, delete and test routes. The editor validates names, prefixes and targets live and previews the final URL mapping before you save. |
| **Route detail** | The pipeline (prefix → rewrite → transport → target) plus per-route counts, error rate, P50/P95/P99 latency and recent requests. |
| **Transports** | Manage HTTP/SOCKS5 proxies and see which routes use each. The built-in `direct` transport is shown but immutable. |
| **Logs** | The in-memory recent-request ring buffer, filterable by route and status class. Bodies and sensitive headers are never recorded. |
| **Diagnostics** | Send a test request through a real route or transport and watch every stage: resolution, final URL, connection, TLS, HTTP status, timing. |
| **Settings** | Listeners, TLS paths, log level and log-buffer capacity. |

Everything the UI does goes through the JSON Admin API under `/api/v1`
(spec: [`api/openapi.yaml`](api/openapi.yaml)) — so you can script the same operations with `curl`.

## 🧭 How routing works

Matching is **path-prefix only**, on segment boundaries, **longest prefix wins**:

```text
Routes: /api   /api/openai   /api/openai/v1
GET /api/openai/v1/models   →  matched by  /api/openai/v1
GET /api/openai/models      →  matched by  /api/openai
GET /apiv2/...              →  404 route_not_found   (not a segment boundary)
```

Rewrite joins the target base path with the remaining request path, preserving the query string
and percent-encoding exactly:

```text
prefix /openai   target https://api.example.com/v1   strip_prefix: true
  /openai/chat/completions?stream=true   →   https://api.example.com/v1/chat/completions?stream=true

strip_prefix: false
  /openai/chat   →   https://api.example.com/v1/openai/chat
```

The `Host` header is set to the target host. Connection failures map to `502`, header timeouts to
`504`, and a client that hangs up mid-request gets no response written — see the full error table in
[`docs/configuration.md`](docs/configuration.md).

## ⚙️ Configuration

The YAML file is the single source of truth. Two core objects:

**Route** — one prefix → target mapping:

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | string | yes | — | unique, `[A-Za-z0-9][A-Za-z0-9._-]{0,63}` |
| `enabled` | bool | no | `true` | disabled routes never match |
| `prefix` | string | yes | — | path prefix; starts with `/`, unique; non-root must not end with `/` |
| `target` | string | yes | — | absolute `http`/`https` URL; no query, fragment or userinfo |
| `strip_prefix` | bool | no | `true` | remove the matched prefix before joining the target path |
| `transport` | string | no | `direct` | outbound transport to use |

**Transport** — one outbound link:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | unique (`direct` is reserved for the built-in transport) |
| `type` | enum | yes | `http` or `socks5` |
| `url` | string | yes | `http://host:port` / `socks5://host:port`; credentials not supported |

The built-in `direct` transport (no proxy) always exists and must not be declared. Global sections
cover listeners, optional proxy TLS, log level, recent-log buffer size and outbound timeouts.

## 🔒 Security

> PipeRouter v0.1 has **no authentication** on the admin API or WebUI. Treat the admin plane as
> privileged and design your deployment accordingly.

- The admin listener defaults to **`127.0.0.1:9090` — loopback only**, where it also enforces a
  loopback `Host` check to blunt DNS-rebinding. Keep it that way on any shared or internet-facing
  host; binding a non-loopback address logs a prominent security warning (and relaxes the `Host`
  check so a fronting proxy can forward its own host name).
- To reach the WebUI remotely, front it with something you trust: an SSH tunnel
  (`ssh -N -L 9090:127.0.0.1:9090 you@server`), a VPN (WireGuard/Tailscale), or an authenticating
  reverse proxy such as Caddy. See [`docs/deployment.md`](docs/deployment.md).
- The admin API never emits CORS headers and rejects cross-origin mutating requests.
- Access logs and the WebUI never record bodies, query strings, or sensitive headers
  (`Authorization`, `Cookie`, …). Proxy/target URLs with embedded credentials are rejected.

> **YAML caveat:** saving from the WebUI re-serializes the file as canonical YAML — **comments, key
> order and formatting are lost** (the previous version is kept as `<config>.bak`). Keep a commented
> master copy elsewhere if you care about it.

## 📦 Deployment

PipeRouter does **not** do ACME. For public HTTPS, terminate TLS in Caddy (recommended) or point
`server.proxy.tls` at existing certificate files.

```text
Client ──HTTPS──▶ Caddy ──HTTP──▶ PipeRouter ──▶ upstreams
```

A 3-stage **distroless Docker image (~17 MB)**, a `docker-compose` + Caddy example and systemd unit
live in [`deploy/`](deploy/); the full walkthrough is in [`docs/deployment.md`](docs/deployment.md).

## 🛠️ CLI

```text
piperouter [serve] [flags]     run the proxy (default command)
piperouter validate [flags]    validate a configuration file and exit
piperouter version             print version information
```

| Flag (for `serve`) | Description |
| --- | --- |
| `--config <path>` | configuration file (default `piperouter.yaml`) |
| `--proxy-listen <addr>` | override `server.proxy.listen` (runtime only, not persisted) |
| `--admin-listen <addr>` | override `server.admin.listen` (runtime only, not persisted) |
| `--disable-admin` | disable the admin API and WebUI |
| `--disable-web` | disable the WebUI (admin API stays on) |
| `--log-level <level>` | `debug` \| `info` \| `warn` \| `error` |

CLI flags take precedence over the file and are never written back. `validate` exits `0` with
`configuration valid`, or non-zero listing every problem one per line.

## 🧪 Development

```bash
make build      # full release: frontend → embed → go test -race → dist/piperouter
make test       # go test -race ./...
make run        # build backend, run with configs/example.yaml
make generate   # regenerate the OpenAPI TypeScript client from api/openapi.yaml
```

Frontend work — run the backend and the Vite dev server side by side (the dev server proxies `/api`
to `127.0.0.1:9090`, keeping same-origin checks happy):

```bash
make run                              # terminal 1 — backend
cd web && npm install && npm run dev  # terminal 2 — WebUI with HMR
```

```text
cmd/piperouter/    CLI entry point (serve | validate | version)
internal/          Go packages: proxy, router, transport, config, runtime,
                   metrics, logging, diagnostics, api, webui, app
web/               Vue 3 + TypeScript + Vite + Tailwind WebUI
api/openapi.yaml   Admin API contract (the frontend client is generated from it)
configs/  deploy/  docs/  test/
```

## 🚫 Non-goals

PipeRouter stays a simple, quiet pipe. It deliberately does **not** do: credential management or
header injection, user accounts / RBAC / multi-tenancy, body parsing or rewriting, token counting /
billing / quotas / rate limiting, load balancing, retries or transport fallback, health checks or
service discovery, TCP/UDP proxying, plugins, ACME, or persistent log/metric storage.

> If a feature needs to understand *who* is calling or *what* a request means, it doesn't belong
> here. PipeRouter only decides where a request goes, how its path changes, and which link it takes.

## 📄 License

Released under the [MIT License](LICENSE).
