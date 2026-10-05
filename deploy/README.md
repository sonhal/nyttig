# Deploying to a VPS

A complete single-server setup on Debian 13 (trixie): the web client behind
Caddy for the browser, and the TUI over mutual TLS on port 9090. The SQLite
database is embedded in `nyttigd`, so there is no separate database server:
it is a file in `/var/lib/nyttig`.

To run it in containers instead (nyttigd, nyttig-api and the web app as
three images, behind a proxy you bring), see [`docker/README.md`](docker/README.md).

```
laptop:  nyttig (TUI) ── mTLS, :9090 ───────────────────────────────▶ nyttigd
browser: https, basic auth ──▶ Caddy ─┬─ /api/* ─▶ nyttig-api ── unix socket ──┘
                                      └─ /*     ─▶ nyttig-web (Node)
```

`nyttigd` serves both at once: the Unix socket `/run/nyttig/nyttig.sock`
(plaintext, `nyttig` group only) for nyttig-api, and `[tls] listen = ":9090"`
for the TUI.

| File                                   | Installed to                             |
|----------------------------------------|------------------------------------------|
| `systemd/nyttigd.service`              | `/etc/systemd/system/`                   |
| `systemd/nyttigd.service.d/mtls.conf`  | `/etc/systemd/system/nyttigd.service.d/` |
| `systemd/nyttigd-backup.{service,timer}` | `/etc/systemd/system/`                 |
| `systemd/nyttig-api.service`, `systemd/nyttig-web.service` | `/etc/systemd/system/` |
| `Caddyfile`                            | `/etc/caddy/Caddyfile`                   |

**Sizing:** 1 vCPU, 1 GB RAM and 20 GB disk are enough for personal use.
nyttigd, nyttig-api and Caddy each use tens of MB and the Node server a bit
more. Build in CI (or on your workstation), not on the server: the Go build with cgo
and the web app's Node build are heavy for a 1 GB machine. Nothing prunes old items yet, so the
database grows by roughly 0.2–2 GB per year depending on how many feeds you
follow.

## 1. Get a release (on your workstation)

CI builds the release: a `v*` tag runs the whole pipeline and, when it
passes, publishes a GitHub Release with one bundle and its checksum. Merges to
`main` tag themselves when their commit titles call for a release (`feat:` a
minor version, `fix:` or `perf:` a patch; see
[Releases](../AGENTS.md#releases)). To cut one by hand, push the tag:

```bash
git tag v0.1.0 && git push origin v0.1.0     # a tag with a hyphen is a pre-release
```

```
nyttig-v0.1.0-linux-amd64/
  bin/      nyttigd, nyttig, nyttig-api, nyttig-clef (see the Docker guide for the last)
  web/      build/ and package.json (the nyttig-web Node server)
  deploy/   these systemd units, the Caddyfile and this README
  sample_config.toml
```

Download and check it:

```bash
VERSION=v0.1.0
gh release download "$VERSION" --repo sonhal/nyttig
sha256sum -c SHA256SUMS
gh attestation verify "nyttig-$VERSION-linux-amd64.tar.gz" --repo sonhal/nyttig
```

`sha256sum -c` catches a corrupt download. The attestation proves the tarball
was built by this repository's CI workflow from the tagged commit, not
uploaded by hand.

`nyttigd` uses cgo and links the server's SQLite library (see
[SQLite](#sqlite)), so it only runs on systems with the same or a newer glibc
and libsqlite3 than it was built with. CI builds the Go binaries in a
`golang:1.26-trixie` container for that reason. If the server runs something
older than Debian 13, build it yourself in a matching image instead.

### Building it yourself

To deploy a commit that has no release, build the same layout. Build the Go
binaries in the same Debian release as the server:

```bash
BUNDLE=nyttig-dev-linux-amd64
docker run --rm -v "$PWD":/src -w /src golang:1.26-trixie \
  sh -c "apt-get update && apt-get install -y --no-install-recommends libsqlite3-dev &&
         git config --global --add safe.directory /src &&
         go build -trimpath -tags libsqlite3 -ldflags='-s -w' -o $BUNDLE/bin/ ./cmd/nyttigd ./cmd/nyttig ./cmd/nyttig-api ./cmd/nyttig-clef"

pnpm --dir web install --frozen-lockfile
pnpm --dir web build        # web/build/ is plain JavaScript; any OS can build it
mkdir -p "$BUNDLE/web" && cp -r web/build web/package.json "$BUNDLE/web/"
cp -r deploy sample_config.toml "$BUNDLE/"
tar -czf "$BUNDLE.tar.gz" "$BUNDLE"
```

## 2. Certificates (on your workstation)

```bash
scripts/gen-certs.sh ./certs nyttig.example.com   # the name the TUI will dial
```

The server gets `server.pem`, `server.key` and `ca.pem`. **Never copy `ca.key`
to the server:** anyone who has it can issue client certificates. Keep it
offline, since you need it to issue new certificates. Two limitations to plan for:

- **No revocation.** The daemon trusts any certificate the CA signed. If a
  client key leaks, generate a new CA and re-issue every certificate.
- **Expiry after 825 days.** Set a reminder. Re-running `gen-certs.sh` creates
  a new CA, so every client needs the new files too.

## 3. Install (on the server)

Debian 13 packages Node.js 20 and an older Caddy, but nyttig-web needs
Node.js 22+ and the Caddyfile's `basic_auth` needs Caddy 2.8+. Install Node.js
from [NodeSource](https://github.com/nodesource/distributions) and Caddy from
[Caddy's apt repository](https://caddyserver.com/docs/install#debian-ubuntu-raspbian),
then check `node --version` and `caddy version`.

Copy the bundle and the server's certificate files over, and unpack:

```bash
scp "nyttig-$VERSION-linux-amd64.tar.gz" certs/{server.pem,server.key,ca.pem} vps:
ssh vps
tar -xzf nyttig-v0.1.0-linux-amd64.tar.gz
mv server.pem server.key ca.pem nyttig-v0.1.0-linux-amd64/
cd nyttig-v0.1.0-linux-amd64
```

Then on the server:

```bash
sudo apt install sqlite3 ufw        # sqlite3: backups, and it brings libsqlite3-0, which nyttigd links
sudo useradd --system --home-dir /var/lib/nyttig --shell /usr/sbin/nologin nyttig

# Binaries and the web app
sudo install -m 0755 bin/nyttigd bin/nyttig bin/nyttig-api /usr/local/bin/
sudo install -d /opt/nyttig-web
sudo cp -r web/build web/package.json /opt/nyttig-web/

# TLS files, root-only; the mtls.conf drop-in hands them to nyttigd
sudo install -d -m 0700 /etc/nyttig/tls
sudo install -m 0644 server.pem ca.pem /etc/nyttig/tls/
sudo install -m 0600 server.key /etc/nyttig/tls/
shred -u server.key

# Units
sudo install -m 0644 deploy/systemd/*.service deploy/systemd/*.timer /etc/systemd/system/
sudo install -D -m 0644 deploy/systemd/nyttigd.service.d/mtls.conf \
  /etc/systemd/system/nyttigd.service.d/mtls.conf
sudo systemctl daemon-reload
```

Create `/etc/nyttig/config.toml` (`sudo install -D -o root -g nyttig -m 0640
/dev/null /etc/nyttig/config.toml`, then edit it). The unit's `--socket` and
`--db-path` flags set the rest:

```toml
log_level = "info"

# Sources can be added from the internet through nyttig-api.
block_private_addresses = true

# mTLS for the TUI. The files are copied here from /etc/nyttig/tls by
# LoadCredential= (deploy/systemd/nyttigd.service.d/mtls.conf).
[tls]
cert = "/run/credentials/nyttigd.service/server.pem"
key = "/run/credentials/nyttigd.service/server.key"
client_ca = "/run/credentials/nyttigd.service/ca.pem"
listen = ":9090"

# Add [[sources]], [[tags]] and [[tag_rules]] as in sample_config.toml.
```

Set your host name in nyttig-api and nyttig-web (see the comments at the top
of each unit), and in the Caddyfile along with your user and a
`caddy hash-password` hash:

```bash
sudo systemctl edit nyttig-api      # ExecStart with --origin https://<host>
sudo systemctl edit nyttig-web      # Environment=ORIGIN=https://<host>
sudo install -m 0644 deploy/Caddyfile /etc/caddy/Caddyfile   # then edit it
```

Open the firewall and start everything:

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80,443/tcp           # Caddy (Let's Encrypt needs 80)
sudo ufw allow 9090/tcp             # the TUI; or: from <your IP> to any port 9090 proto tcp
sudo ufw enable

sudo systemctl enable --now nyttigd nyttigd-backup.timer nyttig-api nyttig-web
sudo systemctl reload caddy
journalctl -u nyttigd -o cat        # expect "database opened" with sqlite_version,
                                    # and "mutual TLS enabled" with addr :9090
```

Also harden SSH (`PasswordAuthentication no`, `PermitRootLogin no`) and
install `unattended-upgrades`.

## 4. Connect the TUI

```bash
nyttig -socket nyttig.example.com:9090 \
  -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem
```

The CLI subcommands (`add-source`, `add-tag-rule`, …) take the same flags.

## SQLite

The release binaries do not carry their own SQLite. They are built with
`-tags libsqlite3` and use the server's `libsqlite3-0` package (3.46.1 on
Debian 13), so SQLite security fixes arrive through `apt` and
`unattended-upgrades` like glibc's do, without a new nyttig release. Two
things follow from that:

- A library upgrade only takes effect in a running `nyttigd` after
  `sudo systemctl restart nyttigd`. `unattended-upgrades` does not restart
  it; install `needrestart` if you want that automated.
- The library must have the FTS5 extension. Debian's does. `nyttigd` checks
  at startup and refuses to start with a message naming the problem rather
  than failing in the first migration.

The `database opened` line in the journal shows the version in use as
`sqlite_version`. A development build (`-tags sqlite_fts5`, see the main
README) compiles go-sqlite3's bundled SQLite into the binary instead.

## Clef assessor

`nyttig-clef`, the optional assessor that scores items with Cloudflare's Clef
models (see [Clef assessor](../README.md#clef-assessor) in the README), is
deployed as a container, not as a systemd unit. Its image and a compose
overlay are in the Docker guide: [`docker/README.md`](docker/README.md#the-clef-assessor-opt-in).
The release bundle's `bin/nyttig-clef` is the same program for running it by
hand against this server's socket (`--config` with `deploy/clef.sample.toml`
as the starting point); a systemd unit for it is a follow-up.

## Upgrading

`nyttigd` runs schema migrations on start, and a failed migration needs manual
repair. Always take a backup first:

```bash
scp "nyttig-$VERSION-linux-amd64.tar.gz" vps:
ssh vps
tar -xzf nyttig-v0.2.0-linux-amd64.tar.gz && cd nyttig-v0.2.0-linux-amd64
sudo systemctl start nyttigd-backup
sudo install -m 0755 bin/nyttigd bin/nyttig bin/nyttig-api /usr/local/bin/
sudo rm -rf /opt/nyttig-web/build
sudo cp -r web/build web/package.json /opt/nyttig-web/
sudo systemctl restart nyttigd nyttig-api nyttig-web
nyttigd -version                    # the release tag; also in the startup logs
```

Check the release notes for changes to the units or the Caddyfile, and diff
the bundle's `deploy/` against the installed files.
`nyttigd` does not handle `SIGHUP`, so use `systemctl restart` (not `reload`)
to apply config changes.

## Backups and restore

`nyttigd-backup.timer` writes `/var/lib/nyttig/backups/nyttig-YYYY-MM-DD.db`
daily and keeps 14 days. These copies are on the same disk, so copy them
somewhere else too (e.g. `restic`, or `rsync` to your workstation).

To restore:

```bash
sudo systemctl stop nyttig-api nyttigd
sudo rm -f /var/lib/nyttig/nyttig.db-wal /var/lib/nyttig/nyttig.db-shm
sudo install -o nyttig -g nyttig -m 0600 nyttig-YYYY-MM-DD.db /var/lib/nyttig/nyttig.db
sudo systemctl start nyttigd nyttig-api
```
