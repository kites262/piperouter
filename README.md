<div align="center">

# PipeRouter

**One endpoint in front of all your APIs** — matched by path, rewritten, and sent out each on its own link.

[![CI](https://img.shields.io/github/actions/workflow/status/kites262/piperouter/ci.yml?branch=main&label=CI&logo=github)](https://github.com/kites262/piperouter/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/kites262/piperouter?label=release&color=6d7cff)](https://github.com/kites262/piperouter/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kites262/piperouter?logo=go&logoColor=white&label=Go)](go.mod)
[![WebUI](https://img.shields.io/badge/WebUI-Vue_3-42b883?logo=vuedotjs&logoColor=white)](web/)
[![License](https://img.shields.io/github/license/kites262/piperouter?color=blue)](LICENSE)

```text
/openai/*    →  api.openai.com/v1/*                    via jp-proxy   (HTTP)
/deepseek/*  →  api.deepseek.com/*                     direct
/gemini/*    →  generativelanguage.googleapis.com/*    via us-socks   (SOCKS5)
```

</div>

Point one hostname at PipeRouter and fan it out to as many upstreams as you like — each reached over
its own network path. It matches every request by path prefix, rewrites it onto the target, and
forwards it transparently by default. Optional Overwrite rules replace JSON string values
at selected paths, such as LLM model aliases. API keys pass straight through to the upstream.

Configure it in one YAML file or from the built-in web console, and reload live — no restarts.

```text
Request ─▶ Match prefix ─▶ Rewrite path ─▶ Pick transport ─▶ Forward ─▶ Stream response back
```

## ✨ Features

| | |
| --- | --- |
| 🎯 **Prefix routing** | Longest-prefix match on segment boundaries — `/openai` matches `/openai/models`, never `/openai2`. Order in the file never matters. |
| ✂️ **Path rewrite** | Strip or keep the matched prefix, join with the target path. Query strings and percent-encoding (`%2F`, spaces) pass through byte-for-byte. |
| 🔀 **Per-route egress** | Send each route out `direct`, through an `http` proxy (CONNECT for HTTPS), or over `socks5` — with pooled, reused connections. |
| 🌊 **Streaming first** | Responses, including SSE, stream immediately; WebSocket upgrades are preserved. Requests stream unless an enabled Overwrite rule requires bounded JSON buffering. |
| ✏️ **JSON Overwrite** | Reusable, opt-in string replacements by JSON path — map `.model` aliases and bind rules to selected routes. Off by default, with live rule updates. |
| ♻️ **Live reload** | Edit the YAML or use the console; changes apply atomically, mid-flight requests and open streams never drop. Bad config? It keeps serving the last good one. |
| 🖥️ **Built-in console** | A Vue 3 web UI — dashboard, route/transport editors, live logs and diagnostics — served straight from the binary (dark & light). |
| 📦 **Single binary** | One ~8 MB Go binary with the frontend embedded. Drop it on a box and run. No Node, no nginx, no sidecars. |
| 🔒 **Transparent** | Upstream status codes pass through unchanged; hop-by-hop headers handled per spec; no injected headers, no retries, no surprises. |

## ⚡ Fast and quiet

With Overwrite disabled, the data plane is allocation-light and does not buffer request bodies.
The following baseline measurements were taken on an Apple M1 against a local upstream:

- **sub-millisecond** overhead (~47 µs median added latency per request)
- **~2.4 GB/s** streamed throughput
- **256 MiB** request bodies forwarded with an essentially **flat heap** — streamed, not buffered
- **200+** concurrent SSE streams held open with no goroutine growth

Reproduce with `go test -bench . ./test/integration`.

## 🚀 Quick start

Build (needs Go ≥ 1.26 and Node.js) — or grab a release binary:

```bash
git clone https://github.com/kites262/piperouter
cd piperouter
make build          # → dist/piperouter  (web UI embedded)
```

Write `piperouter.yaml`:

```yaml
version: v0.3

server:
  proxy:
    listen: ":8080"
  admin:
    listen: "127.0.0.1:9090"   # admin API + web UI, loopback only

transports:
  - name: jp-proxy
    type: http
    url: http://127.0.0.1:7890

routes:
  - name: openai
    prefix: /openai
    options:
      target: https://api.openai.com/v1
      strip_prefix: true
      transport: jp-proxy      # this route goes out through the HTTP proxy

  - name: github
    prefix: /github
    options:
      target: https://api.github.com   # transport defaults to built-in "direct"
```

Run it, then hit it:

```bash
./dist/piperouter serve --config piperouter.yaml

# → https://api.github.com/repos/golang/go   (direct)
curl http://127.0.0.1:8080/github/repos/golang/go

# → https://api.openai.com/v1/models   (through jp-proxy);
#   your Authorization header is forwarded untouched
curl http://127.0.0.1:8080/openai/models -H "Authorization: Bearer $OPENAI_API_KEY"
```

Open the console at **[http://127.0.0.1:9090](http://127.0.0.1:9090)**.

> A fully-commented config lives at [`configs/example.yaml`](configs/example.yaml); every field, default
> and validation rule is documented in [`docs/configuration.md`](docs/configuration.md).

## 🖥️ Web console

A single-page Vue 3 console, dark-first with a light theme (toggle in the sidebar — your choice is
remembered, first visit follows your OS).

| Module | What it does |
| --- | --- |
| **Dashboard** | Service status, uptime, request totals, error rate, P95 latency, route summaries and recent errors — health at a glance. |
| **Routes** | List, create, edit, enable/disable, delete and test routes. The editor validates live and previews the final URL mapping before you save. |
| **Route detail** | The pipeline (prefix → rewrite → transport → target) plus per-route counts, error rate, P50/P95/P99 latency and recent requests. |
| **Transports** | Manage HTTP/SOCKS5 proxies and see which routes use each. |
| **Overwrite** | Create JSON path string replacement rules with a phase for each condition and action, and bind rules to proxy routes. Currently supports Req (request body). |
| **Logs** | The recent-request log, filterable by route and status class, with one-click clear of the memory buffer. Bodies and sensitive headers are never recorded. |
| **Diagnostics** | Send a test request, including POST JSON and headers, through a real route. Inspect the outbound body and watch resolution, URL, connection, TLS, status and timing. |
| **Settings** | Listeners, TLS, log level and log-buffer capacity. |

Everything the UI does goes through the JSON API under `/api/v1`
(spec: [`api/openapi.yaml`](api/openapi.yaml)) — script the same operations with `curl`.

## 🧭 How routing works

Matching is path-prefix only, on segment boundaries, **longest prefix wins**:

```text
Routes: /api   /api/openai   /api/openai/v1
GET /api/openai/v1/models   →  matched by  /api/openai/v1
GET /api/openai/models      →  matched by  /api/openai
GET /apiv2/...              →  404   (not a segment boundary)
```

Rewrite joins the target path with the remaining request path, preserving query and encoding exactly:

```text
prefix /openai   target https://api.example.com/v1   strip_prefix: true
  /openai/chat/completions?stream=true   →   https://api.example.com/v1/chat/completions?stream=true

strip_prefix: false
  /openai/chat   →   https://api.example.com/v1/openai/chat
```

The `Host` header is set to the target host. Connection failures map to `502`, header timeouts to
`504`; a client that hangs up mid-request gets no response written. Full error table in
[`docs/configuration.md`](docs/configuration.md).

## ⚙️ Configuration

**Route** — one prefix → backend mapping (`type: proxy` or `static`):

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | string | yes | — | unique, `[A-Za-z0-9][A-Za-z0-9._-]{0,63}` |
| `enabled` | bool | no | `true` | disabled routes never match |
| `type` | enum | no | `proxy` | `proxy` reverse-proxies to a URL; `static` serves one local file |
| `prefix` | string | yes | — | path prefix; starts with `/`, unique; non-root must not end with `/` |
| `target` | string | yes | — | `proxy`: absolute `http`/`https` URL; `static`: path to a **file** (absolute, or relative to the config file's directory) |
| `strip_prefix` | bool | no | `true` | `proxy` only: remove the matched prefix before joining the target path |
| `transport` | string | no | `direct` | `proxy` only: outbound transport to use |
| `overwrites` | string[] | no | `[]` | `proxy` only, inside `options`: names of reusable Overwrite rules |

**Transport** — one outbound link:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | unique (`direct` is the reserved built-in) |
| `type` | enum | yes | `http` or `socks5` |
| `url` | string | yes | `http://host:port` / `socks5://host:port`; credentials not supported |

The built-in `direct` transport always exists and must not be declared. Global sections cover
listeners, optional proxy TLS, log level and outbound timeouts.

## ✏️ LLM model aliases with Overwrite

In the **Overwrite** sidebar page, create a rule. Set its condition to **Req**, JSON path
`.model` equal to `gpt-6.1-sol`, and its action to **Req**, setting `.model` to `gpt-6.1-sol-1`.
Then select the proxy routes to bind.
Enable both the rule and the global switch to apply it; both default to off.
The equivalent YAML is:

```yaml
overwrite:
  enabled: true
  rules:
    - name: model-alias
      enabled: true
      condition:
        phase: req
        path: .model
        op: eq
        value: gpt-6.1-sol
      action:
        phase: req
        path: .model
        op: set
        value: gpt-6.1-sol-1

routes:
  - name: openai
    prefix: /openai
    options:
      target: https://api.openai.com/v1
      overwrites: [model-alias]
```

Condition and action each specify a phase, defaulting to `req`; currently only `req` is
supported. The first `.` represents the complete body in that phase. Paths can address
nested objects and array elements, for example `.reasoning.effort` and `.messages[0].model`; `.model` is the
editor default. Condition and action paths are independent: a match on `.model` can also
set another existing string field. Conditions compare exact strings, and actions change
existing strings only. Missing paths and other value types pass through. All conditions
read the original request, so replacements do not trigger further matches. Use
**Diagnostics** to send a POST JSON request with headers and inspect its **Outbound body**.
Rule changes and route bindings hot-reload atomically; existing SSE streams continue
uninterrupted.

An active binding buffers JSON request bodies before forwarding, even when no value matches.
Input and output are limited to 8 MiB each, reads to 30 seconds, and Body processing to eight
concurrent requests. This can increase time to first token for long or slow uploads; SSE
responses still stream chunk by chunk. Requests without bodies and WebSocket upgrades bypass
Overwrite. See [the full semantics and errors](docs/configuration.md#overwrite) before binding
routes that also accept uploads or non-JSON payloads.

## 🔒 Security

Start with `--access-token <token>` to require **Bearer authentication** for all admin API requests:

```bash
piperouter serve --config piperouter.yaml --access-token 'your-secret-token'
curl -H 'Authorization: Bearer your-secret-token' http://127.0.0.1:9090/api/v1/status
```

Open the console and enter the token in the **Access Token** field at the lower left, then click **Apply**.
The masked token is kept in the current tab's session storage; **Clear** removes it. The page and
static assets remain public so you can enter credentials. Missing or invalid credentials return
`401 Unauthorized`. The token is runtime-only and is never saved to YAML or returned by the API.
Without `--access-token`, the API remains unauthenticated.

- The admin listener defaults to **`127.0.0.1:9090` — loopback only**, where it also enforces a
  loopback `Host` check to blunt DNS-rebinding. Keep it that way on any shared or internet-facing
  host; binding a non-loopback address without a token logs a prominent warning.
- To reach the console remotely, front it with something you trust: an SSH tunnel
  (`ssh -N -L 9090:127.0.0.1:9090 you@server`), a VPN, or an authenticating reverse proxy such as
  Caddy. See [`docs/deployment.md`](docs/deployment.md).
- The admin API never emits CORS headers and rejects cross-origin mutating requests.
- Logs and the console never record bodies, query strings, or sensitive headers (`Authorization`,
  `Cookie`, …). Proxy/target URLs with embedded credentials are rejected.

## 📦 Deployment

PipeRouter does not do ACME. For public HTTPS, terminate TLS in Caddy (recommended) or point
`server.proxy.tls` at existing certificate files.

```text
Client ──HTTPS──▶ Caddy ──HTTP──▶ PipeRouter ──▶ upstreams
```

A distroless Docker image (~17 MB), a `docker-compose` + Caddy example and a systemd unit live in
[`deploy/`](deploy/); the full walkthrough is in [`docs/deployment.md`](docs/deployment.md).

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
| `--access-token <token>` | require `Authorization: Bearer <token>` for every admin API request (runtime only) |
| `--disable-admin` | disable the admin API and web console |
| `--disable-web` | disable the web console (admin API stays on) |
| `--log-level <level>` | `debug` \| `info` \| `warn` \| `error` |

CLI flags take precedence over the file and are never written back.

## 🧪 Development

```bash
make build      # full release: Vite → internal/webui/dist → go test -race → dist/piperouter
make frontend   # Vite-build the WebUI straight into the Go embed directory
make test       # ensure-embed + go test -race ./...
make run        # backend only (run `make frontend` first for a current UI)
make generate   # regenerate the OpenAPI TypeScript client from api/openapi.yaml
```

The WebUI is embedded with a single pipeline — no content-hash patching, no
manual copy step. `internal/webui/dist/` is fully gitignored; `make ensure-embed`
(seeded by `make test` / `make backend`) only adds a throwaway `.gitkeep` when
no UI has been built yet so `//go:embed` always has a file:

```text
web/  ── vite build ──▶  internal/webui/dist/  ── go:embed ──▶  binary
                         (assets/app.js + app.css, stable names)
```

Frontend work — run the backend and the Vite dev server side by side (the dev server proxies `/api`
to `127.0.0.1:9090`):

```bash
make run                              # terminal 1 — backend
cd web && npm install && npm run dev  # terminal 2 — web UI with HMR
```

```text
cmd/piperouter/    CLI entry point (serve | validate | version)
internal/          Go packages: proxy, router, transport, config, runtime,
                   metrics, logging, diagnostics, api, webui, app
web/               Vue 3 + TypeScript + Vite + Tailwind web UI
api/openapi.yaml   Admin API contract (the frontend client is generated from it)
configs/  deploy/  docs/  test/
```

## 📄 License

Released under the [MIT License](LICENSE).
