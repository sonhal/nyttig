# nyttig-web: plan for a web client

Status: **agreed plan; phase 0 is done**, phases 1–4 are not started.

A browser client for nyttig with the same design goals as the TUI: compact,
information dense, keyboard first, log-viewer inspired, minimal. It runs on
the same server as `nyttigd`, is reachable over the internet through Caddy,
and also works on a phone.

## Decisions

| Topic            | Decision                                                                 |
|------------------|--------------------------------------------------------------------------|
| Framework        | SvelteKit 2 + Svelte 5 (runes), TypeScript strict                         |
| Rendering        | `adapter-static` SPA (`ssr = false`), embedded in a Go binary             |
| Server side      | Separate `nyttig-web` Go binary; a gRPC client of `nyttigd`               |
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
Caddy  ── reverse_proxy ──▶  nyttig-web   (127.0.0.1:7070)
                              │  static SvelteKit build (go:embed)
                              │  /api/*       JSON
                              │  /api/stream  SSE
                              ▼  gRPC over unix socket (internal/client)
                             nyttigd       (unix socket /run/nyttig/nyttig.sock)
```

Why a separate binary:

- It follows the existing Docker-style model: nyttigd is the daemon, and
  the TUI, CLI and web are all clients of one gRPC API.
- The process that holds the DB and parses untrusted feeds is never
  exposed to HTTP.
- The web side can be restarted or upgraded on its own.
- It reuses `internal/client`, including its reconnect and backoff.
- No Node.js on the server: one static binary.

`internal/web` depends on the generated `pb.NyttigClient` interface, not on
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

The nyttig-web unit file is added in phase 1, together with the binary.

**Deferred (nice to have, not now):**

- View-state sync between clients: a `ViewedChanged` push message.
- A daemon event stream for a live fetch-log pane.
- A Kibana-style histogram of items over time.
- Retagging existing items when a rule is added.
- PWA/offline support.
- Light theme.
- Multiple users.

## nyttig-web (Go)

```
cmd/nyttig-web/main.go
    --listen        127.0.0.1:7070 (loopback only unless --allow-public-listen)
    --socket        nyttigd address (same as the nyttig client, mTLS flags too)
    --origin        https://nyttig.example.com   (required; CSRF / Host checks)
internal/web/
    server.go       routing, static files + SPA fallback, cache headers
    api.go          JSON handlers → pb.NyttigClient
    stream.go       SSE bridge for StreamItems
    security.go     Origin / Sec-Fetch-Site / Host checks, security headers, CSP
    dist/           adapter-static output, embedded (placeholder index.html committed)
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
  nyttig-web reads the gRPC stream fast and has a bounded per-connection
  queue. On overflow it closes the SSE connection. The browser reconnects,
  gets `reset` plus a fresh snapshot, and resyncs instead of silently
  missing items.
- nyttigd has `MaxConcurrentStreams(100)`, which allows about one stream per open tab.

### Static serving

- `/_app/immutable/*` gets `Cache-Control: public, max-age=31536000, immutable`.
- HTML gets `no-cache`.
- Unknown non-`/api` paths serve the SPA fallback page.

## Frontend (SvelteKit)

```
web/
  package.json            "packageManager": "pnpm@10.x"
  svelte.config.js        adapter-static → ../internal/web/dist, fallback page
  src/routes/
    +layout.ts            export const ssr = false; export const prerender = false
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
`@sveltejs/adapter-static`, `vite`, `typescript`, `svelte-check`, `vitest`,
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

Everything is monospace, single-line rows, with the TUI palette:

| Token        | Value     | Use                      |
|--------------|-----------|--------------------------|
| `--bg`       | `#1E1E1E` | page                     |
| `--bar`      | `#2D2D2D` | filter/status bars       |
| `--sel`      | `#3A3D41` | selected row             |
| `--fg`       | `#E0E0E0` | text                     |
| `--dim`      | `#808080` | description, domain      |
| `--time`     | `#6A9955` | timestamp                |
| `--accent`   | `#569CD6` | chips, links             |
| `--unviewed` | `#4EC9B0` | `●`, connected           |
| `--error`    | `#F44747` | disconnected, fetch errors |

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
2. **Loopback only.** nyttig-web listens on `127.0.0.1:7070` and refuses
   non-loopback addresses without `--allow-public-listen`. Local processes
   can bypass basic auth, which is accepted on a single-user VPS. The
   firewall only needs 80/443 (Caddy) and SSH open.
3. **CSRF.** Basic-auth credentials are sent automatically, like cookies. All
   mutations are `POST`/`PATCH`/`DELETE` with `Content-Type: application/json`,
   and nyttig-web rejects them unless `Origin` equals `--origin` or
   `Sec-Fetch-Site` is `same-origin`. It also checks the `Host` header.
4. **SSRF.** Covered by D5 (`block_private_addresses = true` in the web deployment) and D7.
5. **XSS from feeds.**
   - No `{@html}` anywhere. Descriptions are converted to plain text
     (`DOMParser` + `textContent`, which doesn't run scripts).
   - Links must be `http:`/`https:`; anything else (`javascript:`, `data:`)
     is not rendered as a link. This is checked in nyttig-web and in the
     component.
   - Links get `rel="noopener noreferrer"` and `referrerpolicy="no-referrer"`.
   - Colors are validated before they are bound with `style:`.
   - No feed images are loaded (no tracking pixels).
6. **Headers** (set by nyttig-web):
   - CSP: `default-src 'none'; script-src 'self' 'sha256-…'; style-src 'self';
     style-src-attr 'unsafe-inline'; connect-src 'self'; img-src 'self';
     font-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self';
     frame-ancestors 'none'`. nyttig-web hashes the inline bootstrap script
     of the embedded `index.html` at startup, so the header always matches
     the build.
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
The VPS has a single user, so nyttig-web listens on **TCP `127.0.0.1:7070`**
and Caddy proxies to it. Both daemons run as **system units** (managed by
PID 1), not user units:

- **Isolation.** nyttigd parses untrusted feeds and nyttig-web faces the
  internet. A bug in either then yields a service account, not your login
  account (SSH keys, `authorized_keys`, shell rc files).
- **Starts at boot** without `loginctl enable-linger`.
- **Sandboxing is reliable.** User units only get `PrivateTmp=`,
  `ProtectSystem=` etc. when unprivileged user namespaces are available.
- **systemd creates and owns the paths**: `/var/lib/nyttig`, `/run/nyttig`,
  `/etc/nyttig`. The socket moves out of `/tmp`, which fixes D8.

Accepted trade-off of loopback TCP: any local process can reach nyttig-web
without basic auth. On a single-user VPS that means only a compromised
local service, which is acceptable. nyttig-web binds `127.0.0.1` by default
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
# /etc/systemd/system/nyttig-web.service
[Unit]
Description=Nyttig web client
Requires=nyttigd.service
After=nyttigd.service

[Service]
DynamicUser=yes                       # throwaway uid; owns no files, cannot read the DB
SupplementaryGroups=nyttig            # only to connect to /run/nyttig/nyttig.sock
ExecStart=/usr/local/bin/nyttig-web --listen 127.0.0.1:7070 \
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
sudo install -m 0755 nyttigd nyttig-web nyttig /usr/local/bin/
sudo install -D -o root -g nyttig -m 0640 sample_config.toml /etc/nyttig/config.toml
#   edit /etc/nyttig/config.toml: absolute paths, your sources/tags/rules
sudo install -m 0644 deploy/systemd/nyttigd.service deploy/systemd/nyttig-web.service \
  /etc/systemd/system/
sudo usermod -aG nyttig "$USER"       # TUI access; log in again afterwards
sudo systemctl daemon-reload
sudo systemctl enable --now nyttigd nyttig-web
```

`StateDirectory=` creates `/var/lib/nyttig`, and nyttigd creates and
migrates the database on first start.

Let's Encrypt via Caddy's automatic HTTPS needs a DNS record for the host
and ports 80 and 443 reachable.

## Tooling, tests and CI

- **Development:** run `pnpm dev` in `web/`, with Vite proxying `/api` to a
  local `nyttig-web --listen 127.0.0.1:7070`, which connects to a local
  nyttigd.
- **Go tests:** `internal/web` handlers against a fake `pb.NyttigClient`
  (`httptest`), SSE framing and backpressure, and the security middleware
  (Origin, Host, headers).
- **Vitest:** stream reducer, query parser, keymap mode machine, sanitizers.
- **Playwright end-to-end:** real nyttigd with feeds from a local test HTTP
  server, plus nyttig-web.
  - Desktop and mobile viewports (Chromium device emulation).
  - Real iOS Safari is checked by hand.
- **CI:**
  - A new `web` job: pnpm setup, `pnpm install --frozen-lockfile`,
    `svelte-check`, `vitest run`, `vite build`, Playwright.
  - The committed placeholder `internal/web/dist/index.html` keeps
    `go build ./...`, Lint and Test working without Node.
  - The Build job builds the web bundle first and embeds it.

## Phases (one PR each)

0. **Groundwork (done):** codegen fix, D1–D8, CLI updates, system unit file
   in `deploy/systemd/` replacing the root `nyttigd.service` user unit, tests,
   README/AGENT.md.
1. **Feed at TUI parity:** nyttig-web (API, SSE, security middleware,
   embedding) and the SvelteKit feed: desktop and mobile layouts, keymap,
   view tracking, status bar.
2. **Management:** source, tag and rule views with forms, delete
   confirmations and the live rule preview (D6).
3. **Log-viewer features:** follow mode, row expand, query syntax + URL
   state, load older, help overlay, `:` command line, highlighting.
4. **Deployment and hardening:** system units, Caddy example, CI web job,
   Playwright end-to-end tests, docs.
