# Web client plan: nyttig-api and nyttig-web

Status: **agreed plan; phases 0 and 1 are done**, phases 2–4 are not started.
See [Phases](#phases-one-pr-each) for what phase 1 changed from this plan.

A browser client for nyttig with the same design goals as the TUI: compact,
information dense, keyboard first, log-viewer inspired, minimal. It runs on
the same server as `nyttigd`, is reachable over the internet through Caddy,
and also works on a phone.

## Decisions

| Topic            | Decision                                                                 |
|------------------|--------------------------------------------------------------------------|
| Framework        | SvelteKit 2 + Svelte 5 (runes), TypeScript strict                         |
| Rendering        | `adapter-node` (`node build`), feed rendered client-side (`ssr = false`)  |
| Server side      | Separate `nyttig-api` Go binary for `/api`; a gRPC client of `nyttigd`    |
| Browser API      | JSON (`protojson`) + Server-Sent Events                                   |
| Exposure         | Public internet via Caddy: Let's Encrypt TLS + HTTP basic auth            |
| Scope            | Feed view at TUI parity **and** management of sources, tags and rules     |
| Devices          | Desktop (keyboard first) and mobile (touch)                               |
| Theme            | Dark only                                                                 |
| Package manager  | pnpm                                                                      |
| Daemon changes   | Allowed when they make the web app much better or much simpler to build. Nice-to-haves are deferred |

## Architecture

```
phone / desktop browser
   │  https (Let's Encrypt), basic auth
   ▼
Caddy ─┬─ /*     ──▶  nyttig-web   (127.0.0.1:7071, SvelteKit adapter-node)
       └─ /api/* ──▶  nyttig-api   (127.0.0.1:7070)
                        │  /api/*       JSON
                        │  /api/stream  SSE
                        ▼  gRPC over unix socket (internal/client)
                       nyttigd       (unix socket /run/nyttig/nyttig.sock)
```

The app is served by SvelteKit's standard Node adapter rather than embedded
in the Go binary (changed during phase 1): standard build and serving
tooling, at the cost of Node.js on the server and a third service.

Why a separate binary:

- It follows the existing Docker-style model: nyttigd is the daemon, and
  the TUI, CLI and web are all clients of one gRPC API.
- The process that holds the DB and parses untrusted feeds is never
  exposed to HTTP.
- The web side can be restarted or upgraded on its own.
- It reuses `internal/client`, including its reconnect and backoff.

`internal/api` depends on the generated `pb.NyttigClient` interface, not on
a concrete connection. Handlers are then unit-testable with a fake client,
and the package could later be mounted inside nyttigd if that is ever wanted.

Why not Connect/gRPC-Web: browsers cannot use bidirectional streams, so
`StreamItems` would need a new RPC anyway. It would also add
runtime dependencies on both sides, and it needs working `buf` codegen.
JSON + SSE over roughly 20 endpoints is less machinery.

## Changes to nyttigd (phase 0)

Only changes that make the web app much better or much simpler to build.

| #  | Change | Why the web app needs it |
|----|--------|--------------------------|
| D1 | `UpdateSourceRequest`: use proto3 `optional` on every mutable field and add `color` and `abbreviation` | The management UI must edit color and abbreviation, which the RPC cannot do today. Field presence also fixes the "enabled unset vs false" ambiguity (see AGENT.md gotchas) and lets PATCH mean "absent = unchanged". Update the `update-source` CLI to match. |
| D2 | New `UpdateTag(id, optional name, optional color)` RPC | Without it, recoloring a tag means removing and re-adding it, and removal cascade-deletes its rules and item assignments. |
| D3 | `SearchRequest`: add `sort` and `unviewed_only` | "Load older" in a log-style view pages with `Search`, which has `offset` and `total` but ignores sort and the unviewed filter. The stream alone returns only the newest 200 items. |
| D4 | `StreamItems`: when the filter has a search query, only push items that match it (check `items_fts` by rowid) | Today pushed items skip the search filter. Without this, the browser would have to emulate FTS5 tokenization. The TUI also benefits. |
| D5 | Fetcher option `block_private_addresses` (config key plus flag, default off), enforced in the dialer's `Control` hook | Once "add source" is on the internet, it is an SSRF primitive: it can reach `169.254.169.254`, loopback and the LAN. The check must happen at connect time, not at URL-validation time, so DNS rebinding and redirects cannot bypass it. The web deployment guide turns it on. |
| D6 | New `TestTagRule(pattern, field, source_id, limit)` RPC: validates the pattern and returns the most recent matching items | The rule editor shows live matches. Go RE2 syntax such as `(?i)` differs from JS regex, so this can't be done correctly in the browser. Rules only apply to items fetched after the rule is created, so without a preview a new rule seems to do nothing. |
| D7 | Input validation in the service: URL must be `http(s)`, colors must match `^#[0-9A-Fa-f]{6}$`, `refresh_sec` ≥ 60, and names and patterns have length caps | The web is a new, internet-facing input path. Validate once at the service, not in each client. |
| D8 | Socket location and permissions (unit and docs only): `/run/nyttig/nyttig.sock` via `RuntimeDirectory=`, `UMask=0007` | The shipped unit uses `PrivateTmp=yes` with `/tmp/nyttig.sock`, so other services probably cannot see the socket. Verify this. |

Codegen was fixed first: `buf.yaml`/`buf.gen.yaml` moved to the repository
root and use local, pinned plugins, and a CI job fails when `internal/proto`
drifts from the proto (see AGENT.md).

Phase 0 also fixed three bugs found along the way:

- **Colors never reached clients.** `color`/`abbreviation` had been
  hand-added to the generated structs but not to the embedded descriptor,
  so protobuf-go dropped them on the wire.
- **Source edits needed a daemon restart.** A running fetch runner kept its
  original URL and interval. Edits now restart it (`scheduler.RestartSource`).
- **`systemctl reload` killed the daemon.** The old unit sent SIGHUP, which
  nyttigd doesn't handle. The new unit has no reload action.

The nyttig-api unit file is added in phase 1, together with the binary.

**Deferred (nice to have, not now):**

- View-state sync between clients: a `ViewedChanged` push message.
- A daemon event stream for a live fetch-log pane.
- A Kibana-style histogram of items over time.
- Retagging existing items when a rule is added.
- PWA/offline support.
- Light theme.
- Multiple users.

## nyttig-api (Go)

```
cmd/nyttig-api/main.go
    --listen        127.0.0.1:7070 (loopback only unless --allow-public-listen)
    --socket        nyttigd address (same as the nyttig client, mTLS flags too)
    --origin        https://nyttig.example.com   (required; CSRF / Host checks)
internal/api/
    server.go       routing (any non-API path is a JSON 404)
    api.go          JSON handlers → pb.NyttigClient
    stream.go       SSE bridge for StreamItems
    security.go     Origin / Sec-Fetch-Site / Host checks, security headers
```

### HTTP API

JSON is produced with `protojson` (`UseProtoNames`), so field names match
the proto. **`int64` values are encoded as strings.** The TypeScript side
treats IDs as opaque strings.

```
GET    /api/health                      daemon reachable?
GET    /api/sources                     ListSources
POST   /api/sources                     AddSource (client sends enabled=true; proto default is false)
PATCH  /api/sources/:id                 UpdateSource (D1: absent = unchanged)
DELETE /api/sources/:id                 RemoveSource
POST   /api/refresh[?source=:id]        RefreshSource
GET    /api/tags      POST /api/tags    PATCH /api/tags/:id (D2)   DELETE /api/tags/:id
GET    /api/rules     POST /api/rules   DELETE /api/rules/:id
POST   /api/rules/test                  TestTagRule (D6)
GET    /api/items?q&source&tag&sort&unviewed&limit&offset     Search (D3), for "load older"
GET    /api/stream?q&source&tag&sort&unviewed                 SSE
POST   /api/viewed   {"ids": ["1", "2"]}                       MarkViewed
```

Editing a rule is done in the client as "add the new rule, then remove the
old one". Removing a rule never touches `item_tags`, and adding first means
an invalid pattern loses nothing.

### SSE bridge

- One gRPC `StreamItems` per SSE connection. The initial filter comes from
  the query string.
- Events:
  - `event: reset`
  - `id: <item id>` / `event: item` / `data: <protojson Item>`
  - `event: complete`
  - `: ping` every 20 seconds, to keep proxies from closing idle connections.
- Headers: `Content-Type: text/event-stream`, `Cache-Control: no-store`,
  `X-Accel-Buffering: no`. Flush after each event.
- Filter change = the browser closes the EventSource and opens a new one.
  There is no browser→server message.
- Backpressure: the daemon's Hub drops pushes for slow subscribers, so
  nyttig-api reads the gRPC stream fast and has a bounded per-connection
  queue. On overflow it closes the SSE connection. The browser reconnects,
  gets `reset` plus a fresh snapshot, and resyncs instead of silently
  missing items.
- nyttigd has `MaxConcurrentStreams(100)`, which allows about one stream per open tab.

### Serving the app

The SvelteKit Node server (adapter-node) serves the app: `/_app/immutable/*`
with `Cache-Control: public, max-age=31536000, immutable`, and the page
itself (`no-cache`, set in `hooks.server.ts`) for every route.

## Frontend (SvelteKit)

```
web/
  package.json            "packageManager": "pnpm@10.x"
  svelte.config.js        adapter-node → build/, kit.csp
  src/hooks.server.ts     security headers on pages
  src/routes/
    +layout.ts            export const ssr = false
    +layout.svelte        shell: filter/command bar, status bar, global keymap
    +page.svelte          feed
    sources/ tags/ rules/ management views
  src/lib/
    api.ts                typed fetch wrappers (hand-written types mirroring protojson)
    stream.svelte.ts      EventSource lifecycle, connection status, reducer
    feed.svelte.ts        items Map<id, Item>, ordered ids, cursor, filter ⇄ URL
    keymap.ts             mode machine: normal | search | command | overlay | form
    query.ts              "tag:rust src:hn is:unviewed text" ⇄ filter
    viewed.ts             visible-ids tracking, 3s batches, sendBeacon on pagehide
    VirtualList.svelte    fixed row height, overscan, no library
    sanitize.ts           plain-text descriptions, safe links, color validation
```

Dependencies, kept to a minimum: `svelte`, `@sveltejs/kit`,
`@sveltejs/adapter-node`, `vite`, `typescript`, `svelte-check`, `vitest`,
`@playwright/test`. No CSS framework, no component library and no web
fonts from third-party CDNs. Use `ui-monospace`, or a self-hosted font file.

### Stream reducer

1. `reset`: start a snapshot buffer. Keep showing the old rows so the table
   doesn't flicker.
2. `item` before `complete`: add to the buffer.
3. `complete`: swap the buffer in, keeping the cursor on the same item ID
   if it still exists.
4. `item` after `complete` (live push): dedupe by ID, then insert according
   to the sort order.
5. Reconnect: the server sends a snapshot again, and dedupe by ID keeps it
   idempotent.

## UI design

Everything is monospace, single-line rows, with the TUI palette (as
implemented in phase 1, which follows `internal/tui/table.go`: the date is
gray, the description green and the domain blue):

| Token          | Value     | Use                        |
|----------------|-----------|----------------------------|
| `--bg`         | `#1E1E1E` | page                       |
| `--bar`        | `#2D2D2D` | status bar                 |
| `--bar-filter` | `#252525` | filter bar                 |
| `--sel`        | `#3A3D41` | selected row               |
| `--fg`         | `#E0E0E0` | text                       |
| `--dim`        | `#808080` | date, labels, brackets     |
| `--desc`       | `#6A9955` | description                |
| `--accent`     | `#569CD6` | domain, links              |
| `--unviewed`   | `#4EC9B0` | `●`, connected             |
| `--error`      | `#F44747` | disconnected, fetch errors |

Dark only: `color-scheme: dark`, `<meta name="color-scheme" content="dark">`,
and `theme-color` set to `--bg`.

### Desktop feed (≥ 720px)

```
/ kernel panic_        src:all  tag:linux  sort:newest  unviewed:off               [12 src]
● 30.09 10:32 HN   [linux]        Linux 6.18 released  Linus announced the rel…  kernel.org
  30.09 10:15 LOB  [rust] [linux] Rust for Linux status  An overview of where…      lwn.net
▶ 30.09 09:45 GO   [go]           Go 1.26.8 security release  Fixes GO-2026-…       go.dev
    id=8812 source="Go Blog" published=2026-09-30T09:45Z fetched=09:47Z author=-
    tags=go link=https://go.dev/blog/…
    Fixes GO-2026-6443 in net/http … (full description, plain text)
● connected · unviewed 23 · last fetch 2m ago · next 8m · ↑ 4 new (F)           ? help
```

Row height is fixed (20px), which is what makes the virtual list and
visible-ID tracking simple arithmetic.

### Mobile feed (< 720px)

```
┌──────────────────────────────────┐
│ [/ search…            ] [⚙] [⟳] │
├──────────────────────────────────┤
│ ● 10:32 HN [linux]    kernel.org │
│   Linux 6.18 released            │
│   10:15 LOB [rust]       lwn.net │
│   Rust for Linux status          │
├──────────────────────────────────┤
│ ● 23 unviewed · 2m    ↑4 new    │
└──────────────────────────────────┘
```

- Two-line rows with a fixed 44px height, which also meets touch-target
  guidelines. The description is shown only when a row is expanded.
- Tap a row to select and expand it. The expanded row has an explicit
  "open ↗" link, so scrolling never opens links by accident.
- `⚙` opens a bottom sheet with native `<select>`s for source, tag, sort
  and unviewed.
- The layout uses `100dvh`, `env(safe-area-inset-*)` with
  `viewport-fit=cover`, 16px inputs (prevents iOS zoom on focus) and hover
  styles only under `@media (hover: hover)`.
- The virtual list reads the row height from a CSS variable per breakpoint
  and re-measures on resize.

### Keymap (desktop)

| Key                     | Action                                                     |
|-------------------------|------------------------------------------------------------|
| `j`/`↓`, `k`/`↑`        | move                                                       |
| `g`/`Home`, `G`/`End`   | top / bottom                                               |
| `d`, `u`                | half page down / up (`Ctrl+d/u` best effort: browsers reserve them) |
| `/`                     | focus query bar; `Enter` applies, `Esc` clears and leaves the bar |
| `s`, `t`                | cycle source / tag (as in the TUI); `S`, `T` open a fuzzy picker |
| `o`                     | toggle sort                                                |
| `r`                     | refresh all (`R`: refresh the selected item's source)      |
| `Enter`                 | open link in new tab (`noopener,noreferrer`)               |
| `Space` / `l`           | expand / collapse row                                      |
| `F`                     | follow mode (tail): jump to newest and stick               |
| `:`                     | command line: `:sources` `:tags` `:rules` `:feed`          |
| `?`                     | help overlay                                               |
| `q` / `Esc`             | close overlay, expanded row or form                        |

Management views use the same row style, with these keys: `a` add, `e` edit
(inline form panel at the bottom), `x` delete (confirmation shows what
cascades), `Space` enable/disable a source, `r` refresh a source. Sources
show `last_fetch`, the next fetch computed from `refresh_sec`, and
`fetch_error` in `--error`. The rule editor shows D6 matches live, debounced.

One global `keydown` listener dispatches on the current mode. While an input
has focus, only `Esc`/`Enter` are intercepted. The table uses
`role="grid"` + `aria-activedescendant`.

### Log-viewer features

- **Follow mode:** in newest-first sort at the top, pushed items flow in.
  After scrolling away, the view stays put and the status bar counts
  "↑ N new".
- **Row expand:** shows the item as a `key=value` record, like a log line
  in Kibana.
- **Query syntax:** `tag:`, `src:`, `is:unviewed`, `sort:`, plus free text
  (FTS). Tag and source names are completed. The query is reflected in the
  URL (`/?q=...`), so a view can be bookmarked and the back button works.
- **Search highlighting** in titles, and a toggle between relative and
  absolute times.
- **Load older:** reaching the end of the list fetches the next page with
  `GET /api/items` (D3) and dedupes by ID.

## Security

Threat model: an internet-facing, single-user app behind basic auth that can
make the server fetch URLs and that renders untrusted feed content.

1. **Authentication at Caddy.**
   - Basic auth with a bcrypt hash from `caddy hash-password`, and a long
     random password (≥ 20 characters, kept in a password manager).
   - Caddy has no built-in rate limiting, so password strength is the main
     defense. Optionally run fail2ban on Caddy's 401s.
   - Same-origin `fetch` and `EventSource` send the cached credentials
     automatically.
2. **Loopback only.** nyttig-api listens on `127.0.0.1:7070` and refuses
   non-loopback addresses without `--allow-public-listen`. Local processes
   can bypass basic auth, which is accepted on a single-user VPS. The
   firewall only needs 80/443 (Caddy) and SSH open.
3. **CSRF.** Basic-auth credentials are sent automatically, like cookies. All
   mutations are `POST`/`PATCH`/`DELETE` with `Content-Type: application/json`,
   and nyttig-api rejects them unless `Origin` equals `--origin` or
   `Sec-Fetch-Site` is `same-origin`. It also checks the `Host` header.
4. **SSRF.** Covered by D5 (`block_private_addresses = true` in the web deployment) and D7.
5. **XSS from feeds.**
   - No `{@html}` anywhere. Descriptions are converted to plain text
     (an inert `<template>` + `textContent`, which runs no scripts and loads
     nothing; see phase 1 below for why not `DOMParser`).
   - Links must be `http:`/`https:`; anything else (`javascript:`, `data:`)
     is not rendered as a link. This is checked in nyttig-api and in the
     component.
   - Links get `rel="noopener noreferrer"` and `referrerpolicy="no-referrer"`.
   - Colors are validated before they are bound with `style:`.
   - No feed images are loaded (no tracking pixels).
6. **Headers** (set by the app server for pages, by nyttig-api for `/api`):
   - CSP for pages, from SvelteKit's `kit.csp`: `default-src 'none';
     script-src 'self' 'nonce-…'; style-src 'self'; style-src-attr
     'unsafe-inline'; connect-src 'self'; img-src 'self'; font-src 'self';
     manifest-src 'self'; base-uri 'none'; form-action 'self';
     frame-ancestors 'none'`. SvelteKit puts a fresh nonce on its own
     bootstrap script for each request. API responses get
     `default-src 'none'`.
   - Also: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
     `Cross-Origin-Opener-Policy: same-origin`,
     `Permissions-Policy: camera=(), microphone=(), geolocation=()`.
   - HSTS is set at Caddy.
7. **Regex rules.** Go RE2 is linear-time, so there is no ReDoS. Pattern
   length is capped by D7.
8. **Supply chain.**
   - npm packages run only at build time, but a compromised one could still
     inject code into the bundle.
   - Keep the dependency list short and commit `pnpm-lock.yaml`.
   - CI installs with `pnpm install --frozen-lockfile`.
   - pnpm 10 does not run dependency install scripts unless they are
     allow-listed (`onlyBuiltDependencies`, e.g. esbuild).
   - Consider `minimumReleaseAge` to avoid installing brand-new releases.
   - Add the `npm` ecosystem for `/web` to `dependabot.yml`.

## Deployment

nyttig is not deployed yet, so this is a fresh install with no migration.
The VPS has a single user, so nyttig-api listens on **TCP `127.0.0.1:7070`**
and Caddy proxies to it. Both daemons run as **system units** (managed by
PID 1), not user units:

- **Isolation.** nyttigd parses untrusted feeds and nyttig-api faces the
  internet. A bug in either then yields a service account, not your login
  account (SSH keys, `authorized_keys`, shell rc files).
- **Starts at boot** without `loginctl enable-linger`.
- **Sandboxing is reliable.** User units only get `PrivateTmp=`,
  `ProtectSystem=` etc. when unprivileged user namespaces are available.
- **systemd creates and owns the paths**: `/var/lib/nyttig`, `/run/nyttig`,
  `/etc/nyttig`. The socket moves out of `/tmp`, which fixes D8.

Accepted trade-off of loopback TCP: any local process can reach nyttig-api
without basic auth. On a single-user VPS that means only a compromised
local service, which is acceptable. nyttig-api binds `127.0.0.1` by default
and refuses a non-loopback `--listen` address unless `--allow-public-listen`
is passed, so a typo cannot expose it past Caddy.

```ini
# /etc/systemd/system/nyttigd.service
[Unit]
Description=Nyttig news aggregator daemon
After=network-online.target
Wants=network-online.target

[Service]
User=nyttig
Group=nyttig
ExecStart=/usr/local/bin/nyttigd --config /etc/nyttig/config.toml \
  --socket /run/nyttig/nyttig.sock --db-path /var/lib/nyttig/nyttig.db
StateDirectory=nyttig                 # /var/lib/nyttig, owned by nyttig
RuntimeDirectory=nyttig               # /run/nyttig, removed on stop
RuntimeDirectoryMode=0750
ConfigurationDirectory=nyttig         # /etc/nyttig
UMask=0007                            # socket srwxrwx---, group nyttig
Restart=on-failure
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallFilter=@system-service
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/nyttig-api.service
[Unit]
Description=Nyttig web client
Requires=nyttigd.service
After=nyttigd.service

[Service]
DynamicUser=yes                       # throwaway uid; owns no files, cannot read the DB
SupplementaryGroups=nyttig            # only to connect to /run/nyttig/nyttig.sock
ExecStart=/usr/local/bin/nyttig-api --listen 127.0.0.1:7070 \
  --socket unix:///run/nyttig/nyttig.sock --origin https://nyttig.example.com
Restart=on-failure
IPAddressDeny=any                     # no outbound network at all...
IPAddressAllow=localhost              # ...except accepting Caddy on loopback
RestrictAddressFamilies=AF_UNIX AF_INET
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
MemoryDenyWriteExecute=yes
SystemCallFilter=@system-service
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
```

Add your login user to the `nyttig` group (`sudo usermod -aG nyttig $USER`)
so the TUI can use `--socket /run/nyttig/nyttig.sock`.

Use absolute paths in `/etc/nyttig/config.toml`. `~` expands to the running
user's home, which is `/var/lib/nyttig` for the service account.

```caddyfile
nyttig.example.com {
	basic_auth {
		sondre $2a$14$...            # caddy hash-password
	}
	header Strict-Transport-Security "max-age=31536000; includeSubDomains"
	@notstream not path /api/stream
	encode @notstream zstd gzip       # never compress/buffer SSE
	reverse_proxy 127.0.0.1:7070 {
		flush_interval -1
	}
}
```

### Fresh install

```bash
sudo useradd --system --home-dir /var/lib/nyttig --shell /usr/sbin/nologin nyttig
sudo install -m 0755 nyttigd nyttig-api nyttig /usr/local/bin/
sudo install -D -o root -g nyttig -m 0640 sample_config.toml /etc/nyttig/config.toml
#   edit /etc/nyttig/config.toml: absolute paths, your sources/tags/rules
sudo install -d /opt/nyttig-web
sudo cp -r web/build web/package.json /opt/nyttig-web/   # needs Node.js 22+
sudo install -m 0644 deploy/systemd/nyttigd.service deploy/systemd/nyttig-api.service \
  deploy/systemd/nyttig-web.service /etc/systemd/system/
#   systemctl edit nyttig-api / nyttig-web: set --origin / ORIGIN
sudo usermod -aG nyttig "$USER"       # TUI access; log in again afterwards
sudo systemctl daemon-reload
sudo systemctl enable --now nyttigd nyttig-api nyttig-web
```

`StateDirectory=` creates `/var/lib/nyttig`, and nyttigd creates and
migrates the database on first start.

Let's Encrypt via Caddy's automatic HTTPS needs a DNS record for the host
and ports 80 and 443 reachable.

## Tooling, tests and CI

- **Development:** run `pnpm dev` in `web/`, with Vite proxying `/api` to a
  local `nyttig-api --listen 127.0.0.1:7070`, which connects to a local
  nyttigd.
- **Go tests:** `internal/api` handlers against a fake `pb.NyttigClient`
  (`httptest`), SSE framing and backpressure, and the security middleware
  (Origin, Host, headers).
- **Vitest:** stream reducer, query parser, keymap mode machine, sanitizers.
- **Playwright end-to-end:** real nyttigd with feeds from a local test HTTP
  server, plus nyttig-api and the app's Node server behind a small proxy
  that routes like the Caddyfile.
  - Desktop and mobile viewports (Chromium device emulation).
  - Real iOS Safari is checked by hand.
- **CI:**
  - A new `web` job: pnpm setup, `pnpm install --frozen-lockfile`,
    `svelte-check`, `vitest run`, `vite build`, Playwright.
  - The Web job uploads the app build; the Build job builds the Go
    binaries, which don't depend on Node.

## Phases (one PR each)

0. **Groundwork (done):** codegen fix, D1–D8, CLI updates, system unit file
   in `deploy/systemd/` replacing the root `nyttigd.service` user unit, tests,
   README/AGENT.md.
1. **Feed at TUI parity (done):** nyttig-api (API, SSE, security middleware)
   and the SvelteKit feed: desktop and mobile layouts, keymap,
   view tracking, status bar. It also brought forward from later phases:
   row expand (the mobile layout needs tap-to-expand), the nyttig-api and
   nyttig-web system units, the Caddy example, the CI web job and the Playwright
   end-to-end tests. Differences from the plan above:
   - The web client is two services: `nyttig-web`, the app (built from
     `web/`), and `nyttig-api`, the Go binary that serves `/api` (package
     `internal/api`).
   - `nyttig-web` runs on SvelteKit's standard `adapter-node` server
     instead of an adapter-static build embedded in the Go binary with
     `go:embed`. `nyttig-api` is API-only, Caddy routes by path, and the
     CSP comes from `kit.csp` (a nonce) instead of a hash computed at
     startup. This needs Node.js 22+ on the server and a third unit,
     `deploy/systemd/nyttig-web.service`.
   - Descriptions are converted to text with an inert `<template>` instead
     of `DOMParser`. Both are inert, but `DOMParser` still processes
     `<style>` elements, so CSP logged a violation for every description
     that contained one. Script and style bodies are removed before
     taking `textContent`.
   - The palette follows the TUI's actual colors (see the table above).
   - Dates are `dd.MM HH:mm` in the browser's time zone; the TUI prints UTC.
   - The unviewed count in the status bar is the `total` of
     `GET /api/items?unviewed=1&limit=1` for the current filter. The next
     fetch is estimated from each enabled source's `last_fetch` plus
     `refresh_sec`.
   - The filter is in the URL as separate parameters
     (`?q=&source=&tag=&sort=&unviewed=1`, IDs for source and tag); the
     query syntax (`tag:` `src:` …) comes with phase 3.
   - `Host` must match `--origin` exactly, so a health check on the
     loopback port needs `-H 'Host: …'`.
2. **Management:** source, tag and rule views with forms, delete
   confirmations and the live rule preview (D6).
3. **Log-viewer features:** follow mode, query syntax, load older, help
   overlay, `:` command line, highlighting, `S`/`T` pickers.
4. **Deployment and hardening:** a deployment guide, and whatever running
   it in production shows is missing (the units, Caddy example, CI job and
   end-to-end tests landed in phase 1).
