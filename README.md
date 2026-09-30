# Nyttig

> Warning: This project is generated using LLMs


Nyttig is a news aggregator with a developer-oriented terminal UI. Subscribe to RSS/Atom feeds, apply regex-based tags, full-text search, and browse your collected news stream in a compact, keyboard-driven interface inspired by [K9s](https://k9scli.io/) and Kibana.

## Architecture

Nyttig follows the Docker model — two separate binaries communicating over gRPC:

```
nyttigd          daemon (server) — fetches feeds, runs in the background
nyttig           client (TUI/CLI) — connects to the daemon over a Unix socket
nyttig-api       web client API — JSON + SSE for the browser app, a gRPC client like the TUI
nyttig-web       web client app — the SvelteKit app in web/, served by Node
```

- **Daemon**: Periodically fetches RSS/Atom feeds, applies tagging rules, stores items in SQLite (with FTS5 full-text search).
- **Client**: Bubble Tea TUI with a filter bar, scrollable news table with tag chips, and K9s-style view tracking. Also supports CLI subcommands for headless management (`add-source`, `list-sources`, etc.).
- **Protocol**: gRPC with bidirectional streaming — the daemon pushes new items to the TUI in real time as they are fetched.
- **Web client**: a SvelteKit app (`web/`) with the same feed view in a browser (desktop and phone), backed by the `nyttig-api` service, see [Web Client](#web-client).

## Install

```bash
git clone https://github.com/sonhal/nyttig.git
cd nyttig
go install ./cmd/nyttigd ./cmd/nyttig
```

Requires Go 1.26+ and a C compiler (cgo), since SQLite is compiled in.
The web client's app also needs Node.js 22+ (and pnpm to build it); see
[Web Client](#web-client).

`go install github.com/sonhal/nyttig/cmd/...@latest` does not work: the module
uses a `replace` directive for its vendored SQLite driver, which Go refuses for
remote installs. FTS5 is enabled in that driver by default, so no build tags are
needed.

## Quick Start

### 1. Create a config file

```bash
mkdir -p ~/.config/nyttig
```

Create `~/.config/nyttig/config.toml`:

```toml
socket = "/tmp/nyttig.sock"
db_path = "~/.local/share/nyttig/nyttig.db"
log_level = "info"

[[sources]]
name = "Hacker News"
url = "https://news.ycombinator.com/rss"
refresh_sec = 600
type = "rss"

[[sources]]
name = "Lobsters"
url = "https://lobste.rs/rss"
refresh_sec = 1800
type = "rss"

[[sources]]
name = "Go Blog"
url = "https://go.dev/blog/feed.atom"
refresh_sec = 3600
type = "atom"

[[tags]]
name = "rust"
color = "#FF6B35"

[[tags]]
name = "go"
color = "#00ADD8"

[[tags]]
name = "database"
color = "#336791"

[[tags]]
name = "linux"
color = "#FCC624"

[[tag_rules]]
tag = "rust"
pattern = "(?i)\\brust\\b"
field = "both"

[[tag_rules]]
tag = "go"
pattern = "(?i)\\bgolang?\\b"
field = "both"

[[tag_rules]]
tag = "database"
pattern = "(?i)(sqlite|postgres|mysql|mariadb|database|db|nosql)"
field = "both"

[[tag_rules]]
tag = "linux"
pattern = "(?i)\\blinux\\b"
field = "both"
```

### 2. Start the daemon

```bash
nyttigd --config ~/.config/nyttig/config.toml
```

`nyttigd` only reads a config file when `--config` is given; without it, the
daemon starts with the built-in defaults and no seeded sources, tags, or rules.
Flags override the file's values:

```bash
nyttigd --config ~/.config/nyttig/config.toml --log-level debug
```

### 3. Open the TUI

```bash
nyttig
```

The TUI connects to the daemon via the default Unix socket (`/tmp/nyttig.sock`). To connect to a remote daemon over TCP, use mutual TLS (see [Remote Access with Mutual TLS](#remote-access-with-mutual-tls)):

```bash
nyttig --socket daemon.example.com:9090 \
  --tls-cert client.pem --tls-key client.key --tls-ca ca.pem
```

### 4. Manage sources from the CLI (no TUI needed)

```bash
nyttig add-source -n "Rust Blog" -u "https://blog.rust-lang.org/feed.xml"
nyttig list-sources
nyttig remove-source -id 3
nyttig add-tag -n "security" -c "#FF0000"
nyttig add-tag-rule -tag security -p '(?i)\b(cve|exploit)\b'
nyttig search -tag security openssl
nyttig refresh
```

## Configuration Reference

The file is only read when `nyttigd` is started with `--config`. Unknown keys
are rejected, so a typo stops the daemon with an error rather than being
silently ignored.

### Top-level settings

These go at the top of the file, before any `[table]`.

| Field       | Default                           | Description                        |
|-------------|-----------------------------------|------------------------------------|
| `socket`    | `/tmp/nyttig.sock`                | Unix socket path or `host:port`    |
| `db_path`   | `~/.local/share/nyttig/nyttig.db` | SQLite database path               |
| `log_level` | `info`                            | `debug`, `info`, `warn`, `error`   |
| `block_private_addresses` | `false`             | Refuse to fetch feeds from loopback, private or link-local addresses (see [Fetching and SSRF](#fetching-and-ssrf)) |

### `[tls]`

Optional. Enables [mutual TLS](#remote-access-with-mutual-tls) on the daemon's
listener. Omit the whole table to serve in plaintext (fine for a local Unix
socket). Paths may use a leading `~`.

Without `listen`, TLS applies to `socket`. With `listen`, the daemon serves
mTLS on that TCP address and keeps serving `socket` in plaintext for local
clients such as nyttig-api; `socket` must then be a Unix socket path.

| Field        | Required | Description                                          |
|--------------|----------|------------------------------------------------------|
| `cert`       | yes      | Server certificate (PEM) the daemon presents         |
| `key`        | yes      | Server private key (PEM)                             |
| `client_ca`  | yes      | CA bundle (PEM) used to verify client certificates   |
| `listen`     | no       | Extra TCP address for mTLS, e.g. `:9090`              |

### `[[sources]]`

| Field         | Required | Default | Description                                      |
|---------------|----------|---------|--------------------------------------------------|
| `name`        | yes      | —       | Display name for the feed                        |
| `url`         | yes      | —       | Feed URL (RSS or Atom)                           |
| `type`        | no       | `rss`   | Feed type: `rss` or `atom`                       |
| `refresh_sec` | no       | `3600`  | Fetch interval in seconds                        |
| `color`       | no       | —       | Hex color for the source chip in the TUI         |
| `abbreviation`| no       | —       | Short display name in the TUI (falls back to `name`) |

### `[[tags]]`

| Field   | Required | Default | Description                               |
|---------|----------|---------|-------------------------------------------|
| `name`  | yes      | —       | Tag name (unique)                         |
| `color` | no       | —       | Hex color for TUI chip, e.g. `"#FF6B35"`  |

### `[[tag_rules]]`

| Field      | Required | Default | Description                                              |
|------------|----------|---------|----------------------------------------------------------|
| `tag`      | yes      | —       | Tag name to assign (must match a `[[tags]]` entry)       |
| `pattern`  | yes      | —       | Regex pattern (Go syntax)                                |
| `field`    | no       | `both`  | Field to match: `title`, `description`, or `both`        |
| `source`   | no       | all     | Source name to restrict rule to (omit for global rule)   |
| `priority` | no       | `0`     | Evaluation order (lower = evaluated first)               |

## Daemon Flags (`nyttigd`)

| Flag              | Default                        | Description                                       |
|-------------------|--------------------------------|---------------------------------------------------|
| `--socket`        | `/tmp/nyttig.sock`             | Unix socket path (or `host:port`)                 |
| `--config`        | — (no config file is read)     | Config file path                                  |
| `--db-path`       | config value or default        | Override database path                            |
| `--log-level`     | `info`                         | `debug`, `info`, `warn`, `error`                  |
| `--tls-cert`      | —                              | Server certificate (PEM); enables mTLS            |
| `--tls-key`       | —                              | Server private key (PEM)                          |
| `--tls-client-ca` | —                              | CA bundle (PEM) used to verify client certs       |
| `--tls-listen`    | —                              | Extra TCP address served with mTLS; `--socket` stays a plaintext Unix socket |
| `--block-private-addresses` | `false`              | Refuse to fetch feeds from non-public addresses   |

Flags override the corresponding config values. All three TLS flags are
required together.

## Client Flags (`nyttig`)

These are global flags accepted by every subcommand and the TUI.

| Flag                | Default            | Description                                          |
|---------------------|--------------------|------------------------------------------------------|
| `--socket`          | `/tmp/nyttig.sock` | Daemon Unix socket path or `host:port`               |
| `--tls-cert`        | —                  | Client certificate (PEM); enables mTLS               |
| `--tls-key`         | —                  | Client private key (PEM)                             |
| `--tls-ca`          | —                  | CA bundle (PEM) used to verify the daemon            |
| `--tls-server-name` | —                  | Override the name verified against the daemon's cert |

## TUI Keybindings

| Key          | Action                                                   |
|--------------|----------------------------------------------------------|
| `/`          | Focus search bar. Type to filter with FTS5 search.       |
| `Esc`        | Clear search and defocus search bar.                     |
| `s`          | Cycle source filter (all → specific source → all).       |
| `t`          | Cycle tag filter (all → specific tag → all).             |
| `o`          | Toggle sort order (newest ↔ oldest).                     |
| `r`          | Force refresh all sources immediately.                   |
| `Enter`      | Open selected item's link in default browser.            |
| `j` / `↓`    | Move selection down.                                     |
| `k` / `↑`    | Move selection up.                                       |
| `g` / `Home` | Jump to top of list.                                     |
| `G` / `End`  | Jump to bottom of list.                                  |
| `Ctrl+d`     | Page down (half screen).                                 |
| `Ctrl+u`     | Page up (half screen).                                   |
| `q` / `Ctrl+c`| Quit the TUI.                                           |

### View tracking

Items are automatically marked as viewed when you scroll past them in the TUI (K9s-style). A `●` prefix indicates an unviewed item; viewed items have no prefix. View state is debounced and sent to the daemon in ~3 second batches.

## TUI Layout

```
┌─ Filter Bar ───────────────────────────────────────────────────────────────────┐
│ / search...   src: [all ▼]   tag: [all ▼]   sort: [newest ▼]   [12 sources]    │
├─ News Table ───────────────────────────────────────────────────────────────────┤
│ ●  10:32 [rust]       Announcing Rust 1.85.0                   blog.rust-lang.. │
│ ●  10:15 [go]         Go 1.24 Release Notes                    go.dev/blog       │
│     09:45 [db] [oss]  SQLite 3.47.0 Released                   sqlite.org        │
│ ●  09:30 [linux]      The Linux Kernel Mailing List Anno..     lkml.org          │
│     08:12 [rust]      This Week in Rust #543                   this-week-in-rust │
│     08:00 [ai]        Paper: Attention Is All You Need          arxiv.org         │
├─ Status Bar ────────────────────────────────────────────────────────────────────┤
│ 🟢 connected   Unviewed: 3   Last fetch: 2m ago   Next: 8m       nyttig 0.1.0   │
└──────────────────────────────────────────────────────────────────────────────────┘
```

## CLI Subcommands

When invoked with arguments, `nyttig` acts as a CLI management tool:

```
nyttig add-source      -n <name> -u <url> [-t rss|atom] [-r refresh_sec] [-color <hex>] [-abbreviation <short>]
nyttig list-sources
nyttig update-source   -id <source_id> [-n <name>] [-u <url>] [-t rss|atom] [-r refresh_sec]
                       [-enable|-disable] [-color <hex>] [-abbreviation <short>]
nyttig remove-source   -id <source_id>
nyttig add-tag         -n <name> [-c <hex_color>]
nyttig list-tags
nyttig update-tag      -id <tag_id> [-n <name>] [-c <hex_color>]   # keeps rules and item assignments
nyttig remove-tag      -id <tag_id>      # also removes the tag's rules and item assignments
nyttig add-tag-rule    -tag <name|id> -p <regex> [-f title|description|both] [-s source_id] [-priority N]
nyttig test-tag-rule   -p <regex> [-f title|description|both] [-s source_id] [-l limit]   # dry run
nyttig list-tag-rules
nyttig remove-tag-rule -id <rule_id>
nyttig search          [-tag <name|id>] [-s source_id] [-l limit] [-offset N] [-sort newest|oldest] [-unviewed] [query...]
nyttig refresh         [-id <source_id>]   # omit -id to refresh all
```

Each subcommand calls the corresponding gRPC RPC against the daemon. The daemon must be running for these to work.

`update-source` and `update-tag` change only the flags you pass. Pass
`-color ''` or `-abbreviation ''` to clear a value. Changing a source's URL or
refresh interval takes effect immediately and triggers a fetch.

Tag rules added with `add-tag-rule` apply to items fetched after the rule is
created; existing items are not retagged. Use `test-tag-rule` first to see
which of the 500 most recent items a pattern would match.

### Input validation

The daemon validates everything clients send, so the CLI, the TUI and other
clients get the same checks:

| Value           | Rule                                                        |
|-----------------|-------------------------------------------------------------|
| Source URL      | Absolute `http`/`https` URL with a host, no credentials, at most 2048 bytes |
| Source type     | `rss` or `atom`                                             |
| Refresh interval| 60 seconds to 7 days (default 3600)                         |
| Colors          | `#RRGGBB`, or empty for none                                |
| Names           | Non-empty, no control characters; sources ≤ 200, tags ≤ 64, abbreviations ≤ 16 characters |
| Tag rule pattern| Valid Go (RE2) regex, at most 1024 bytes; `field` is `title`, `description` or `both` |

A duplicate source URL or tag name is rejected with `AlreadyExists`. Sources,
tags and rules from the config file are seeded directly and are not checked.

## Fetching and SSRF

Every source URL is fetched by the daemon. If people you don't fully trust can
add sources, for example through a web client exposed to the internet, a
source URL could point at services that are only reachable from the server:
`http://127.0.0.1:…`, your LAN, or a cloud metadata endpoint such as
`169.254.169.254`.

Set `block_private_addresses = true` (or pass `--block-private-addresses`) to
refuse those. The daemon then refuses to connect to:

- loopback, private (RFC 1918, `fc00::/7`) and link-local addresses;
- carrier-grade NAT (`100.64.0.0/10`, which Tailscale also uses), multicast,
  and reserved and documentation ranges;
- IPv4-mapped and NAT64 forms of the above.

The check runs on the resolved IP when the connection is made, so it also
covers hostnames that resolve to internal addresses, DNS rebinding and
redirects. A refused fetch shows up as the source's `fetch_error`. HTTP proxy
environment variables are ignored while the option is on, because through a
proxy the real destination can't be checked.

## Tagging System

Tags are applied automatically via regex rules evaluated when items are fetched:

- **Global rules** (`source` omitted or `source_id = 0`): Apply to items from all sources.
- **Per-source rules** (`source` set): Only apply to items from that specific feed.
- **Evaluation order**: Rules are evaluated in `priority` order (lower number first).
- **Multiple tags**: An item can match multiple rules and receive multiple tags.
- **Field scoping**: Rules can match against `title`, `description`, or `both`.

Tags are purely rule-based in v1. No manual tagging UI.

## Deduplication

Items are deduplicated by GUID using `UNIQUE(source_id, guid)`:

- If the feed entry has a non-empty `<guid>` element, that value is used directly.
- If no GUID is present, the SHA-256 hash of the entry's `<link>` is used instead.

This means an item will only appear once per source in the database.

## Remote Access with Mutual TLS

By default the daemon listens on a local Unix socket in plaintext. To run
`nyttigd` on a server and connect from another machine, expose it on a TCP port
protected by **mutual TLS (mTLS)**: the daemon proves its identity to the
client *and* the client proves its identity to the daemon, so only holders of a
certificate signed by your CA can connect — no extra password layer needed.

### 1. Generate certificates

A helper script issues a private CA plus a server and a client certificate.
Pass the hostname (or IP) clients will dial:

```bash
./scripts/gen-certs.sh ./certs nyttig.example.com
# → certs/ca.pem, certs/server.pem, certs/server.key,
#   certs/client.pem, certs/client.key
```

Keep the `*.key` files private. The server needs `ca.pem`, `server.pem`,
`server.key`; each client needs `ca.pem`, `client.pem`, `client.key`.

### 2. Run the daemon with TLS

Via flags:

```bash
nyttigd -socket :9090 \
  -tls-cert certs/server.pem -tls-key certs/server.key -tls-client-ca certs/ca.pem
```

…or via the config file (see [`[tls]`](#tls)):

```toml
socket = ":9090"

[tls]
cert = "~/.config/nyttig/certs/server.pem"
key = "~/.config/nyttig/certs/server.key"
client_ca = "~/.config/nyttig/certs/ca.pem"
```

The daemon logs `mutual TLS enabled` on startup; it logs a warning if no TLS is
configured.

To keep a local Unix socket as well, for example for nyttig-api on the same
server, set `listen` instead of making `socket` a TCP address:

```toml
socket = "/tmp/nyttig.sock"

[tls]
cert = "~/.config/nyttig/certs/server.pem"
key = "~/.config/nyttig/certs/server.key"
client_ca = "~/.config/nyttig/certs/ca.pem"
listen = ":9090"
```

### 3. Connect the client

```bash
nyttig list-sources -socket nyttig.example.com:9090 \
  -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem

# Launch the TUI against the remote daemon:
nyttig -socket nyttig.example.com:9090 \
  -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem
```

Use `-tls-server-name` if you dial by an address that doesn't match the
server certificate's name (e.g. connecting by raw IP).

Connections without a valid client certificate, or whose certificate is signed
by a different CA, are rejected at the TLS handshake.

## Running as a Systemd Service

[`deploy/systemd/nyttigd.service`](deploy/systemd/nyttigd.service) is a
hardened **system** unit that runs the daemon as a dedicated `nyttig` user.
systemd creates `/var/lib/nyttig` for the database, `/run/nyttig` for the
socket and `/etc/nyttig` for the config.

```bash
sudo useradd --system --home-dir /var/lib/nyttig --shell /usr/sbin/nologin nyttig
sudo install -m 0755 nyttigd nyttig /usr/local/bin/
sudo install -D -o root -g nyttig -m 0640 sample_config.toml /etc/nyttig/config.toml
sudo install -m 0644 deploy/systemd/nyttigd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now nyttigd
```

The socket is `/run/nyttig/nyttig.sock`, readable and writable by the `nyttig`
group only. To use the TUI or CLI, add yourself to the group, log in again,
and pass the socket:

```bash
sudo usermod -aG nyttig "$USER"
nyttig --socket /run/nyttig/nyttig.sock
```

Use absolute paths in `/etc/nyttig/config.toml`: `~` expands to the service
user's home, `/var/lib/nyttig`. The unit's `--socket` and `--db-path` flags
override the file. Logs go to the journal: `sudo journalctl -u nyttigd`.

The daemon has no reload action. Restart it (`sudo systemctl restart nyttigd`)
to pick up config changes.

## Web Client

The web client is the same feed view as the TUI in a browser: the filter
bar, the table with the same columns and colors, live pushes, and view
tracking. It works with the keyboard on a desktop and with touch on a
phone. It has two parts behind one reverse proxy:

- **The app, `nyttig-web`** (`web/`): a SvelteKit app, built with `vite build` and served
  by its own Node server (`@sveltejs/adapter-node`, run as `node build`).
- **`nyttig-api`**: a Go binary that serves the app's `/api/*` (JSON and
  Server-Sent Events). Like the TUI it is a gRPC client of `nyttigd`, so
  the process that holds the database and parses feeds is never exposed
  to HTTP.

```
browser ── https, basic auth ──▶ Caddy ─┬─ /api/* ──▶ nyttig-api (127.0.0.1:7070) ── unix socket ──▶ nyttigd
                                        └─ /*     ──▶ node build (127.0.0.1:7071)
```

### Build

```bash
go build ./cmd/nyttig-api
pnpm --dir web install --frozen-lockfile
pnpm --dir web build            # writes web/build/, a standalone Node server
```

### Run locally

For development, the Vite dev server serves the app and proxies `/api` to
nyttig-api:

```bash
nyttig-api --socket /tmp/nyttig.sock --origin http://localhost:5173
pnpm --dir web dev              # then open http://localhost:5173
```

In production both run behind Caddy, which routes by path; see
[Deploying behind Caddy](#deploying-behind-caddy). The app server takes
the standard adapter-node environment: `HOST`, `PORT` and `ORIGIN` (the
URL the browser uses).

nyttig-api's flags:

| Flag                    | Default            | Description                                               |
|-------------------------|--------------------|-----------------------------------------------------------|
| `--origin`              | (required)         | The URL the browser uses, e.g. `https://nyttig.example.com`. Requests with another `Host`, and mutations from another origin, are refused |
| `--listen`              | `127.0.0.1:7070`   | HTTP listen address. Must be loopback                      |
| `--allow-public-listen` | `false`            | Allow a non-loopback `--listen`. nyttig-api has no authentication of its own |
| `--socket`              | `/tmp/nyttig.sock` | Daemon Unix socket path or TCP address                     |
| `--tls-cert`, `--tls-key`, `--tls-ca`, `--tls-server-name` | | mTLS to a remote daemon, as for `nyttig` |
| `--log-level`           | `info`             | `debug`, `info`, `warn` or `error`                        |

nyttig-api starts even when the daemon is down; the status bar then shows
"disconnected" and the page reconnects on its own.

### Using it

The filter (search, source, tag, sort, unviewed) is kept in the URL, so a
view can be bookmarked and the back button works.

| Key                | Action                                            |
|--------------------|---------------------------------------------------|
| `j`/`↓`, `k`/`↑`   | Move down / up                                     |
| `g`/`Home`, `G`/`End` | Top / bottom                                    |
| `d`, `u`           | Half page down / up (`Ctrl+d`/`Ctrl+u` where the browser allows them) |
| `/`                | Focus the search; `Enter` applies it, `Esc` clears it |
| `s`, `t`           | Cycle source / tag                                |
| `o`                | Toggle sort (newest / oldest)                     |
| `r`, `R`           | Refresh all sources / the selected item's source  |
| `Enter`            | Open the link in a new tab                         |
| `Space`, `l`       | Expand / collapse the row (details and full description) |
| `q`, `Esc`         | Collapse the row                                   |

On a phone (narrower than 720px) rows take two lines, a tap selects and
expands a row (with an explicit "open ↗" link), `⚙` opens the filters and
`⟳` refreshes.

Rows are marked viewed as they scroll into view, like in the TUI, and sent
to the daemon about every 3 seconds; what is pending when you close the tab
is sent with a beacon.

### Security

nyttig-api is meant to be reachable from the internet only through a
reverse proxy that does TLS and authentication:

- It listens on loopback and refuses anything else without
  `--allow-public-listen`. Local processes can reach it without the
  proxy's password, which is acceptable on a single-user server.
- Requests must carry the `Host` of `--origin` (against DNS rebinding).
  Mutations (`POST`) must be `Content-Type: application/json` and come from
  `--origin` (`Origin` header) or be `Sec-Fetch-Site: same-origin`, since
  basic-auth credentials are sent automatically, like cookies.
- The app's pages have a strict `Content-Security-Policy` from SvelteKit's
  `kit.csp` (no inline scripts except SvelteKit's bootstrap, allowed by a
  per-request nonce), plus `nosniff`, `Referrer-Policy: no-referrer` and
  `Cross-Origin-Opener-Policy` from `web/src/hooks.server.ts`. API
  responses get the same headers and a `default-src 'none'` policy.
- Feed content is untrusted: it is only rendered as text, links must be
  `http(s)` (checked in nyttig-api and in the page) and open with
  `noopener,noreferrer`, colors must be `#RRGGBB`, and no feed images are
  loaded.
- Turn on `block_private_addresses` in the daemon's config (see
  [Fetching and SSRF](#fetching-and-ssrf)).

### Deploying behind Caddy

[`deploy/Caddyfile`](deploy/Caddyfile) is an example site with Let's
Encrypt and basic auth that sends `/api/*` to nyttig-api (with response
buffering off for the event stream) and everything else to the app.

Both run as throwaway dynamic users with no outbound network:
[`deploy/systemd/nyttig-api.service`](deploy/systemd/nyttig-api.service)
can reach only the daemon's socket (through the `nyttig` group), and
[`deploy/systemd/nyttig-web.service`](deploy/systemd/nyttig-web.service)
runs the app with the server's Node.js (22 or newer). Set up `nyttigd`
first (see [above](#running-as-a-systemd-service)), then:

```bash
sudo install -m 0755 nyttig-api /usr/local/bin/
sudo install -d /opt/nyttig-web
sudo cp -r web/build web/package.json /opt/nyttig-web/
sudo install -m 0644 deploy/systemd/nyttig-api.service deploy/systemd/nyttig-web.service \
  /etc/systemd/system/
sudo systemctl edit nyttig-api     # override ExecStart with your --origin
sudo systemctl edit nyttig-web      # Environment=ORIGIN=<the same URL>
sudo systemctl daemon-reload
sudo systemctl enable --now nyttig-api nyttig-web
```

The app's build bundles all its dependencies, so it needs no
`node_modules` on the server.

Then install the Caddyfile (usually `/etc/caddy/Caddyfile`) with your host
name, user and `caddy hash-password` hash, and reload Caddy.

For a complete VPS setup (the web client behind Caddy, the TUI over mTLS on
port 9090, backups and upgrades), see [`deploy/README.md`](deploy/README.md).

## Database

Nyttig uses SQLite with FTS5 for full-text search. The default database path is `~/.local/share/nyttig/nyttig.db`. The database is created and migrated automatically on first daemon start.

### Schema

| Table         | Purpose                                           |
|---------------|---------------------------------------------------|
| `sources`     | Feed subscriptions (URL, refresh interval, etc.)  |
| `items`       | Fetched news items (title, link, description)     |
| `tags`        | User-defined tags with optional color             |
| `tag_rules`   | Regex rules for automatic tagging                 |
| `item_tags`   | Many-to-many join between items and tags          |
| `view_state`  | Per-item view tracking (row exists = viewed)      |
| `items_fts`   | FTS5 virtual table for full-text search           |

## Technology Stack

| Layer         | Technology                          |
|---------------|-------------------------------------|
| Language      | Go 1.26+                            |
| TUI           | Bubble Tea + custom Lipgloss table  |
| Web client    | SvelteKit 2 + Svelte 5 on Node (adapter-node); Go API with JSON + Server-Sent Events |
| Server API    | gRPC with bidirectional streaming   |
| Wire format   | Protocol Buffers (proto3)           |
| Database      | SQLite with FTS5                    |
| Migrations    | Embedded SQL files                  |
| Configuration | TOML                                |
| Feed parsing  | `encoding/xml` (RSS 2.0 / Atom)     |
| Logging       | `slog` with JSON output             |

## License

MIT
