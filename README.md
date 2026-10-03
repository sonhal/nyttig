# Nyttig

> Warning: This project is generated using LLMs


Nyttig is a news aggregator with a developer-oriented terminal UI. Subscribe to RSS/Atom feeds and Bluesky accounts, apply regex-based tags, full-text search, and browse your collected news stream in a compact, keyboard-driven interface inspired by [K9s](https://k9scli.io/) and Kibana.

## Architecture

Nyttig follows the Docker model — two separate binaries communicating over gRPC:

```
nyttigd          daemon (server) — fetches feeds, runs in the background
nyttig           client (TUI/CLI) — connects to the daemon over a Unix socket
nyttig-api       web client API — JSON + SSE for the browser app, a gRPC client like the TUI
nyttig-web       web client app — the SvelteKit app in web/, served by Node
```

- **Daemon**: Periodically fetches RSS/Atom feeds and Bluesky accounts, applies tagging rules, stores items in SQLite (with FTS5 full-text search).
- **Client**: Bubble Tea TUI with a filter bar, scrollable news table with tag chips, and K9s-style view tracking. Also supports CLI subcommands for headless management (`add-source`, `list-sources`, etc.).
- **Protocol**: gRPC with bidirectional streaming — the daemon pushes new items to the TUI in real time as they are fetched.
- **Web client**: a SvelteKit app (`web/`) with the same feed view in a browser (desktop and phone), backed by the `nyttig-api` service, see [Web Client](#web-client).

## Install

```bash
git clone https://github.com/sonhal/nyttig.git
cd nyttig
go install -tags sqlite_fts5 ./cmd/nyttigd ./cmd/nyttig
```

Requires Go 1.26+ and a C compiler, since the SQLite driver uses cgo. The
`sqlite_fts5` build tag is required: it compiles the driver's bundled SQLite
with the FTS5 extension the schema needs. Without it the binary builds, but
the daemon stops at startup with a message saying so. The web client's app
also needs Node.js 22+ (and pnpm to build it); see [Web Client](#web-client).

A remote install works the same way:

```bash
go install -tags sqlite_fts5 github.com/sonhal/nyttig/cmd/...@latest
```

To link the operating system's SQLite instead of the bundled copy, build with
`-tags libsqlite3` (it needs the `libsqlite3-dev` headers). The release
bundles are built that way so the server's package manager keeps SQLite
patched; see [SQLite](deploy/README.md#sqlite) in the deploy guide.

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

# Tags can have parents: "cyber security" also shows CVE and linux security
# items, and "linux" also shows linux security items.
[[tags]]
name = "cyber security"
color = "#D7263D"

[[tags]]
name = "CVE"
parents = ["cyber security"]

[[tags]]
name = "linux security"
parents = ["cyber security", "linux"]

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

[[tag_rules]]
tag = "CVE"
pattern = "(?i)\\bCVE-\\d{4}-\\d{4,}\\b"
field = "both"

[[tag_rules]]
tag = "linux security"
pattern = "(?i)\\blinux\\b.*\\b(vulnerabilit|exploit|privilege escalation)"
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
| `url`         | yes      | —       | Feed URL (RSS or Atom), or for `bluesky` the profile URL |
| `type`        | no       | `rss`   | Feed type: `rss`, `atom` or `bluesky`            |
| `refresh_sec` | no       | `3600`  | Fetch interval in seconds                        |
| `color`       | no       | —       | Hex color for the source chip in the TUI         |
| `abbreviation`| no       | —       | Short display name in the TUI (falls back to `name`) |

#### Bluesky sources

`type = "bluesky"` follows one account's own posts (replies and reposts are
left out; quote posts are kept) through Bluesky's public API, so no login is
needed. The `url` is the account's profile URL, ideally with its DID, which
does not change when the account renames itself:

```toml
[[sources]]
name = "Alice"
url = "https://bsky.app/profile/did:plc:z72i7hdynmk6r22z27h6tvur"
type = "bluesky"
```

`https://bsky.app/profile/alice.bsky.social` also works, but stops working if
the account changes its handle. The config file is read without network
access, so nyttigd does not resolve handles at startup. To find a DID, add the
account once with `nyttig add-source -t bluesky -u alice.bsky.social` and read
the DID from `nyttig list-sources`, or open
`https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=alice.bsky.social`
in a browser and copy the `did` field.

Adding a source with the CLI, the TUI or the web app accepts a handle
(`alice.bsky.social` or `@alice.bsky.social`), a DID, or a profile URL. The
daemon looks the account up once and stores the profile URL with its DID. The
name is optional for Bluesky sources: it defaults to the account's display
name, or `@handle`. Each post becomes an item: the title is the post's first
line, the description holds the full text plus any quoted post, image alt text
and link card.

### `[[tags]]`

| Field   | Required | Default | Description                               |
|---------|----------|---------|-------------------------------------------|
| `name`  | yes      | —       | Tag name (unique)                         |
| `color` | no       | —       | Hex color for TUI chip, e.g. `"#FF6B35"`  |
| `parents` | no     | —       | Names of parent tags (see [Tag tree](#tag-tree)); may be declared later in the file, an unknown name is created |

### `[[assessors]]`

| Field         | Required | Default | Description                                          |
|---------------|----------|---------|------------------------------------------------------|
| `name`        | yes      | —       | Assessor name (unique); an existing one is left as it is |
| `description` | no       | —       | What the score means, shown with the scores          |
| `color`       | no       | —       | Hex color for the score chip, e.g. `"#D97757"`       |

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
| `t`          | Cycle tag filter (all → specific tag → all), parents before their children. A parent's label shows how many tags it covers: `cyber security +2`. |
| `o`          | Cycle the sort order (newest → oldest, and `score` once an assessor is selected). |
| `=`          | Rate the selected item yourself: a prompt takes `<score> [note]` (`Enter` rates, `Esc` cancels), see [Rating items yourself](#rating-items-yourself). |
| `a`          | Cycle the assessor whose scores are shown first and used by `m` and the `score` sort (none → each assessor → none); the bar shows `score: [claude]`. |
| `m`          | With an assessor: cycle the minimum score (none → 0.5 → 0.7 → 0.9 → none). |
| `i`          | Show or hide a line above the status bar with the selected item's assessments (assessor, tag, score, note). |
| `r`          | Force refresh all sources immediately.                   |
| `Enter`      | Open selected item's link in default browser.            |
| `j` / `↓`    | Move selection down.                                     |
| `k` / `↑`    | Move selection up.                                       |
| `g` / `Home` | Jump to top of list.                                     |
| `G` / `End`  | Jump to bottom of list.                                  |
| `Ctrl+d`     | Page down (half screen).                                 |
| `Ctrl+u`     | Page up (half screen).                                   |
| `q` / `Ctrl+c`| Quit the TUI.                                           |

Rows show one score chip per assessor that scored the item, `[claude 0.9]` in
the assessor's color (see [Assessments](#assessments)); new and changed scores
arrive live. An item that starts matching the filter is added in its sort
position, and one that stops matching stays until the next filter change.
Notes and assessor names are untrusted: control characters are stripped and
they are shown on one line. The TUI has no "not assessed by" filter; use
`nyttig search -unassessed-by` or the web app for that.

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
nyttig add-source      -n <name> -u <url> [-t rss|atom|bluesky] [-r refresh_sec] [-color <hex>] [-abbreviation <short>]
nyttig list-sources
nyttig update-source   -id <source_id> [-n <name>] [-u <url>] [-t rss|atom|bluesky] [-r refresh_sec]
                       [-enable|-disable] [-color <hex>] [-abbreviation <short>]
nyttig remove-source   -id <source_id>
nyttig add-tag         -n <name> [-c <hex_color>] [-parent <name|id>]...
nyttig list-tags                         # a tree: child tags are indented under their parents
nyttig update-tag      -id <tag_id> [-n <name>] [-c <hex_color>] [-parent <name|id>]... [-no-parents]   # keeps rules and item assignments
nyttig remove-tag      -id <tag_id>      # also removes the tag's rules and item assignments
nyttig add-tag-rule    -tag <name|id> -p <regex> [-f title|description|both] [-s source_id] [-priority N]
nyttig test-tag-rule   -p <regex> [-f title|description|both] [-s source_id] [-l limit]   # dry run
nyttig list-tag-rules
nyttig remove-tag-rule -id <rule_id>
nyttig list-views                        # name, ★ for favorites, and the filter written like the web query bar
nyttig add-view        -n <name> [-q <text>] [-source <name|id>] [-tag <name|id>] [-unviewed] [-since <window>] [-sort newest|oldest|score] [-favorite]
                       [-assessor <name|id>] [-min-score <0-1>] [-unassessed-by <name|id>]
nyttig update-view     (-id <id> | -n <name>) [-rename <name>] [-q <text>] [-source <name|id> | -no-source]
                       [-tag <name|id> | -no-tag] [-sort newest|oldest|score] [-unviewed[=false]]
                       [-since <window> | -no-since] [-favorite[=false]]
                       [-assessor <name|id> | -no-assessor] [-min-score <0-1> | -no-min-score] [-unassessed-by <name|id>]
nyttig remove-view     (-id <id> | -n <name>)
nyttig reorder-views   <id|name>...      # every view once, in the new order
nyttig list-assessors
nyttig add-assessor    -n <name> [-description <text>] [-c <hex_color>]
nyttig update-assessor (-id <id> | -n <name>) [-rename <name>] [-description <text>] [-c <hex_color>]
nyttig remove-assessor (-id <id> | -n <name>)   # also removes all of its assessments
nyttig assess          <item-id> -assessor <name|id> [-tag <name|id>] [-score <0-1>] [-note <text>]
nyttig unassess        <item-id> -assessor <name|id> [-tag <name|id>]
nyttig rate            <item-id> <score 0-1> [note...]   # you, as the assessor "me" (created on first use)
nyttig search          [-view <name|id>] [-tag <name|id>] [-s source_id] [-l limit] [-offset N] [-sort newest|oldest|score] [-unviewed] [-since <window>] [-exact]
                       [-assessor <name|id>] [-min-score <0-1>] [-unassessed-by <name|id>] [query...]
nyttig refresh         [-id <source_id>]   # omit -id to refresh all
```

Each subcommand calls the corresponding gRPC RPC against the daemon. The daemon must be running for these to work. The `remove-*` subcommands fail with a `NotFound` error (exit status 1) when the ID does not exist.

`update-source` and `update-tag` change only the flags you pass. Pass
`-color ''` or `-abbreviation ''` to clear a value. Changing a source's URL or
refresh interval takes effect immediately and triggers a fetch.

Saved views are named feed filters (search text, one source, one tag with its
child tags, unviewed only, a time window, sort order) stored in the daemon, so every client
shares them. `-favorite` marks a view for the tab bar of clients that have
one. `update-view` changes only the flags you pass; any filter flag replaces
the stored filter's matching part and keeps the rest, and `-no-source` /
`-no-tag` / `-no-since` drop that part. Deleting a source or tag keeps the views that used
it and just stops filtering on it. Names are unique (case-insensitive), at
most 64 characters, and there can be at most 100 views. `search -view NAME`
runs a view's filter headless; flags passed explicitly (`-sort`, `-unviewed`,
`-tag`, `-s`, `-since`, a query) override the view's.

`-since` limits `search` and views to a rolling window counted back from now:
`<n><unit>` with `n` from 1 to 9999 and the unit `h` (hours), `d` (days), `w`
(weeks), `mo` (calendar months) or `y` (calendar years), for example `24h`,
`7d`, `2w`, `1mo`, `1y`. The unit is lower case and `m` is rejected because it
could mean minutes or months. An item's date is its published date, or the
time nyttigd fetched it when the feed gives none. A view stores the window as
you typed it (`30d` stays `30d`), and `search -view NAME -since 1y` replaces
the view's window.

Assessors score items from outside the daemon (see [Assessments](#assessments)).
`assess` stores a score from 0 to 1, a note, or both, for the item as a whole
or, with `-tag`, for one tag; assessing again replaces the earlier assessment
with the same assessor and tag. In `search`, `-assessor` picks whose scores
`-min-score` and `-sort score` use (the sort is only by score when you ask for
it), `-unassessed-by` lists the items an assessor has not assessed yet, and a
SCORES column appears when a listed item has a score. With `-tag`, only the
assessor's scores for that tag (including its child tags) and for the item as
a whole count. A view saves the same fields (`score:claude>=0.7`,
`unassessed:claude` and `sort:score` in `list-views`); deleting the assessor
keeps the view and drops those fields (a `score` sort becomes `newest`).

Tag rules added with `add-tag-rule` apply to items fetched after the rule is
created; existing items are not retagged. Use `test-tag-rule` first to see
which of the 500 most recent items a pattern would match.

### Input validation

The daemon validates everything clients send, so the CLI, the TUI and other
clients get the same checks:

| Value           | Rule                                                        |
|-----------------|-------------------------------------------------------------|
| Source URL      | Absolute `http`/`https` URL with a host, no credentials, at most 2048 bytes; for `bluesky`, a handle, DID or bsky.app profile URL |
| Source type     | `rss`, `atom` or `bluesky`                                  |
| Refresh interval| 60 seconds to 7 days (default 3600)                         |
| Colors          | `#RRGGBB`, or empty for none                                |
| Names           | Non-empty (a `bluesky` source may omit its name), no control characters; sources ≤ 200, tags ≤ 64, abbreviations ≤ 16 characters |
| Assessor        | Name ≤ 64 characters, description ≤ 500 characters without control characters, optional `#RRGGBB` color |
| Score           | A number from 0 to 1 (NaN and infinities are rejected); `min_score` follows the same rule and, like `sort: score`, needs an assessor |
| Note            | Valid UTF-8, at most 4096 bytes; an assessment needs a score or a note |
| Tag rule pattern| Valid Go (RE2) regex, at most 1024 bytes; `field` is `title`, `description` or `both` |

A duplicate source URL, tag name or assessor name is rejected with `AlreadyExists`. Sources,
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

### Tag tree

A tag can have parent tags, several if you like (`add-tag -parent`,
`update-tag -parent ... | -no-parents`, or `parents` in `[[tags]]`). Filtering
by a tag shows items tagged with it **or any tag below it**: with `cyber
security` above `CVE` and `linux security`, `-tag "cyber security"` lists
items tagged with any of the three, and `-tag CVE` only CVE items. `search
-exact` matches the tag alone. Item chips show only the tags a rule assigned.
The daemon rejects a parent that would make a cycle. Deleting a tag keeps its
children; one with no other parent becomes top-level. Config seeding only adds
parents and never removes them, so a parent set in the UI survives a restart.

How it works: the parent edges live in the `tag_parents` table, and a filter
expands the tag to its subtree when it runs (a recursive query), so a change
to the tree applies at once, to old items too. `item_tags` still records only
what a rule matched. The stream filter and `GET /api/items` (`tag=ID`) include
child tags; `tag_exact=1` on `/api/items` and `search -exact` match the tag
alone. In the web app the tags view is a tree, the tag form has a parents
list, and the pickers list tags parents first. Filtering by a parent does not
resend history to a stream that is already open when a child is attached
later; the next filter change or reload does.

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
tracking. It also manages sources, tags, tag rules and saved views. It works with the
keyboard on a desktop and with touch on a phone. It has two parts behind one reverse proxy:

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

The filter (search, source, tag, sort, unviewed, since) is kept in the URL as
separate parameters (`?q=&source=&tag=&sort=&unviewed=1&since=7d`, IDs for
source and tag), so a view can be bookmarked, survives a rename, and the back button
works. `?` lists every key of the current view; the list is generated from
the keymap, so it is always current.

| Key                | Action                                            |
|--------------------|---------------------------------------------------|
| `j`/`↓`, `k`/`↑`   | Move down / up                                     |
| `g`/`Home`, `G`/`End` | Top / bottom                                    |
| `d`, `u`           | Half page down / up (`Ctrl+d`/`Ctrl+u` where the browser allows them) |
| `/`                | Focus the query bar (see below); `Enter` applies it, `Esc` clears the search text |
| `s`, `t`           | Cycle source / tag                                |
| `S`, `T`           | Pick a source / tag by name (fuzzy, `Enter` picks, `Esc` closes) |
| `1`-`9`, `0`       | Open favorite saved view 1-9 / the unfiltered feed (see [Saved views](#saved-views)) |
| `v`                | Pick a saved view by name (fuzzy)                  |
| `=`                | Rate the selected item yourself (types `:rate ` for you, see [Rating items yourself](#rating-items-yourself)) |
| `a`, `A`           | Pick an assessor by name (its scores are shown first, see [Assessments](#assessments)) / clear it |
| `o`                | Toggle sort (newest / oldest)                     |
| `F`                | Follow: jump to the newest and stick to it         |
| `D`                | Relative ("12m ago") or absolute times; remembered in the browser |
| `r`, `R`           | Refresh all sources / the selected item's source  |
| `Enter`            | Open the link in a new tab                         |
| `Space`, `l`       | Expand / collapse the row (details and full description) |
| `q`, `Esc`         | Collapse the row                                   |
| `:`                | Command line (see below)                           |
| `?`                | Help: all keys, the query syntax and the commands  |

**Query bar.** `kernel tag:rust src:"Hacker News" is:unviewed since:7d sort:oldest`:
`tag:` (also matches child tags), `src:` (a name or abbreviation, any case), `is:unviewed`,
`since:<window>` and `sort:newest|oldest|score` set the filter; `score:<assessor>` picks an
assessor whose scores are shown first, `score:claude>=0.7` keeps only items it
scored at least that, `unassessed:<assessor>` keeps items it has not assessed,
and `sort:score` orders by the assessor's scores (it needs a `score:` term).
Every other word is the full-text search. Names with spaces are quoted. Names are completed as you type
(`Tab` accepts, `↑`/`↓` choose; on a phone tap a suggestion), and an
unknown name is an error under the bar instead of being ignored. The bar
always shows the current filter in this syntax. Matching words are
highlighted in titles and descriptions (whole words, any case; FTS5
tokenization is approximated).

**Time window.** `since:7d` shows only items from the last seven days:
`<n><unit>` with `n` from 1 to 9999 and the unit `h`, `d`, `w`, `mo` (calendar
months) or `y` (calendar years), in lower case (`24h`, `7d`, `2w`, `1mo`,
`1y`; `m` is rejected because it could mean minutes or months). The window
rolls with the clock, and an item's date is its published date, or when
nyttigd first fetched it if the feed gives none. It is a filter like the
others: it is in the URL (`since=7d`), a `since:[..]` chip on the desktop
bar cycles any time / 24h / 7d / 30d / 1y, the phone's `⚙` sheet has a
"since" select, and a saved view can hold one (`today` is `since:24h`,
`this week in security` is `tag:security since:7d`). Rows leave the window
only when the list is loaded again (a filter or tab change, a reconnect or a
reload), not while the page is open. An empty list says "no items in the last
7d". The browser turns the window into one absolute cutoff per list load and
sends that as `after=<unix seconds>` to `GET /api/items` and `GET
/api/stream`, so older pages line up with the first.

**Follow.** At the top of a newest-first list, new items flow in and the
view sticks to the newest (`follow` in the status bar). After you scroll
away the view stays put and the status bar counts `↑ N new`; `F` (or a tap
on the count) jumps back and sticks again.

**Load older.** Scrolling near the end of the list fetches the next 100
rows; the list ends with `— end —`. A reconnect replaces the list with a
fresh snapshot (the newest 200), dropping the older pages.

**Command line** (`:`; `Tab` completes, again to cycle; `↑`/`↓` history;
a unique prefix is enough): `:feed` `:sources` `:tags` `:rules` `:views` (`:q` is
the feed), `:assessors`, `:sort [newest|oldest|score]`, `:unviewed [on|off]`, `:src <name>|all`,
`:tag <name>|all`, `:score <assessor>|all [min]`, `:unassessed <assessor>|all`, `:rate <score> [note]`, `:view <name>|all` (`:v`), `:save [name]`,
`:refresh [source]`, `:time [relative|absolute]`, `:follow`, `:help`. The
filter and view commands also work from the management pages, and go to the
feed.

On a phone (narrower than 720px) rows take two lines, a tap selects and
expands a row (with an explicit "open ↗" link), `⚙` opens the filters
(including the time format), `⟳` refreshes and `?` opens the help.

#### Saved views

A saved view is a named filter (search text, one source, one tag with its
child tags, unviewed only, a time window, sort order) that the daemon stores, so the web
app and the CLI (`nyttig list-views`, `add-view`, ...) share them. The
favorite ones are tabs in a row above the filter bar:
`all │ 1 security │ 2 linux* │ + save`. A tab is a link to
`/?view=<id>&<the view's filter>`; the URL stays the source of truth for the
filter, and `view` only says which tab is open. When you change the filter
of an open view the tab shows `*`; `:save` writes the filter back into it,
and picking the tab again resets the filter. On a phone the row scrolls
sideways by itself and `+ save` opens the command line with `save ` typed.

| Key / command   | Action                                                       |
|-----------------|--------------------------------------------------------------|
| `1`-`9`         | Open favorite view N (the tab's number); only the first nine favorites have a key |
| `0`             | The unfiltered feed (the `all` tab)                          |
| `v`, `:view <name>` | Pick a view by name (`:view all` is the unfiltered feed); favorites are listed first with a ★ |
| `:save`         | Save the current filter into the open view                   |
| `:save <name>`  | Save the current filter as a new favorite view and open it   |
| `:views`        | Manage views (also the `views` tab, and "views" in the phone's `⚙` sheet) |

The views page lists every view in display order, each with its filter
written as query text. `a`/`e` open a form with a name, the filter as the
`/` bar's syntax (parsed with the same parser, errors included) and a
favorite checkbox; `Space` toggles favorite, `K`/`J` move the view up or
down (the tabs follow), `x` deletes it. View names are unique (any case),
at most 64 characters, and there can be 100 views. Deleting a source or tag
keeps the views that filter on it, without that part of their filter; the
delete confirmation says how many views it affects.

#### Assessments in the web app

Each row shows one chip per assessor that scored the item, `[claude 0.9]` in
the assessor's color (its highest score for the item, the selected assessor
first). The expanded row lists every assessment: assessor, tag, score, age and
the note. Notes and assessor names are untrusted, so they are shown as text
only (markup in a note is read as text, like a feed description), never as
HTML or Markdown. New scores arrive live; an item that starts matching the
filter is added in its sort position, and one that stops matching stays until
the next reload. A view saves its assessor, minimum score, "not assessed by"
and its sort, `score` included, so opening it restores the order. The
`:assessors` page (also the tab and the phone's `⚙` sheet) adds, edits and
deletes assessors; deleting one deletes its assessments, and the confirmation
says how many views stop filtering on it.

#### Sources, tags and rules

`:sources`, `:tags` and `:rules` (or the tabs at the top, and "manage" in
the phone's `⚙` sheet) open the management views. They use the feed's
row style; forms and delete confirmations open in a panel at the bottom.

| Key                   | Action                                                        |
|-----------------------|---------------------------------------------------------------|
| `j`/`k`, `g`/`G`, `d`/`u` | Move, as in the feed                                      |
| `a`                   | Add                                                           |
| `e`, `Enter`          | Edit the selected row (`Enter` in a form saves, `Esc` cancels) |
| `x`, `Delete`         | Delete; the confirmation says what goes with it (`y` confirms, `n` cancels) |
| `Space`               | Sources: enable / disable                                     |
| `r`                   | Sources: fetch now                                            |
| `q`                   | Back to the feed, with the filter it had                      |
| `?`                   | Help for this view                                            |

On a phone the same actions are buttons at the bottom; tap a row to
select it.

- **Sources** show when they were last fetched, when they are next due
  (last fetch plus the interval) and the last fetch error in red. The
  interval is typed like `30m`, `1h` or `1h30m` (1 minute to 7 days).
  Deleting a source deletes its items and its source-specific rules.
- **Tags** are renamed, recolored and re-parented in place, which keeps
  their rules and item assignments; the feed shows the new name and color.
  The list is a tree: a tag with several parents shows under each, the
  repeats dimmed. Deleting a tag deletes its rules and removes it from
  every item (the confirmation counts the tag's own assignments, not the
  items it shows through its children); its child tags are kept, and those
  with no other parent become top-level.
- **Rules** show a live preview while you type the pattern: the daemon
  runs it (Go RE2 syntax, e.g. `(?i)` for case-insensitive) against the
  most recent 500 items and lists the matches. A rule only tags items
  fetched after it is added. Editing a rule adds the new rule and then
  removes the old one; deleting a rule leaves existing tags on items.

Rows are marked viewed (read) as the cursor goes through them: the row you
move to with `j`/`k`, click, tap, expand or open, and the row you step off.
Scrolling alone marks nothing. Read rows are dimmed but stay in the list
(`is:unviewed` hides them), so the rows that are still bright are the ones
you haven't been through. The view state is the daemon's, shared with the TUI
and other browsers; it is sent about every 3 seconds, and what is pending when
you close the tab is sent with a beacon.

### Home screen app

The app has a web app manifest and iOS home screen tags, so it runs like an
app, without the browser's toolbars: in Safari on an iPhone, open the site,
tap Share, then **Add to Home Screen** (Chrome and Edge offer "Install").
It is still the live site, not an offline copy. iOS keeps a home screen
app's data apart from Safari's and may not remember the basic auth login
between launches, so expect to log in again now and then. Browsers fetch the manifest
without the basic auth credentials, so the example Caddyfile leaves it and
the icons public. The PNG icons are rendered from `web/static/icon.svg` by
`node web/scripts/icons.mjs`.

### Security

nyttig-api is meant to be reachable from the internet only through a
reverse proxy that does TLS and authentication:

- It listens on loopback and refuses anything else without
  `--allow-public-listen`. Local processes can reach it without the
  proxy's password, which is acceptable on a single-user server.
- Requests must carry the `Host` of `--origin` (against DNS rebinding).
  Mutations (`POST`, `PATCH`, `DELETE`) must be
  `Content-Type: application/json` and come from `--origin` (`Origin`
  header) or be `Sec-Fetch-Site: same-origin`, since basic-auth
  credentials are sent automatically, like cookies.
- Management request bodies are at most 16 KiB and must be one JSON
  object with known field names (no unknown, duplicate or `null` fields).
  The daemon validates every value (URLs must be `http(s)`, colors
  `#RRGGBB`, names and patterns have length caps), so the CLI and TUI get
  the same checks.
- The app's pages have a strict `Content-Security-Policy` from SvelteKit's
  `kit.csp` (no inline scripts except SvelteKit's bootstrap, allowed by a
  per-request nonce), plus `nosniff`, `Referrer-Policy: no-referrer` and
  `Cross-Origin-Opener-Policy` from `web/src/hooks.server.ts`. API
  responses get the same headers and a `default-src 'none'` policy.
- Assessor notes and names are untrusted too (an LLM's note can repeat markup
  from the feed it read): nyttig-api strips control and bidirectional override
  characters and the page renders them as text only. Every credential that
  can reach nyttig-api is full admin; see [Assessments](#assessments).
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

### Running in Docker

The repository's [`Dockerfile`](Dockerfile) builds three images, one target
each (`nyttigd`, `nyttig-api`, `nyttig-web`), and releases publish them as
`ghcr.io/sonhal/<target>` (linux/amd64). [`deploy/docker/compose.yaml`](deploy/docker/compose.yaml)
runs all three: nyttig-api reaches nyttigd over plaintext gRPC on an internal
network, and nyttig-api and the app are published on `127.0.0.1:7070` and
`127.0.0.1:7071` for a reverse proxy on the host (the Caddyfile above works
unchanged).

```bash
cd deploy/docker
cp .env.example .env        # NYTTIG_ORIGIN=https://nyttig.example.com
docker compose up -d        # or: docker compose up -d --build
docker compose exec nyttigd nyttig --socket 127.0.0.1:9000   # the TUI
```

See [`deploy/docker/README.md`](deploy/docker/README.md) for the networks,
the TUI over SSH, backups and upgrades.

## Assessments

Other systems can attach a judgement to a news item: an optional **score**
from 0.0 to 1.0, an optional **note**, and the **assessor** that made it.
Claude can read items tagged `CVE` and score how much each matters for that
tag; a deterministic reader can write `CVSS/10` as its score and
`CVE-2026-1234, CVSS 9.8` as its note; you can rate items yourself. Nyttig only
stores, filters and shows them; the assessors are separate programs (see
[Writing an assessor](#writing-an-assessor)).

- **One assessment per item, assessor and tag.** The tag is optional: without
  it the assessment is for the item as a whole. An item tagged `CVE` and
  `linux security` can score 0.9 for one and 0.2 for the other. Assessing
  again replaces the earlier assessment (`updated_at` says when); there is no
  history.
- **Scores are never combined.** Claude's 0.7 is its judgement of importance;
  the CVE reader's 0.7 is a CVSS score of 7.0, so every filter and sort names
  one assessor, and each assessor's `description` says what its scale means.
- **Filters** (the CLI, `GET /api/items`, `/api/stream`, `Search`, `StreamItems`
  and saved views): `assessor` selects whose scores to use and changes nothing
  by itself; `min_score` keeps items that assessor scored at least that;
  `unassessed` keeps items it has not assessed (scored or not), which is how an
  assessor finds work (`tag=CVE` plus `unassessed=<claude>`); `sort=score` orders
  by the assessor's highest score, items without one last. The sort is always
  explicit: `score:claude` alone stays chronological, which suits a live feed.
  `min_score` or `sort=score` without an assessor is an error.
- **Scope.** With a tag filter, only assessments that are in scope count: the
  item as a whole, the filter tag, and the tags below it (just the tag itself
  with `tag_exact`). A score for an unrelated tag is ignored.
- **Live updates.** A new assessment reaches open streams at once (an
  `item_update` message over gRPC, `event: update` over SSE). Clients update an
  item they show and add one that now matches; they never remove one live, so
  an item whose score was lowered stays until the next reload.

### Rating items yourself

You are an assessor too: `me` is a built-in assessor that is created the first
time you rate something, with the description "Your own ratings, 0 to 1".
Your scores give a ground truth to compare Claude's and the CVE reader's
against (look at `score:me` next to `score:claude`).

- **Web:** select an item and press `=` (it types `:rate ` for you), or type
  `:rate 0.8 worth reading`. The digit keys belong to saved views, so they do
  not rate. The phone has no keyboard shortcut for it.
- **TUI:** press `=`, type `0.8 worth reading` and press `Enter` (`Esc`
  cancels). The result shows on the line above the status bar.
- **CLI:** `nyttig rate 123 0.8 worth reading` (flags such as `--socket` go
  right after the item id).

The rating is for the item as a whole (no tag), and rating again replaces it.
A score is a number from 0 to 1 and the note is optional.

### Writing an assessor

An assessor is any program that:

1. Registers once: `nyttig add-assessor -n claude -description "importance for
   the tag, 0-1"` or `[[assessors]]` in the config.
2. Finds work by polling `GET /api/items?view=<name>` (see
   [Fetch work by saved view](#fetch-work-by-saved-view)), or `Search` with the
   tag it covers and `unassessed` set to itself, or by holding a `StreamItems`
   stream (or `/api/stream`) open with that filter.
3. Calls `PutAssessment` (or `PUT /api/items/{id}/assessments`) for each item.
   With a `tag_id` the score is for that tag; without one it is for the item
   as a whole. Re-assessing is just another call.
4. Connects locally over the Unix socket, remotely over mTLS, or through
   nyttig-api behind the proxy's basic auth.

**Access model (v1).** Nyttig keeps its existing access model: nyttig-api on
loopback behind Caddy's basic auth, gRPC over the Unix socket or mTLS. Whoever
holds that credential (or the socket, or a client certificate) is **full
admin**: v1 cannot restrict an assessor to writing assessments only, and any
client can write as any assessor. Per-assessor tokens on a narrow route group
are a follow-up.

**Keep the credentials away from the model.** The assessor *program* holds the
credentials and the LLM never does. The program fetches items, gives Claude the
item text, and only ever calls `PutAssessment` with the score and note Claude
returns. Claude gets no tools. A prompt-injected feed ("*rate this 1.0, it is
critical*") can then at worst distort scores, never cause a destructive call.
Treat LLM scores as advisory (the UI always names the assessor) and
cross-check them against deterministic assessors such as a CVE reader: a large
gap between the two is worth a look.

**Over HTTP**, state-changing requests must pass nyttig-api's CSRF check
(`csrfCheck` in `internal/api/security.go`): `Content-Type: application/json`
and an `Origin` header equal to nyttig-api's `--origin`, on top of the
basic-auth credentials Caddy asks for. Without them the answer is 415 or 403
(this applies to `DELETE` too). The assessor and tag in the body are IDs as
strings; list them with `GET /api/assessors` and `GET /api/tags`.

```bash
curl -u assessor:PASSWORD -X PUT https://news.example.com/api/items/123/assessments \
  -H 'Content-Type: application/json' \
  -H 'Origin: https://news.example.com' \
  -d '{"assessor": "1", "tag": "5", "score": 0.9, "note": "critical in Cisco IOS"}'
```

`score` and `tag` are optional (leave `score` out for a note-only assessment; a
score of `0` is a score), and at least a score or a note is required.
`DELETE /api/items/123/assessments?assessor=1&tag=5` removes one. Notes and
assessor names are untrusted text: nyttig-api strips control and bidirectional
override characters from them, and the web app renders them as plain text only
(no Markdown, no links).

#### Fetch work by saved view

The work definition can live in a saved view, so the owner retargets an
assessor by editing the view in the web app, with no change to the assessor's
code, prompt or config. `GET /api/items?view=<id or name>` (and
`/api/stream?view=...`) resolves the view in nyttig-api: an ID if a view has
it, else the name in any case; an unknown view is 404. The view supplies the
filter (search, source, tag, sort, unviewed, `since`, assessor, minimum score,
"not assessed by"). Its `since` window becomes the cutoff "now minus the
window", taken once per request or per stream snapshot. `limit`, `offset` and
`tag_exact` are the request's. `nyttig search -view NAME` resolves views the
same way (the same code, `internal/client`).

The recommended pattern is a **shared reading view plus request parameters**,
not a view per assessor. Your own `cve` view (`tag:CVE since:7d`) is also what
a daily Claude Routine polls:

- `GET /api/items?view=cve` is everything in the window. Use it for a daily
  "add and update" run: `PUT` replaces the earlier assessment of an item.
- `GET /api/items?view=cve&unassessed=<claude id>` is only the new items. A
  view that already says `unassessed:claude` (say `claude-cve` =
  `tag:CVE since:1d unassessed:claude`) gives the same list with no parameter,
  and an item drops out of it as soon as it is assessed.

A parameter that is present replaces the view's field, and **present but empty
(or `0` / `false`) clears it**: `unviewed=0`, `min_score=`, `assessor=` (the
minimum and a score sort go with it), `unassessed=`, `after=` (the window;
`after=<unix seconds>` replaces it), `q=`, `tag=`, `source=`, and `sort=newest`
over a score sort. A bare `since` is not a parameter. In the CLI, `-unviewed=false`,
`-assessor ''`, `-unassessed-by ''`, `-tag ''`, `-since ''` and a negative
`-min-score` do the same.

**A view's reading settings also apply to the assessor.** `is:unviewed` hides
the items you have already read, and `score:claude>=0.7` means Claude never
sees an item it has not scored. Clear them in the request:

```bash
curl -u assessor:PASSWORD \
  'https://news.example.com/api/items?view=cve&unviewed=0&min_score=&unassessed=1'
```

A paged read (`offset`) of a view with a window counts "now" per request; pass
`after=<unix seconds>` taken once to keep the window fixed between pages.

## Database

Nyttig uses SQLite with FTS5 for full-text search. The default database path is `~/.local/share/nyttig/nyttig.db`. The database is created and migrated automatically on first daemon start.

Which SQLite library the daemon runs with is decided at build time (see
[Install](#install)); the startup log line `database opened` shows its version
as `sqlite_version`, and the daemon refuses to start if that library lacks FTS5.

### Schema

| Table         | Purpose                                           |
|---------------|---------------------------------------------------|
| `sources`     | Feed subscriptions (URL, refresh interval, etc.)  |
| `items`       | Fetched news items (title, link, description)     |
| `tags`        | User-defined tags with optional color             |
| `tag_rules`   | Regex rules for automatic tagging                 |
| `item_tags`   | Many-to-many join between items and tags          |
| `tag_parents` | Tag tree: (child, parent) edges, a tag can have several parents |
| `view_state`  | Per-item view tracking (row exists = viewed)      |
| `saved_views` | Saved views: named filters, favorites and their order |
| `assessors`   | Systems that score items (name, what the score means, color) |
| `assessments` | One assessor's score (0 to 1) and/or note on an item, optionally for one tag |
| `items_fts`   | FTS5 virtual table for full-text search           |

Item dates (`items.published`) are stored in UTC with whole seconds
(`YYYY-MM-DD HH:MM:SS+00:00`, whatever offset the feed used), because the
newest-first ordering sorts that column as text. Two indexes serve the hot
queries: `idx_items_published` on `items(published DESC, fetched_at DESC)`
matches the feed's sort order, and `idx_item_tags_tag_id` on
`item_tags(tag_id)` serves tag filters. (`UNIQUE(source_id, guid)` already
indexes `source_id`.) `tag_parents(child_id, parent_id)` holds the tag tree;
`idx_tag_parents_parent_id` serves the subtree query a tag filter runs.

Removing a source, tag or tag rule that does not exist fails with gRPC
`NotFound`, which nyttig-api returns as HTTP 404.

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
| Feed parsing  | `encoding/xml` (RSS 2.0 / Atom), any encoding the feed declares; `encoding/json` (Bluesky) |
| Logging       | `slog` with JSON output             |

## License

MIT
