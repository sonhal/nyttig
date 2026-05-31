# Nyttig

> Warning: This project is generated using LLMs


Nyttig is a news aggregator with a developer-oriented terminal UI. Subscribe to RSS/Atom feeds, apply regex-based tags, full-text search, and browse your collected news stream in a compact, keyboard-driven interface inspired by [K9s](https://k9scli.io/) and Kibana.

## Architecture

Nyttig follows the Docker model — two separate binaries communicating over gRPC:

```
nyttigd          daemon (server) — fetches feeds, runs in the background
nyttig           client (TUI/CLI) — connects to the daemon over a Unix socket
```

- **Daemon**: Periodically fetches RSS/Atom feeds, applies tagging rules, stores items in SQLite (with FTS5 full-text search).
- **Client**: Bubble Tea TUI with a filter bar, scrollable news table with tag chips, and K9s-style view tracking. Also supports CLI subcommands for headless management (`add-source`, `list-sources`, etc.).
- **Protocol**: gRPC with bidirectional streaming — the daemon pushes new items to the TUI in real time as they are fetched.

## Install

```bash
go install github.com/sonhal/nyttig/cmd/nyttigd@latest
go install github.com/sonhal/nyttig/cmd/nyttig@latest
```

Requires Go 1.26+.

## Quick Start

### 1. Create a config file

```bash
mkdir -p ~/.config/nyttig
```

Create `~/.config/nyttig/config.toml`:

```toml
[server]
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
nyttigd
```

Or with explicit options:

```bash
nyttigd --socket /tmp/nyttig.sock --config ~/.config/nyttig/config.toml --log-level debug
```

### 3. Open the TUI

```bash
nyttig
```

The TUI connects to the daemon via the default Unix socket (`/tmp/nyttig.sock`). To connect to a remote daemon over TCP+TLS:

```bash
nyttig --socket daemon.example.com:8443 --tls --tls-ca /path/to/ca.pem
```

### 4. Manage sources from the CLI (no TUI needed)

```bash
nyttig add-source -n "Rust Blog" -u "https://blog.rust-lang.org/feed.xml"
nyttig list-sources
nyttig remove-source -id 3
nyttig add-tag -n "security" -c "#FF0000"
nyttig refresh
```

## Configuration Reference

### `[server]`

| Field       | Default                           | Description                        |
|-------------|-----------------------------------|------------------------------------|
| `socket`    | `/tmp/nyttig.sock`                | Unix socket path or `host:port`    |
| `db_path`   | `~/.local/share/nyttig/nyttig.db` | SQLite database path               |
| `log_level` | `info`                            | `debug`, `info`, `warn`, `error`   |

### `[[sources]]`

| Field         | Required | Default | Description                                      |
|---------------|----------|---------|--------------------------------------------------|
| `name`        | yes      | —       | Display name for the feed                        |
| `url`         | yes      | —       | Feed URL (RSS or Atom)                           |
| `type`        | no       | `rss`   | Feed type: `rss` or `atom`                       |
| `refresh_sec` | no       | `3600`  | Fetch interval in seconds                        |
| `enabled`     | no       | `true`  | Whether the source is enabled on startup         |

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

| Flag         | Default                              | Description                          |
|--------------|--------------------------------------|--------------------------------------|
| `--socket`   | `/tmp/nyttig.sock`                   | Unix socket path (or `host:port`)    |
| `--config`   | `~/.config/nyttig/config.toml`       | Config file path                     |
| `--db-path`  | config value or default              | Override database path               |
| `--log-level`| `info`                               | `debug`, `info`, `warn`, `error`     |

## Client Flags (`nyttig`)

| Flag       | Default              | Description                              |
|------------|----------------------|------------------------------------------|
| `--socket` | `/tmp/nyttig.sock`   | Daemon socket to connect to              |
| `--tls`    | `false`              | Enable TLS for remote connections        |
| `--tls-ca` | —                    | CA certificate file for TLS verification |

## TUI Keybindings

| Key          | Action                                                   |
|--------------|----------------------------------------------------------|
| `/`          | Focus search bar. Type to filter with FTS5 search.       |
| `Esc`        | Clear search and defocus search bar.                     |
| `s`          | Cycle source filter (all → specific source → all).       |
| `t`          | Cycle tag filter (all → specific tag → all).             |
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
nyttig add-source    -n <name> -u <url> [-t rss|atom] [-i refresh_sec]
nyttig list-sources
nyttig remove-source -id <source_id>
nyttig add-tag       -n <name> [-c <hex_color>]
nyttig refresh       [-id <source_id>]   # omit -id to refresh all
```

Each subcommand calls the corresponding gRPC RPC against the daemon. The daemon must be running for these to work.

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

## Running as a Systemd Service

An example user-level systemd service is provided in `nyttigd.service`. To use it:

```bash
mkdir -p ~/.config/systemd/user
cp nyttigd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now nyttigd
systemctl --user status nyttigd
```

The service runs under your user account and uses the default socket path at `/tmp/nyttig.sock`. Adjust the `ExecStart` path if you installed the binary to a different location.

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
| Server API    | gRPC with bidirectional streaming   |
| Wire format   | Protocol Buffers (proto3)           |
| Database      | SQLite with FTS5                    |
| Migrations    | Embedded SQL files                  |
| Configuration | TOML                                |
| Feed parsing  | gofeed (RSS 2.0 / Atom)            |
| Logging       | `slog` with JSON output             |

## License

MIT
