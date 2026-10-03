# Running Nyttig in Docker

Three containers, built from the repository's `Dockerfile` and run by
`compose.yaml` here:

| Service      | Image                         | Listens on                     |
|--------------|-------------------------------|--------------------------------|
| `nyttigd`    | `ghcr.io/sonhal/nyttigd`      | a Unix socket in a shared volume; optionally mTLS on a host address you pick |
| `nyttig-api` | `ghcr.io/sonhal/nyttig-api`   | `127.0.0.1:7070` on the host   |
| `nyttig-web` | `ghcr.io/sonhal/nyttig-web`   | `127.0.0.1:7071` on the host   |

```
browser: https, auth ──▶ your proxy ─┬─ /api/* ─▶ 127.0.0.1:7070 nyttig-api ── unix socket (volume) ──▶ nyttigd ──▶ feeds
                       (on the host) └─ /*     ─▶ 127.0.0.1:7071 nyttig-web                                ▲
laptop:  nyttig (TUI) ── mTLS, optional (compose.mtls.yaml) ─────────────────────────────────────────────┘
```

The compose file doesn't include a reverse proxy: bring your own for TLS and
authentication. nyttig-api has **no authentication of its own**, so never
publish 7070 or 7071 on a public address. `deploy/Caddyfile` already routes
to these two ports and works unchanged with a Caddy on the host.

For a VPS without Docker, see [`../README.md`](../README.md) (systemd units).

## Run it

You need Docker Engine with the compose plugin (`docker compose version`).

```bash
cd deploy/docker               # from a checkout or an unpacked release bundle
cp .env.example .env           # set NYTTIG_ORIGIN to the URL the browser uses
$EDITOR config.toml            # optional: seed [[sources]], [[tags]], ...
docker compose up -d
docker compose ps              # nyttigd and nyttig-web report "healthy"
docker compose logs nyttigd    # expect "database opened" and "gRPC server listening"
```

`docker compose up` pulls the released images. Set `NYTTIG_VERSION` in `.env`
to a release (`0.5.0`, without the `v`) rather than `latest`, so an upgrade
only happens when you change it. To build the images from a checkout instead
(say, an unreleased commit), run `docker compose up -d --build`. Locally built
images report the version `dev`, and they replace the pulled tag on this
machine until you pull again.

The GHCR packages are created private on the first release; until the owner
makes them public, `docker login ghcr.io` first or build them yourself.

Then point your proxy at the two ports and open `NYTTIG_ORIGIN`.

### Reverse proxy

nyttig-api checks the `Host` and `Origin` headers against `NYTTIG_ORIGIN`, so
the proxy must pass `Host` through unchanged (Caddy does by default; nginx
needs `proxy_set_header Host $host;`). It must not buffer `/api/stream`
(Server-Sent Events): Caddy's `flush_interval -1`, nginx's
`proxy_buffering off;`. The Caddyfile has the rest: basic auth, headers, and
the public manifest and icons.

## Configuration

- **`.env`**: `NYTTIG_ORIGIN` (required; nyttig-api's `--origin` and the app's
  `ORIGIN`) and `NYTTIG_VERSION` (the image tag).
- **`config.toml`**: nyttigd's config, mounted read-only. It has the same keys
  as [`sample_config.toml`](../../sample_config.toml), except that the compose
  command sets `socket` and `db_path`. It turns on `block_private_addresses`,
  because anyone with the web app can add a feed URL: with it on, nyttigd
  refuses to fetch from loopback, private and link-local addresses, which
  include the other containers, the host and cloud metadata endpoints.
  Turn it off only to follow feeds on your own network.
- **Data**: the named volume `nyttig-data` holds the SQLite database
  (`/var/lib/nyttig/nyttig.db`). The containers run as non-root users
  (nyttigd as uid 10001), so if you bind-mount a host directory there
  instead, `chown 10001:10001` it first.

Every service runs read-only, with no capabilities, `no-new-privileges` and
log files capped at 30 MB.

## What can reach the daemon

nyttigd serves its gRPC API in **plaintext** on the Unix socket
`/run/nyttig/nyttig.sock`. That API has full read and write access and no
authentication, so no network carries it:

- The socket lives in the `nyttig-socket` volume, an in-memory tmpfs owned
  by uid 10001 with mode 0700. Only nyttigd and nyttig-api mount it, and
  both run as uid 10001. nyttig-api never mounts the database volume.
- Networks: `feeds` is nyttigd's way out to the internet; `api` and `web`
  each hold one service, for its published port. The web app can't reach
  nyttigd at all: it only serves the app, and the browser talks to `/api`.
- The only way in from outside is the optional mTLS port (below), which
  requires a client certificate signed by your CA at the TLS handshake.

On the host, root (and members of the `docker` group, who are root in
effect) can still reach the socket, as with any container's files.

## Using the TUI and CLI

The nyttigd image contains the `nyttig` client. On the server, it uses the
socket:

```bash
docker compose exec nyttigd nyttig --socket /run/nyttig/nyttig.sock          # the TUI
docker compose exec nyttigd nyttig list-sources --socket /run/nyttig/nyttig.sock
```

Subcommands read their flags before positional arguments:
`nyttig refresh --socket /run/nyttig/nyttig.sock 3`.

### The TUI over mutual TLS (opt-in)

`compose.mtls.yaml` makes nyttigd also serve mutual TLS on port 9090,
published on a host address you choose. This is the same listener as
`[tls] listen` in the systemd setup, so the daemon checks client
certificates itself. Nothing else changes: nyttig-api keeps using the socket.

1. **Certificates, on your workstation.** List every name and address the
   TUI will dial; a client dialing an IP address only accepts an IP entry in
   the certificate. Brackets around an IPv6 address are optional.

   ```bash
   scripts/gen-certs.sh ./certs nyttig.example.com 2001:db8::10
   ```

   Copy `server.pem`, `server.key` and `ca.pem` to `deploy/docker/tls/` on
   the server, and keep `ca.key` offline (see the limitations under
   "Certificates" in [`../README.md`](../README.md#2-certificates-on-your-workstation)).

2. **Make the key readable by nyttigd only** (uid 10001 in the container):

   ```bash
   sudo chown -R 10001:10001 tls
   sudo chmod 0400 tls/server.key
   ```

3. **Turn it on in `.env`** with the address to publish on:

   ```bash
   COMPOSE_FILE=compose.yaml:compose.mtls.yaml
   NYTTIG_TLS_PUBLISH=[2001:db8::10]:9090
   ```

   There is no default on purpose. Docker's published ports bypass the host
   firewall (ufw doesn't see them), so bind one interface address rather than
   every address, and restrict the port further with your provider's
   firewall if you can. An IPv6 address works with Docker's default
   userland proxy, which forwards to the container over IPv4; nyttigd then
   logs Docker's gateway as the peer, not your laptop.

4. **Start it and connect:**

   ```bash
   docker compose up -d
   docker compose logs nyttigd | grep 'mutual TLS enabled'
   ```

   ```bash
   nyttig --socket '[2001:db8::10]:9090' \
     --tls-cert certs/client.pem --tls-key certs/client.key --tls-ca certs/ca.pem
   ```

A client certificate from another CA fails at the handshake (`unknown
certificate authority`). There is no revocation: if a client key leaks,
generate a new CA and re-issue every certificate.

## Backups

SQLite's online backup is consistent while nyttigd writes; never copy a
live database file. The nyttigd image has the `sqlite3` CLI:

```bash
docker compose exec nyttigd sqlite3 /var/lib/nyttig/nyttig.db \
  ".timeout 30000" ".backup /var/lib/nyttig/backup.db"
docker compose cp nyttigd:/var/lib/nyttig/backup.db "nyttig-$(date +%F).db"
docker compose exec nyttigd rm /var/lib/nyttig/backup.db
```

Run that from cron for daily copies. To restore, stop nyttigd, copy the file
into the volume as `nyttig.db` (owned by 10001), and start it again.

## Upgrading

```bash
$EDITOR .env                   # NYTTIG_VERSION=<new release>
docker compose pull
docker compose up -d
```

nyttigd applies database migrations at startup; take a backup first. The
images carry their own SQLite and system libraries, so they are patched by
upgrading to a newer image, not by `apt upgrade` on the host. Releases
rebuild them from current Debian images.

## Building the images by hand

```bash
docker build --target nyttigd    --build-arg VERSION=v0.5.0 -t nyttigd .
docker build --target nyttig-api --build-arg VERSION=v0.5.0 -t nyttig-api .
docker build --target nyttig-web -t nyttig-web .
```

Run these from the repository root. The images are linux/amd64.
