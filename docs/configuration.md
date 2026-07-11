# Configuration Reference

PipeRouter is configured by a single YAML file (default: `piperouter.yaml`, override with `--config`). The file is the **single source of truth**: there is no database, and everything the WebUI edits is written back to this file.

Check a file without starting the server:

```bash
piperouter validate --config piperouter.yaml
```

`validate` prints `configuration valid` and exits `0`, or lists **every** problem (one per line) and exits `1`.

## Parsing rules

- Parsing is **strict**: unknown fields are rejected. A typo like `stript_prefix` fails the load instead of being silently ignored.
- `version` is required and must be `1`.
- Durations are YAML strings in Go duration syntax: `10s`, `1m30s`, `500ms`.
- Booleans and integers left unset get the documented default. A duration set to `0s` (or left unset) also falls back to its default — timeouts cannot be disabled.

## Complete example

Every field, with its default value where one exists:

```yaml
version: 1                      # required; only 1 is supported

server:
  proxy:
    listen: ":8080"             # data-plane listener (all proxied traffic)
    tls:
      enabled: false            # optional TLS for the proxy listener
      cert_file: ""             # required when tls.enabled: true
      key_file: ""              # required when tls.enabled: true
  admin:
    enabled: true               # admin API + WebUI on/off
    listen: "127.0.0.1:9090"    # keep on loopback — no authentication!
  web:
    enabled: true               # WebUI (served by the admin server)

runtime:
  log_level: info               # debug | info | warn | error
  recent_logs: 1000             # in-memory access-log ring buffer; 0 disables

network:                        # global outbound tuning (see "Timeouts")
  dial_timeout: 10s
  tls_handshake_timeout: 10s
  response_header_timeout: 120s
  idle_connection_timeout: 90s

transports:                     # outbound proxy links ("direct" is built in)
  - name: jp-proxy
    type: http
    url: http://127.0.0.1:7890
  - name: us-socks
    type: socks5
    url: socks5://127.0.0.1:1080

routes:                         # prefix → target mappings
  - name: openai
    enabled: true
    prefix: /openai
    target: https://api.openai.com/v1
    strip_prefix: true
    strip_forward_headers: true # remove Forwarded/Via/X-Forwarded-* (default)
    transport: jp-proxy
  - name: github
    prefix: /github
    target: https://api.github.com
```

## Field reference

### Top level

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `version` | int | yes | — | config schema version; must be `1` |
| `server` | object | no | see below | listeners |
| `runtime` | object | no | see below | logging |
| `network` | object | no | see below | outbound timeouts |
| `transports` | list | no | `[]` | outbound proxy links |
| `routes` | list | no | `[]` | prefix routes |

### `server.proxy`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `listen` | string | `":8080"` | data-plane listen address (`host:port`; empty host binds all interfaces) |
| `tls.enabled` | bool | `false` | serve HTTPS on the proxy listener |
| `tls.cert_file` | string | `""` | PEM certificate chain; required (non-empty) when `tls.enabled: true` |
| `tls.key_file` | string | `""` | PEM private key; required (non-empty) when `tls.enabled: true` |

The certificate is loaded at startup; an invalid cert/key pair prevents the proxy listener from starting. PipeRouter does **not** do ACME or renewal — see [deployment.md](deployment.md) for the recommended Caddy-in-front setup.

### `server.admin` and `server.web`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `admin.enabled` | bool | `true` | run the admin API (and, with it, the WebUI) |
| `admin.listen` | string | `"127.0.0.1:9090"` | admin listen address; **loopback by default — there is no authentication** |
| `web.enabled` | bool | `true` | serve the embedded WebUI from the admin server |

With `admin.enabled: false` the admin API and WebUI are off; the proxy keeps running and the config can still be managed by editing the file (hot reload stays active). `web.enabled` has no effect while the admin server is disabled. Binding the admin listener to a non-loopback address logs a prominent security warning.

### `runtime`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `log_level` | string | `"info"` | application log level: `debug`, `info`, `warn` or `error` |
| `recent_logs` | int | `1000` | capacity of the in-memory access-log ring buffer shown in the WebUI; `0` disables it; must be ≥ 0 |

The ring buffer overwrites the oldest entries when full and is cleared on restart. Nothing is written to disk. Structured logs go to stdout.

### `network` — timeouts

Global outbound tuning, shared by all transports. There is deliberately **no overall request deadline**: response bodies may stream for hours (SSE, large downloads) without being cut off.

| Field | Default | Description |
| --- | --- | --- |
| `dial_timeout` | `10s` | maximum time to establish a TCP connection (including via a proxy) |
| `tls_handshake_timeout` | `10s` | maximum time for the TLS handshake with the upstream |
| `response_header_timeout` | `120s` | maximum wait for the upstream's response **headers** after the request is sent; exceeding it returns `504` |
| `idle_connection_timeout` | `90s` | how long pooled idle connections are kept before being closed |

Values are Go duration strings. Unset or `0s` means the default.

### `transports[]`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | unique; must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`; `direct` is reserved |
| `type` | enum | yes | `http` or `socks5` |
| `url` | string | yes | proxy URL; scheme must match the type (`http://` for `http`, `socks5://` for `socks5`), must have a host, must **not** contain userinfo (`user:pass@`) |

- The built-in transport **`direct`** (no proxy) always exists, must not be declared, and cannot be modified or deleted.
- `http` transports use a standard HTTP proxy for `http` targets and a CONNECT tunnel for `https` targets. TLS is established end-to-end between PipeRouter and the upstream — the proxy never sees plaintext.
- `socks5` transports dial through a SOCKS5 proxy; hostnames are resolved by the proxy itself.
- Proxy authentication is not supported in v0.1.
- Each transport owns one long-lived connection pool, shared by every route that references it.

### `routes[]`

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | string | yes | — | unique; must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}` |
| `enabled` | bool | no | `true` | disabled routes are kept in the file but never match |
| `prefix` | string | yes | — | path prefix to match; unique across routes (see rules below) |
| `target` | string | yes | — | absolute `http`/`https` URL; no query, fragment or userinfo |
| `strip_prefix` | bool | no | `true` | remove the matched prefix before joining with the target path |
| `strip_forward_headers` | bool | no | `true` | remove `Forwarded`, `Via` and `X-Forwarded-For/-Host/-Proto` before forwarding; `false` passes inbound values through unchanged |
| `transport` | string | no | `"direct"` | name of a declared transport, or `direct` |

## Validation rules

The loader rejects a configuration (listing **all** problems at once) when any of the following holds:

- `version` is not `1`, or an unknown field is present anywhere;
- a route name or transport name is duplicated, empty, or doesn't match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`;
- a transport is named `direct` (reserved);
- a transport `type` is not `http` or `socks5`;
- a proxy `url` is missing, unparsable, has the wrong scheme for its type, has no host, or contains userinfo;
- a route references a transport that doesn't exist (`direct` is always known);
- a route `prefix` is duplicated or violates the prefix rules below;
- a route `target` is missing, not an absolute URL, not `http`/`https`, has no host, or contains userinfo, a query, or a fragment;
- `tls.enabled: true` but `cert_file` or `key_file` is empty;
- `runtime.log_level` is not one of `debug`, `info`, `warn`, `error`;
- `runtime.recent_logs` is negative.

### Prefix rules

A prefix must:

- start with `/`;
- not contain `?` or `#`;
- not contain empty segments (`//`);
- not contain `..` segments;
- not end with `/` — unless it is exactly the root prefix `/`.

| Prefix | Verdict |
| --- | --- |
| `/openai` | valid |
| `/` | valid (root — matches everything) |
| `/openai/` | rejected — write `/openai` |
| `openai` | rejected — must start with `/` |
| `/a//b` | rejected — empty segment |

The loader rejects non-canonical prefixes rather than silently fixing them. The WebUI route editor trims a trailing `/` for you before saving.

## Matching semantics

Only **path-prefix** matching exists — no host, method, header, query or regex matching.

A route with `prefix: /openai` matches a request path `P` when:

```text
P == "/openai"   OR   P starts with "/openai/"
```

i.e. matching is **path-segment-boundary aware**:

| Request path | `/openai` matches? |
| --- | --- |
| `/openai` | yes |
| `/openai/` | yes |
| `/openai/models` | yes |
| `/openai2` | **no** |
| `/openai-test` | **no** |

When several routes match, the **longest prefix wins** — the order of routes in the file never affects the result. With prefixes `/api`, `/api/openai` and `/api/openai/v1` configured, a request to `/api/openai/v1/models` always hits `/api/openai/v1`.

The root prefix `/` matches every request (useful as a catch-all; any longer prefix still wins). Disabled routes are skipped entirely. If nothing matches, the client gets:

```http
HTTP/1.1 404 Not Found
Content-Type: application/json

{"error":"route_not_found"}
```

## Rewrite semantics

The upstream URL is built from the route's target and the request path:

```text
base  = target path with any trailing "/" removed   (target path "/" → "")
rest  = request path minus the prefix, if strip_prefix   (prefix "/" strips nothing)
      = the full request path, otherwise
final = base + rest;  if empty → "/"
```

Scheme and host always come from the target; the query string is preserved verbatim.

Examples (the third row is the canonical acceptance case):

| Prefix | Target | `strip_prefix` | Request | Upstream receives |
| --- | --- | --- | --- | --- |
| `/openai` | `https://api.example.com/v1` | `true` | `/openai` | `https://api.example.com/v1` |
| `/openai` | `https://api.example.com/v1` | `true` | `/openai/` | `https://api.example.com/v1/` |
| `/openai` | `https://api.example.com/v1` | `true` | `/openai/chat?stream=true` | `https://api.example.com/v1/chat?stream=true` |
| `/openai` | `https://api.example.com/v1` | `true` | `/openai/models` | `https://api.example.com/v1/models` |
| `/openai` | `https://example.com/v1` | `false` | `/openai/models` | `https://example.com/v1/openai/models` |
| `/github` | `https://api.github.com` | `true` | `/github/repos/golang/go` | `https://api.github.com/repos/golang/go` |
| `/` | `http://127.0.0.1:3000` | `true` | `/dashboard` | `http://127.0.0.1:3000/dashboard` |

What PipeRouter does **not** touch:

- **Query strings** — passed through byte-for-byte, including parameter order and a bare trailing `?`.
- **Percent-encoding** — `%2F`, `%20` etc. are never decoded, re-encoded or "cleaned"; the rewrite works on the escaped path.
- **Duplicate slashes** and other legal path oddities inside the remaining path.
- **Method, body, ordinary headers** — forwarded as-is (hop-by-hop headers such as `Connection`, `TE`, `Transfer-Encoding` are handled per the HTTP spec; WebSocket upgrades are preserved).

Two deliberate exceptions to full transparency:

- `Host` is always set to the **target's** host (there is no `preserve_host` option in v0.1).
- PipeRouter adds **no** `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `Forwarded` or `Via` headers — and by default it **removes** inbound ones (`strip_forward_headers: true`), so a fronting reverse proxy such as Caddy or nginx never leaks the real client IP or your public hostname to the target. Set `strip_forward_headers: false` on a route to pass the inbound values through unchanged (useful when the target is your own service and needs the client IP).

## Error mapping

Upstream HTTP responses — including `401`, `404`, `429`, `500` — are relayed **unchanged**. PipeRouter generates its own response only when it cannot get one from the upstream:

| Condition | Status | Body |
| --- | --- | --- |
| No route matched the request path | `404` | `{"error":"route_not_found"}` |
| DNS failure, connection refused/failed, dial timeout, HTTP-proxy CONNECT failure, SOCKS5 negotiation failure, TLS handshake failure, upstream closed the connection before responding | `502` | `{"error":"upstream_connection_failed"}` |
| Upstream connected but response headers didn't arrive within `network.response_header_timeout` | `504` | `{"error":"upstream_timeout"}` |
| WebSocket upgrade toward the upstream failed | `502` | `{"error":"websocket_upgrade_failed"}` |
| Unexpected internal error (recovered panic) | `500` | `{"error":"internal_error"}` |
| Client canceled the request | — | no response is written (logged as `client_canceled`) |

Error bodies are fixed JSON codes; they never leak upstream details, proxy URLs, credentials or file paths (those go to the application log only). There are **no automatic retries** — requests may have side effects, so every failure is reported to the caller immediately.

## Hot reload, revisions and persistence

### Editing the file by hand

PipeRouter watches the configuration file (rename-safe, with a short debounce so editors that write-then-rename work):

- **Valid change** → a new immutable runtime snapshot (route table + transport pools) is built and swapped in atomically. New requests use it immediately; in-flight requests, SSE streams and WebSockets keep running on the old snapshot undisturbed. No restart, ever.
- **Invalid change** → the process keeps serving with the **last valid configuration**, logs the reason, and reports the error through `GET /api/v1/status` (`config.valid: false` + `last_error`); the WebUI shows a persistent banner. PipeRouter never exits because of a bad config edit.

Listener addresses (`server.proxy.listen`, `server.admin.listen`) and proxy TLS settings cannot be re-applied without interruption — changing them requires a restart. Routes, transports, log level and log capacity hot-reload.

### Editing through the WebUI / Admin API

Every write goes through the same pipeline: apply change → full validation → build new snapshot → write a temp file in the same directory → `fsync` → back up the previous file to `<config>.bak` → atomic rename → swap the snapshot. A failed write leaves both the file and the running configuration untouched and returns a structured error.

### Revisions and concurrent edits

Each configuration content has a revision, `sha256:<hex>` of its canonical YAML form. Reads (`GET /api/v1/config`, route/transport GETs) return the current revision; mutating requests can carry it back in the JSON body. If the configuration changed in the meantime — another API client, or a hand edit picked up by the watcher — the API answers `409 Conflict` (`revision_conflict`) instead of overwriting, and the WebUI prompts you to reload. Omitting the revision skips the check.

### YAML caveat (important)

> Saving from the WebUI or Admin API re-serializes the configuration as canonical YAML. **Comments, key order, blank lines and hand formatting are lost.** The previous file content is preserved as `<config>.bak` (one generation only). If you maintain a heavily commented config, treat a copy outside the live path as your master.
