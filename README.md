# Nyttig

> Warning: This project is generated using LLMs

Nyttig is a news aggregator with a keyboard-driven terminal UI in the style of
[K9s](https://k9scli.io/). It follows RSS, Atom and JSON feeds, Bluesky accounts
and the EU Vulnerability Database, tags items with regex rules, and has
full-text search, saved views, scores from external assessors, and digests.
There's also a web app for the browser and phone.

## Architecture

```
nyttigd      daemon: fetches feeds, tags items, stores them in SQLite, serves gRPC
nyttig       client: TUI (no arguments) and CLI subcommands
nyttig-api   the web app's HTTP API (JSON + SSE), a gRPC client of nyttigd
nyttig-web   the web app (SvelteKit, web/), served by Node
```

Clients connect to the daemon over a Unix socket, or over TCP with mutual TLS.
The daemon pushes new items to open clients as they are fetched.

## Install

Requires Go 1.26+ and a C compiler (the SQLite driver uses cgo).

```bash
go install -tags sqlite_fts5 github.com/sonhal/nyttig/cmd/...@latest
```

The `sqlite_fts5` tag is required. It builds the bundled SQLite with FTS5;
without it the daemon refuses to start. To link the system's SQLite instead,
build with `-tags libsqlite3` (needs `libsqlite3-dev`); the release bundles
are built that way. The web app also needs Node.js 22+ and pnpm.

Releases on GitHub include prebuilt Linux binaries, the web app and the deploy
files. Container images are published as `ghcr.io/sonhal/<nyttigd|nyttig-api|nyttig-web>`.

## Quick start

1. Copy [`sample_config.toml`](sample_config.toml) to `~/.config/nyttig/config.toml`
   and edit the sources, tags and rules. A minimal file:

   ```toml
   socket = "/tmp/nyttig.sock"
   db_path = "~/.local/share/nyttig/nyttig.db"

   [[sources]]
   name = "Lobsters"
   url = "https://lobste.rs/rss"

   [[tags]]
   name = "rust"
   color = "#FF6B35"

   [[tag_rules]]
   tag = "rust"
   pattern = "(?i)\\brust\\b"
   ```

2. Start the daemon. It only reads a config file when `--config` is given.

   ```bash
   nyttigd --config ~/.config/nyttig/config.toml
   ```

3. Open the TUI, or use the CLI:

   ```bash
   nyttig
   nyttig add-source -n "Rust Blog" -u https://blog.rust-lang.org/feed.xml
   nyttig search -tag rust async
   ```

## Configuration

Unknown keys are an error. Flags override the file. The daemon has no reload;
restart it to pick up changes.

### Top-level settings

| Field | Default | Description |
|---|---|---|
| `socket` | `/tmp/nyttig.sock` | Unix socket path or `host:port` |
| `db_path` | `~/.local/share/nyttig/nyttig.db` | SQLite database path |
| `log_level` | `info` | `debug`, `info`, `warn`, `error` |
| `block_private_addresses` | `false` | Refuse to fetch from non-public addresses (see [Security](#security)) |

### `[tls]`

Optional; enables [mutual TLS](#remote-access-with-mutual-tls). Without
`listen`, TLS applies to `socket`. With `listen`, the daemon serves mTLS on
that TCP address and keeps `socket` as a plaintext Unix socket for local
clients such as nyttig-api.

| Field | Required | Description |
|---|---|---|
| `cert` | yes | Server certificate (PEM) |
| `key` | yes | Server private key (PEM) |
| `client_ca` | yes | CA bundle (PEM) that client certificates must chain to |
| `listen` | no | Extra TCP address for mTLS, e.g. `:9090` |

### `[[sources]]`

| Field | Required | Default | Description |
|---|---|---|---|
| `name` | yes (optional for `bluesky`, `euvd`) | — | Display name |
| `url` | yes (optional for `euvd`) | — | Feed URL, Bluesky profile URL, or EUVD search URL |
| `type` | no | `rss` | `rss`, `atom`, `bluesky` or `euvd` |
| `refresh_sec` | no | `3600` | Fetch interval in seconds |
| `color` | no | — | Hex color for the source chip |
| `abbreviation` | no | — | Short display name |

**RSS and Atom.** The format is read from the document, so either type works
for RSS 0.9x/1.0/2.0, Atom 1.0 and JSON Feed 1.x. An item's `<comments>` link
(Hacker News, Lobsters) is appended to the description.

Feeds that work as-is: Hacker News (`https://news.ycombinator.com/rss`, or
filtered via [hnrss.org](https://hnrss.org)), Lobsters (`https://lobste.rs/t/<tag>.rss`),
GitHub (`https://github.com/<owner>/<repo>/releases.atom`), arXiv
(`https://rss.arxiv.org/rss/cs.CR+cs.PL`), PyPI
(`https://pypi.org/rss/project/<name>/releases.xml`), Mastodon
(`https://<instance>/@<user>.rss`), and newsletters through an e-mail-to-Atom
bridge such as [Kill the Newsletter](https://kill-the-newsletter.com).

**Bluesky.** `type = "bluesky"` follows one account's posts (no replies or
reposts) through the public API; no login. In the config, use the profile URL
with the DID (`https://bsky.app/profile/did:plc:...`), since handles can
change. The CLI and web app also accept a handle and look up the DID for you.

**EUVD.** `type = "euvd"` reads ENISA's [EU Vulnerability Database](https://euvd.enisa.europa.eu).
The `url` is an `https://euvdservices.enisa.europa.eu/api/search?...` URL;
left out, it means exploited vulnerabilities. Filters: `exploited`,
`fromScore`/`toScore` (whole numbers 0–10), `fromEpss`/`toEpss` (0–100),
`fromDate`/`toDate`, `vendor`, `product`, `assigner`, `text`. Each fetch reads
records updated in the last 14 days, up to 2,000. CVSS and EPSS are stored as
[assessments](#assessments) by the assessors `euvd-cvss` (CVSS / 10) and
`euvd-epss` (EPSS as a fraction), so `score:euvd-cvss>=0.9` finds the critical
ones.

### `[[tags]]`, `[[tag_rules]]`, `[[assessors]]`

| Table | Field | Description |
|---|---|---|
| `[[tags]]` | `name` | Unique name (required) |
| | `color` | Hex color |
| | `parents` | Names of parent tags (see [Tags](#tags)) |
| `[[tag_rules]]` | `tag` | Tag to assign (required) |
| | `pattern` | Go (RE2) regex (required) |
| | `field` | `title`, `description` or `both` (default) |
| | `source` | Restrict to one source by name (default: all) |
| | `priority` | Lower runs first (default `0`) |
| `[[assessors]]` | `name` | Unique name (required); an existing assessor is left unchanged |
| | `description` | What the score means |
| | `color` | Hex color for the score chip |

Seeding from the config only adds; it never removes or overwrites what was
changed in a client.

## Flags

**`nyttigd`**

| Flag | Description |
|---|---|
| `--config` | Config file (none by default) |
| `--socket`, `--db-path`, `--log-level` | Override the config values |
| `--tls-cert`, `--tls-key`, `--tls-client-ca` | Enable mTLS (all three together) |
| `--tls-listen` | Extra TCP address served with mTLS |
| `--block-private-addresses` | As the config key |

**`nyttig`** (every subcommand and the TUI): `--socket`, and for mTLS
`--tls-cert`, `--tls-key`, `--tls-ca`, `--tls-server-name`.

**`nyttig-api`**

| Flag | Default | Description |
|---|---|---|
| `--origin` | (required) | The URL the browser uses; other `Host`s and cross-origin writes are refused |
| `--listen` | `127.0.0.1:7070` | Must be loopback unless `--allow-public-listen` |
| `--socket` | `/tmp/nyttig.sock` | Daemon socket or TCP address |
| `--tls-*` | | mTLS to a remote daemon, as for `nyttig` |
| `--log-level` | `info` | |

## TUI

| Key | Action |
|---|---|
| `/`, `Esc` | Search (full text) / clear |
| `s`, `t` | Cycle source / tag filter |
| `o` | Cycle sort (newest, oldest, score) |
| `a`, `m` | Cycle assessor / minimum score (0.5, 0.7, 0.9) |
| `=` | Rate the selected item (`<score> [note]`) |
| `i` | Show the selected item's assessments |
| `j`/`k`, `g`/`G`, `Ctrl+d`/`Ctrl+u` | Move, top/bottom, half page |
| `Enter` | Open the link in the browser |
| `r` | Refresh all sources |
| `D` | Digests screen (`j`/`k` history, `[`/`]` series, `d`/`u` scroll, `1`-`9` inputs, `q` back) |
| `q` | Quit |

Rows are marked viewed as you scroll past them; `●` marks unviewed rows. With
an assessor selected, rows it scored above 0.5 get an amber bar.

## CLI

```
nyttig add-source      -n <name> -u <url> [-t rss|atom|bluesky|euvd] [-r refresh_sec] [-color <hex>] [-abbreviation <short>]
nyttig list-sources
nyttig update-source   -id <id> [-n] [-u] [-t] [-r] [-enable|-disable] [-color] [-abbreviation]
nyttig remove-source   -id <id>
nyttig add-tag         -n <name> [-c <hex>] [-parent <name|id>]...
nyttig list-tags
nyttig update-tag      -id <id> [-n] [-c] [-parent <name|id>]... [-no-parents]
nyttig remove-tag      -id <id>
nyttig add-tag-rule    -tag <name|id> -p <regex> [-f title|description|both] [-s source_id] [-priority N]
nyttig test-tag-rule   -p <regex> [-f ...] [-s source_id] [-l limit]
nyttig list-tag-rules
nyttig remove-tag-rule -id <id>
nyttig list-views
nyttig add-view        -n <name> [filter flags] [-favorite]
nyttig update-view     (-id <id> | -n <name>) [-rename <name>] [filter flags | -no-source | -no-tag | -no-since | -no-columns ...]
nyttig remove-view     (-id <id> | -n <name>)
nyttig reorder-views   <id|name>...
nyttig list-assessors
nyttig add-assessor    -n <name> [-description <text>] [-c <hex>]
nyttig update-assessor (-id <id> | -n <name>) [-rename] [-description] [-c]
nyttig remove-assessor (-id <id> | -n <name>)
nyttig assess          <item-id> -assessor <name|id> [-tag <name|id>] [-score <0-1>] [-note <text>]
nyttig unassess        <item-id> -assessor <name|id> [-tag <name|id>]
nyttig rate            <item-id> <score 0-1> [note...]
nyttig list-series     [-assessor <name|id>]
nyttig add-series      -assessor <name|id> -n <name> [-description <text>]
nyttig update-series   <series> [-n] [-description]
nyttig remove-series   <series>
nyttig reorder-series  <series>...
nyttig list-digests    <series> [-limit N] [-before <digest-id>]
nyttig show-digest     <digest-id>
nyttig add-digest      <series> -title <text> -start <time> -end <time> (-body <text> | -body-file <path|->) [-items 1,2] [-inputs 4,5]
nyttig update-digest   <digest-id> [same flags as add-digest]
nyttig remove-digest   <digest-id>
nyttig search          [-view <name|id>] [filter flags] [-l limit] [-offset N] [-exact] [query...]
nyttig refresh         [-id <source_id>]
```

Filter flags: `-q <text>`, `-source <name|id>` (`-s <id>` in `search`),
`-tag <name|id>`, `-unviewed`, `-since <window>`, `-sort newest|oldest|score`,
`-assessor <name|id>`, `-min-score <0-1>`, `-unassessed-by <name|id>`, and for
views `-columns <list>`.

- `update-*` commands change only the flags you pass; `''` clears a value.
- A **window** (`-since`) is `<n><unit>` with unit `h`, `d`, `w`, `mo` or `y`
  (`24h`, `7d`, `1mo`). It counts back from now; an item's date is its
  published date, or its fetch time if the feed has none.
- **Columns** are `date`, `source`, `tags`, `score`, `title`, `desc`,
  `domain`. `-columns title,source` shows those; `-columns -desc` hides one.
- `search -view NAME` runs a saved view; other flags override its fields.
- `<series>` is `<assessor>/<name>` or an ID. Digest times are RFC 3339 or
  `YYYY-MM-DD` (UTC).
- Tag rules apply to items fetched after the rule is added. Try a pattern
  with `test-tag-rule` first.

### Validation

The daemon checks everything clients send. Config seeding is not checked.

| Value | Rule |
|---|---|
| Source URL | Absolute `http(s)` URL, no credentials, ≤ 2048 bytes |
| Refresh interval | 60 seconds to 7 days |
| Colors | `#RRGGBB` |
| Names | No control characters; sources ≤ 200, tags, assessors, views and series ≤ 64, abbreviations ≤ 16 characters |
| Tag rule pattern | Valid RE2, ≤ 1024 bytes |
| Score | 0 to 1 |
| Note | UTF-8, ≤ 4096 bytes; an assessment needs a score or a note |
| Digest | Title ≤ 200 characters, body ≤ 64 KiB, ≤ 1000 items, ≤ 100 inputs |

Duplicate source URLs and tag, assessor, view and series names are rejected.

## Tags

Tags are assigned only by rules, when an item is fetched. Rules are global or
per source and run in `priority` order; an item can get several tags.

A tag can have several parents. Filtering by a tag also shows items with any
tag below it, so `cyber security` above `CVE` shows CVE items too.
`search -exact` matches the tag alone. Parents that would form a cycle are
rejected. Deleting a tag keeps its children.

## Saved views

A saved view is a named filter (search, source, tag, unviewed, window, sort,
assessor, minimum score, "not assessed by", columns) stored in the daemon and
shared by the web app and the CLI. Favorites are the web app's tabs. Deleting
a source, tag or assessor keeps the views that used it, without that part.

## Web app

The same feed in a browser, plus pages to manage sources, tags, rules, views,
assessors and digests. It works with the keyboard on a desktop and with touch
on a phone, and can be added to the home screen. Press `?` for every key and
command on the current page.

```
browser ── https, basic auth ──▶ Caddy ─┬─ /api/* ──▶ nyttig-api ── unix socket ──▶ nyttigd
                                        └─ /*     ──▶ nyttig-web (node build)
```

**Develop locally:**

```bash
nyttig-api --origin http://localhost:5173
pnpm --dir web install && pnpm --dir web dev     # http://localhost:5173
```

**Build:** `pnpm --dir web build` writes `web/build/`, run with `node build`
(it takes `HOST`, `PORT` and `ORIGIN`).

**Common keys:** `j`/`k` move, `/` query bar, `s`/`t` cycle source/tag,
`S`/`T`/`v`/`a` pick a source/tag/view/assessor by name, `1`-`9` favorite
views, `0` unfiltered, `C` columns, `F` follow newest, `=` rate, `Enter` open,
`Space` expand, `:` command line.

**Query bar:** `kernel tag:rust src:"Hacker News" is:unviewed since:7d sort:oldest`.
Also `score:claude>=0.7`, `unassessed:claude`, `sort:score` (needs a `score:`
term) and `cols:title,source`. Other words are the full-text search. The
filter is kept in the URL, so it can be bookmarked.

**Commands:** `:sources`, `:tags`, `:rules`, `:views`, `:assessors`,
`:digests`, `:save [name]` (save the filter into the open view, or as a new
one), `:view <name>`, `:rate <score> [note]`, `:refresh`, `:help`.

## Remote access with mutual TLS

To reach the daemon from another machine, serve it on TCP with mutual TLS:
only clients with a certificate from your CA can connect.

```bash
# Certificates; list every name and address clients will dial.
./scripts/gen-certs.sh ./certs nyttig.example.com

# Daemon: mTLS on :9090, plus the local plaintext socket.
nyttigd --socket /tmp/nyttig.sock --tls-listen :9090 \
  --tls-cert certs/server.pem --tls-key certs/server.key --tls-client-ca certs/ca.pem

# Client
nyttig --socket nyttig.example.com:9090 \
  --tls-cert certs/client.pem --tls-key certs/client.key --tls-ca certs/ca.pem
```

Keep the `*.key` files private. Use `--tls-server-name` when dialing an
address the certificate doesn't list.

## Deploying

- **VPS with systemd and Caddy:** [`deploy/README.md`](deploy/README.md). The
  hardened units are in [`deploy/systemd/`](deploy/systemd/) and the proxy
  config in [`deploy/Caddyfile`](deploy/Caddyfile).
- **Docker Compose:** [`deploy/docker/README.md`](deploy/docker/README.md).

## Security

- **nyttig-api has no authentication of its own.** Put it behind a reverse
  proxy that does TLS and authentication (the Caddyfile uses basic auth). It
  listens on loopback, checks `Host` and requires same-origin JSON for writes.
- **Every credential is full admin.** Anyone who can reach nyttig-api, the
  daemon socket, or holds a client certificate can do anything, including
  writing as any assessor.
- **Feed content is untrusted.** The web app renders it as text only, checks
  links and colors, and loads no feed images. The same goes for assessment
  notes and digests.
- **SSRF.** If untrusted people can add sources, set
  `block_private_addresses = true`. The daemon then refuses to connect to
  loopback, private, link-local, CGNAT, reserved and documentation addresses
  (checked on the resolved IP at connect time, so DNS tricks and redirects
  don't get around it) and ignores HTTP proxy variables.

## Assessments

An **assessor** is a program outside the daemon (or you) that attaches a
**score** from 0 to 1 and/or a **note** to an item, optionally for one tag.
Nyttig stores, filters and shows them.

- One assessment per item, assessor and tag; assessing again replaces it.
- Scores of different assessors are never combined: filters and the score
  sort always name one assessor. With a tag filter, only assessments for the
  item as a whole or for that tag (and its children) count.
- New scores reach open clients live.
- **Rate items yourself** with `=` in the TUI or web app, or `nyttig rate`.
  Your ratings are stored under the assessor `me`.

### Writing an assessor

1. Register it: `nyttig add-assessor -n claude -description "importance, 0-1"`.
2. Find work: `GET /api/items?view=<name>&unassessed=<assessor id>`, the gRPC
   `Search` with the same filter, or a `StreamItems` / `/api/stream` stream.
3. Write: `PutAssessment`, or `PUT /api/items/{id}/assessments`.

Over HTTP, writes need `Content-Type: application/json` and an `Origin` equal
to nyttig-api's `--origin`. IDs are strings.

```bash
curl -u assessor:PASSWORD -X PUT https://news.example.com/api/items/123/assessments \
  -H 'Content-Type: application/json' -H 'Origin: https://news.example.com' \
  -d '{"assessor": "1", "tag": "5", "score": 0.9, "note": "critical in Cisco IOS"}'
```

With `?view=`, the view supplies the filter and request parameters override
it; an empty parameter (`min_score=`, `unviewed=0`) clears that field. Pass
`after=<unix seconds>` to keep a time window fixed across pages.

**Keep credentials away from the model.** The assessor program holds them and
calls the API with the score the model returns; the model gets no tools. A
prompt-injected feed can then distort scores, but nothing else.

## Digests

A **digest** is a Markdown document an assessor writes about many items
("today's CVE news"), kept in a **series** that belongs to that assessor.
A digest records the period it covers, the items it's based on, and the
earlier digests it used as input, so a monthly digest can be built from the
dailies. Adding always creates a new digest; editing overwrites it.

Write one with `AddDigest`, `nyttig add-digest`, or over HTTP (same rules as
assessments):

| Route | Does |
|---|---|
| `GET /api/digest-series?assessor=<id>` | List series |
| `POST /api/digest-series` `{assessor, name, description?}` | Create a series |
| `PATCH`, `DELETE /api/digest-series/{id}` | Edit or delete (with its digests) |
| `GET /api/digests?series=<id>&before=<id>&limit=&body=1` | A page of digests, newest first |
| `POST /api/digests` `{series, title, body, period_start, period_end, items?, inputs?}` | Write a digest |
| `GET`, `PATCH`, `DELETE /api/digests/{id}` | Read, overwrite or delete one |

In a body, `[#123]` links to item 123 when it is one of the digest's items.
Digests are untrusted text, and injection compounds: a poisoned daily is read
by every digest built from it, so treat earlier digests like feed text in the
assessor's prompt.

## License

MIT
