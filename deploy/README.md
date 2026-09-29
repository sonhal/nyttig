# Deploying nyttigd to a VPS

Runs `nyttigd` on a dedicated Debian 13 (trixie) server, exposed on TCP port
9090 with mutual TLS. The SQLite database is embedded in the daemon, so there
is no separate database server: it is a file in `/var/lib/nyttigd`.

| File                     | Installed to                                   |
|--------------------------|------------------------------------------------|
| `nyttigd.service`        | `/etc/systemd/system/`                         |
| `nyttigd-backup.service` | `/etc/systemd/system/`                         |
| `nyttigd-backup.timer`   | `/etc/systemd/system/`                         |
| `config.toml`            | `/etc/nyttigd/config.toml`                     |

**Sizing:** 1 vCPU, 1 GB RAM, 20 GB disk is plenty for personal use. Nothing
prunes old items yet, so the database grows by roughly 0.2–2 GB per year
depending on how many feeds you follow.

## 1. Build (on your workstation)

`nyttigd` uses cgo, so it only runs on systems with the same or a newer glibc
than it was built with. Build it in the same Debian release as the server:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-trixie \
  sh -c 'git config --global --add safe.directory /src &&
         go build -trimpath -ldflags="-s -w" -o dist/ ./cmd/nyttigd ./cmd/nyttig'
```

## 2. Certificates (on your workstation)

```bash
scripts/gen-certs.sh ./certs nyttig.example.com   # the name clients will dial
```

The server gets `server.pem`, `server.key` and `ca.pem`. **Never copy `ca.key`
to the server:** anyone who has it can issue client certificates. Keep it
offline, since you need it to issue new certificates. Two limitations to plan for:

- **No revocation.** The daemon trusts any certificate the CA signed. If a
  client key leaks, generate a new CA and re-issue every certificate.
- **Expiry after 825 days.** Set a reminder. Re-running `gen-certs.sh` creates a
  new CA, so every client needs the new files too.

## 3. First install (on the server)

```bash
scp dist/nyttigd deploy/* certs/{server.pem,server.key,ca.pem} vps:
ssh vps
```

Then on the server:

```bash
sudo apt install sqlite3 ufw        # sqlite3 is only used for backups
sudo useradd --system --home-dir /var/lib/nyttigd --shell /usr/sbin/nologin nyttig

sudo install -m 0755 nyttigd /usr/local/bin/
sudo install -d -g nyttig -m 0750 /etc/nyttigd
sudo install -d -m 0700 /etc/nyttigd/tls
sudo install -g nyttig -m 0640 config.toml /etc/nyttigd/
sudo install -m 0644 server.pem ca.pem /etc/nyttigd/tls/
sudo install -m 0600 server.key /etc/nyttigd/tls/
sudo install -m 0644 nyttigd.service nyttigd-backup.service nyttigd-backup.timer /etc/systemd/system/
shred -u server.key

sudo ufw allow OpenSSH
sudo ufw allow 9090/tcp             # or: from <your IP> to any port 9090 proto tcp
sudo ufw enable

sudo systemctl daemon-reload
sudo systemctl enable --now nyttigd nyttigd-backup.timer
journalctl -u nyttigd -o cat        # expect "mutual TLS enabled"
```

Also harden SSH (`PasswordAuthentication no`, `PermitRootLogin no`) and
install `unattended-upgrades`.

## 4. Connect

```bash
nyttig -socket nyttig.example.com:9090 \
  -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem
```

Sources, tags and rules can be managed with the CLI subcommands (`add-source`,
`add-tag-rule`, …) using the same flags, or seeded from `config.toml`. Seeding
only inserts, so removing an entry from the file does not delete it.

## Upgrading

`nyttigd` runs schema migrations on start, and a failed migration needs manual
repair. Always take a backup first:

```bash
scp dist/nyttigd vps:
ssh vps 'sudo systemctl start nyttigd-backup &&
         sudo install -m 0755 nyttigd /usr/local/bin/ &&
         sudo systemctl restart nyttigd'
```

`nyttigd` does not handle `SIGHUP`. Use `systemctl restart`, not `reload`, to
apply config changes.

## Backups and restore

`nyttigd-backup.timer` writes `/var/lib/nyttigd/backups/nyttig-YYYY-MM-DD.db`
daily and keeps 14 days. These copies are on the same disk, so copy them
somewhere else too (e.g. `restic`, or `rsync` to your workstation).

To restore:

```bash
sudo systemctl stop nyttigd
sudo rm -f /var/lib/nyttigd/nyttig.db-wal /var/lib/nyttigd/nyttig.db-shm
sudo install -o nyttig -g nyttig -m 0600 nyttig-YYYY-MM-DD.db /var/lib/nyttigd/nyttig.db
sudo systemctl start nyttigd
```
