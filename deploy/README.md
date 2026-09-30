# Deploying to a VPS

A complete single-server setup on Debian 13 (trixie): the web client behind
Caddy for the browser, and the TUI over mutual TLS on port 9090. The SQLite
database is embedded in `nyttigd`, so there is no separate database server:
it is a file in `/var/lib/nyttig`.

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
more. Build on your workstation, not on the server: compiling SQLite with cgo
can run a 1 GB machine out of memory. Nothing prunes old items yet, so the
database grows by roughly 0.2–2 GB per year depending on how many feeds you
follow.

## 1. Build (on your workstation)

`nyttigd` uses cgo, so it only runs on systems with the same or a newer glibc
than it was built with. Build the Go binaries in the same Debian release as
the server:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-trixie \
  sh -c 'git config --global --add safe.directory /src &&
         go build -trimpath -ldflags="-s -w" -o dist/ ./cmd/nyttigd ./cmd/nyttig ./cmd/nyttig-api'

pnpm --dir web install --frozen-lockfile
pnpm --dir web build        # web/build/ is plain JavaScript; any OS can build it
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

Copy the build and the deploy files over:

```bash
scp -r dist/ web/build web/package.json deploy/ \
  certs/{server.pem,server.key,ca.pem} vps:nyttig-install/
ssh vps
cd nyttig-install
```

Then on the server:

```bash
sudo apt install sqlite3 ufw        # sqlite3 is only used for backups
sudo useradd --system --home-dir /var/lib/nyttig --shell /usr/sbin/nologin nyttig

# Binaries and the web app
sudo install -m 0755 dist/nyttigd dist/nyttig dist/nyttig-api /usr/local/bin/
sudo install -d /opt/nyttig-web
sudo cp -r build package.json /opt/nyttig-web/

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
journalctl -u nyttigd -o cat        # expect "mutual TLS enabled" with addr :9090
```

Also harden SSH (`PasswordAuthentication no`, `PermitRootLogin no`) and
install `unattended-upgrades`.

## 4. Connect the TUI

```bash
nyttig -socket nyttig.example.com:9090 \
  -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem
```

The CLI subcommands (`add-source`, `add-tag-rule`, …) take the same flags.

## Upgrading

`nyttigd` runs schema migrations on start, and a failed migration needs manual
repair. Always take a backup first:

```bash
scp dist/nyttigd dist/nyttig-api vps:
ssh vps 'sudo systemctl start nyttigd-backup &&
         sudo install -m 0755 nyttigd nyttig-api /usr/local/bin/ &&
         sudo systemctl restart nyttigd nyttig-api'
```

For the web app, replace `/opt/nyttig-web/build` and restart `nyttig-web`.
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
