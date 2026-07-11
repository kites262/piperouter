# Deployment Guide

PipeRouter ships as one static binary plus one YAML file, so deployment is deliberately boring. This guide covers the two common setups — a systemd service on a Linux host, and Docker Compose with Caddy in front for TLS — plus TLS options and how to expose the admin plane safely.

Ready-to-use copies of the Docker files referenced below live in [`deploy/`](../deploy/) (`Dockerfile`, `docker-compose.yaml`, `Caddyfile`).

Two facts shape everything here:

1. **The admin API and WebUI have no authentication in v0.1.** They bind `127.0.0.1:9090` by default and must never be reachable from an untrusted network without an authenticating layer in front.
2. **The config file's directory must be writable** by the PipeRouter process if you want to save from the WebUI: writes go through a same-directory temp file, an atomic rename, and a `<config>.bak` backup.

## systemd

Install:

```bash
# binary
sudo install -m 0755 dist/piperouter /usr/local/bin/piperouter

# dedicated user and config directory
sudo useradd --system --home /etc/piperouter --shell /usr/sbin/nologin piperouter
sudo mkdir -p /etc/piperouter
sudo cp configs/example.yaml /etc/piperouter/piperouter.yaml
sudo chown -R piperouter:piperouter /etc/piperouter

# always validate before (re)starting
sudo -u piperouter piperouter validate --config /etc/piperouter/piperouter.yaml
```

`/etc/systemd/system/piperouter.service`:

```ini
[Unit]
Description=PipeRouter HTTP distribution proxy
Documentation=https://github.com/kites262/piperouter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=piperouter
Group=piperouter
ExecStart=/usr/local/bin/piperouter serve --config /etc/piperouter/piperouter.yaml
Restart=on-failure
RestartSec=2

# On SIGTERM PipeRouter stops accepting connections and drains in-flight
# requests for up to 30s before exiting; give it room to finish.
TimeoutStopSec=40

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true

# WebUI saves rewrite the config atomically (temp file + rename + .bak),
# so the whole directory — not just the file — must stay writable:
ReadWritePaths=/etc/piperouter

# Only needed if the proxy listens on a port below 1024 (e.g. ":443"):
#AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now piperouter
journalctl -u piperouter -f        # structured logs go to stdout
```

Routine config changes need **no restart** — edit the file (or use the WebUI) and the watcher hot-reloads it; an invalid edit is rejected and the service keeps running on the last valid config. Only listener addresses and proxy TLS settings require `systemctl restart piperouter`.

## Docker

The image is built with a multi-stage `deploy/Dockerfile` (Node stage builds the WebUI, Go stage compiles the binary with the UI embedded, final stage is a minimal runtime image — no Node, no shell tooling). Build from the repository root:

```bash
docker build -f deploy/Dockerfile -t piperouter:latest .
```

Run it standalone:

```bash
mkdir -p config && cp configs/example.yaml config/piperouter.yaml

docker run -d --name piperouter \
  -p 8080:8080 \
  -v "$PWD/config:/etc/piperouter" \
  piperouter:latest \
  serve --config /etc/piperouter/piperouter.yaml
```

Container notes:

- **Mount the config directory, not the single file.** Atomic saves replace the file via rename and write a `.bak` next to it; a single-file bind mount pins one inode and breaks both saving and change detection.
- **Do not publish the admin port** (`-p 9090:9090`) unless an authenticating proxy sits in front. Inside a container the admin listener must bind a non-loopback address (e.g. `--admin-listen :9090`) to be reachable from *other containers* — PipeRouter then logs its non-loopback security warning, which is expected; keeping the port unpublished is what protects it.
- To run a pure data plane, add `--disable-admin`.

## Docker Compose + Caddy (recommended for public HTTPS)

This is the recommended production shape: Caddy terminates TLS (with automatic Let's Encrypt certificates) and adds authentication in front of the admin plane; PipeRouter stays plain HTTP on the internal compose network.

```text
Client ── HTTPS ──> Caddy ── HTTP ──> PipeRouter ──> Targets
```

`deploy/docker-compose.yaml`:

```yaml
services:
  piperouter:
    build:
      context: ..
      dockerfile: deploy/Dockerfile
    command:
      - serve
      - --config
      - /etc/piperouter/piperouter.yaml
      - --admin-listen
      - :9090        # reachable by Caddy on the compose network only
    volumes:
      - ./config:/etc/piperouter
    restart: unless-stopped
    # No "ports:" — nothing is exposed on the host directly.

  caddy:
    image: caddy:2
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    restart: unless-stopped
    depends_on:
      - piperouter

volumes:
  caddy_data:
  caddy_config:
```

`deploy/Caddyfile`:

```caddyfile
# Data plane — public, no auth (PipeRouter is transparent; callers bring
# their own upstream credentials).
proxy.example.com {
    reverse_proxy piperouter:8080
}

# Admin plane + WebUI — protect it. Generate the hash with:
#   docker run --rm caddy:2 caddy hash-password --plaintext 'your-password'
admin.example.com {
    basic_auth {
        admin <bcrypt-hash-here>
    }
    reverse_proxy piperouter:9090
}
```

Bring it up:

```bash
mkdir -p deploy/config && cp configs/example.yaml deploy/config/piperouter.yaml
# edit deploy/Caddyfile: your domains + password hash
docker compose -f deploy/docker-compose.yaml up -d
```

Caddy obtains and renews certificates automatically as long as both domains point at the host and ports 80/443 are reachable.

One subtlety when fronting the admin API: PipeRouter rejects mutating requests whose `Origin` header doesn't match the request's `Host` (its CSRF defense — there are no cookies or CORS). Caddy's `reverse_proxy` preserves the client's `Host` header by default, so the check passes. If you front it with nginx instead, keep the host intact: `proxy_set_header Host $host;`.

## TLS options

PipeRouter does **no ACME** — no automatic issuance or renewal. Two supported approaches:

### 1. Caddy (or another TLS terminator) in front — recommended

As in the compose setup above. Certificates are issued and renewed automatically, and you get a natural place to add authentication, rate limiting or access logs without violating PipeRouter's "quiet pipe" design. Any terminator works (nginx, HAProxy, a cloud load balancer); Caddy is just the least configuration.

### 2. Direct certificate files on the proxy listener

If you already manage certificates (corporate PKI, wildcard certs, `certbot` with your own hooks), the proxy listener can serve HTTPS itself:

```yaml
server:
  proxy:
    listen: ":443"
    tls:
      enabled: true
      cert_file: /etc/piperouter/fullchain.pem
      key_file: /etc/piperouter/private.key
```

Behavior and caveats:

- The certificate is loaded **at startup**; an invalid or unreadable cert/key pair prevents the proxy listener from starting (validation also rejects `enabled: true` with empty paths).
- Certificate **hot reload is not supported** in v0.1 — restart PipeRouter after renewing (e.g. a `certbot` deploy hook running `systemctl restart piperouter`).
- HTTPS clients get HTTP/2 via standard ALPN negotiation; WebSocket uses the HTTP/1.1 upgrade path.
- This covers the **proxy** listener only. The admin listener is always plain HTTP — one more reason to keep it on loopback or behind a TLS-terminating, authenticating proxy.

## Exposing the admin plane

In order of preference:

1. **Don't.** Leave `server.admin.listen: "127.0.0.1:9090"` and use an SSH tunnel when you need the WebUI:

   ```bash
   ssh -N -L 9090:127.0.0.1:9090 you@server
   # then open http://127.0.0.1:9090 locally
   ```

2. **VPN / overlay network.** With WireGuard or Tailscale, bind the admin listener to the VPN interface address (e.g. `--admin-listen 100.64.0.5:9090`). Only VPN peers can reach it. PipeRouter still logs the non-loopback warning — that's a reminder, not an error.

3. **Authenticating reverse proxy.** Caddy with `basic_auth` (or forward-auth to an SSO provider) as shown above. Keep PipeRouter's own listener unreachable from outside (loopback, or an unpublished container port).

Never bind the admin API to a public interface bare. There is no login, so anyone who can reach the port can read and rewrite your entire routing configuration. Additional guard rails that exist regardless: the admin API never emits CORS headers, rejects cross-origin mutating requests, and its logs/metrics never contain request bodies or sensitive headers — but none of that replaces authentication.

If a deployment doesn't need runtime management at all, run with `--disable-admin`: the data plane and config-file hot reload keep working, and the attack surface drops to zero.
