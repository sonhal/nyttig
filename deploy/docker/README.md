# Running Nyttig in Docker

Three containers, built from the repository's `Dockerfile` and run by
`compose.yaml` here:

| Service      | Image                         | Listens on                     |
|--------------|-------------------------------|--------------------------------|
| `nyttigd`    | `ghcr.io/sonhal/nyttigd`      | gRPC on 9000, internal only    |
| `nyttig-api` | `ghcr.io/sonhal/nyttig-api`   | `127.0.0.1:7070` on the host   |
| `nyttig-web` | `ghcr.io/sonhal/nyttig-web`   | `127.0.0.1:7071` on the host   |

```
browser: https, auth ──▶ your proxy ─┬─ /api/* ─▶ 127.0.0.1:7070 nyttig-api ── daemon network, gRPC :9000 ──▶ nyttigd ──▶ feeds
                       (on the host) └─ /*     ─▶ 127.0.0.1:7071 nyttig-web
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

## Networks and what can reach the daemon

nyttigd serves its gRPC API in **plaintext** on port 9000. That API has full
read and write access and no authentication, so the compose file keeps it on
networks only nyttigd and nyttig-api join:

- `daemon` (internal: no route out): nyttigd and nyttig-api.
- `feeds`: nyttigd's way out to the internet.
- `api` and `web`: one service each, for their published ports. The web app
  can't reach nyttigd at all; it only serves the app, and the browser talks
  to `/api`.

Never publish port 9000 on a public address. On the host, root (and members
of the `docker` group, who are root in effect) can reach the port through
the container's IP. That's the trust boundary here, where the systemd setup
limits its Unix socket to the `nyttig` group.

## Using the TUI and CLI

The nyttigd image contains the `nyttig` client. On the server:

```bash
docker compose exec nyttigd nyttig --socket 127.0.0.1:9000          # the TUI
docker compose exec nyttigd nyttig list-sources --socket 127.0.0.1:9000
```

Subcommands read their flags before positional arguments:
`nyttig refresh --socket 127.0.0.1:9000 3`.

From your laptop, tunnel the port over SSH. Uncomment the `ports` lines
under `nyttigd` in `compose.yaml`, which publish 9000 on the server's loopback
only, then:

```bash
ssh -N -L 9000:127.0.0.1:9000 vps &
nyttig --socket 127.0.0.1:9000
```

SSH then provides the encryption and authentication. The mTLS listener of
the systemd setup (`[tls] listen = ":9090"`) isn't available here: nyttigd
refuses `[tls] listen` together with a TCP `socket`, so it can't open a
plaintext port by accident.

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
