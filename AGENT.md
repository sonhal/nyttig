# AGENT.md

Guidance for AI coding agents working in the **nyttig** repository.

## What this project is

Nyttig is a news aggregator with a developer-oriented terminal UI. It follows the
Docker model — a daemon and clients that talk over **gRPC**:

| Binary       | Path             | Role                                                                          |
|--------------|------------------|-------------------------------------------------------------------------------|
| `nyttigd`    | `cmd/nyttigd`    | Daemon/server. Fetches RSS/Atom feeds on a schedule, tags items, serves gRPC. |
| `nyttig`     | `cmd/nyttig`     | Client. Bubble Tea TUI (no args) or CLI subcommands for headless management.  |
| `nyttig-api` | `cmd/nyttig-api` | Client. The web app's API (JSON + SSE over the gRPC API), behind a proxy.     |

The web app itself (`web/`) is a SvelteKit app served by its own Node
server; Caddy routes `/api/*` to nyttig-api and everything else to it.

The client connects to the daemon over a **Unix domain socket** by default
(`/tmp/nyttig.sock`), or over TCP protected by **mutual TLS** for remote
daemons. The daemon pushes newly fetched items to connected TUIs in real time
via a **bidirectional gRPC stream**.

- Language: **Go 1.26+** (`go.mod` sets `go 1.26.3` as the minimum and
  `toolchain go1.26.8` as the version to build with; see [Dependencies](#dependencies))
- TUI: **Bubble Tea** + **Lipgloss** (a custom table, not the bubbles table)
- Wire: **Protocol Buffers (proto3)** + **gRPC** (bidi streaming)
- Storage: **SQLite** with **FTS5** full-text search (cgo; see the vendored driver note below)
- Feed parsing: standard library `encoding/xml` (RSS 2.0 and Atom)
- Config: **TOML**
- Logging: `slog` with JSON output to stderr
- Web client: **SvelteKit 2 + Svelte 5** (runes, TypeScript strict) on
  `@sveltejs/adapter-node`; **pnpm**, Vitest, Playwright

## Repository layout

```
cmd/nyttig/main.go          Client entrypoint: TUI launch + CLI subcommands
cmd/nyttigd/main.go         Daemon entrypoint: wires db → fetcher → tagger → scheduler → gRPC
proto/nyttig/v1/nyttig.proto   Source-of-truth API definition
buf.yaml, buf.gen.yaml      buf config for codegen; run `buf generate` from the repo root
internal/proto/nyttig/v1/   GENERATED Go from the proto (do not hand-edit)
internal/config/            TOML config loading + ~ expansion
internal/client/            gRPC client wrapper + StreamSub helper used by the TUI
internal/mtls/              Mutual-TLS credential loading shared by daemon and client
internal/tui/               Bubble Tea Model, filter bar, table, status bar, view tracking
internal/api/               nyttig-api's HTTP API: routing (server.go), JSON handlers (api.go),
                            source/tag/rule management (manage.go), SSE bridge (stream.go),
                            security middleware (security.go)
cmd/nyttig-api/main.go      nyttig-api entrypoint: flags, listen-address guard, HTTP server
web/                        The SvelteKit app (pnpm); "pnpm build" writes a Node server to web/build/
web/src/lib/                Pure modules (reducer, keymap, filter, query, command, highlight,
                            fuzzy, history, help, sanitize, viewed, forms, latest, meta,
                            format) with Vitest tests next to them, plus the Svelte
                            components; metadata.svelte.ts holds the sources and tags every
                            view shares, prefs.svelte.ts the time format (localStorage)
web/src/routes/             / is the feed; sources/, tags/ and rules/ are the management views
                            (ManageView.svelte is their shared frame)
web/e2e/                    Playwright tests; stack.mjs starts a feed server, nyttigd, nyttig-api,
                            the app server and a Caddy-like proxy (proxy.mjs)
internal/server/service/    gRPC service impl + Hub (broadcasts pushed items to subscribers);
                            validate.go holds all client-input validation
internal/server/db/         SQLite layer: items, sources, tags, views; embedded migrations
internal/server/fetcher/    Feed fetch/parse + GUID-based dedup; client.go builds the HTTP
                            client, including the private-address (SSRF) block
internal/server/tagger/     Regex-based auto-tagging engine
internal/server/scheduler/  Per-source fetch timers; refresh/enable/disable lifecycle
migrations/                 Copy of the migrations; the DB applies the embedded set in internal/server/db/migrations
third_party/                Vendored, patched mattn/go-sqlite3 (see note below)
scripts/gen-certs.sh        Generates a private CA plus server/client certs for mTLS
.github/workflows/ci.yml    CI pipeline (see below)
.github/dependabot.yml      Weekly grouped dependency updates
sample_config.toml          Example config (loaded by a test, so keep it valid)
deploy/systemd/             Hardened system units; nyttigd runs as a dedicated `nyttig` user,
                            nyttig-api as a DynamicUser in the `nyttig` group, nyttig-web
                            (the app's Node server) as a DynamicUser
deploy/README.md            VPS guide: sizing, build for Debian, mTLS for the TUI, backups, upgrades
deploy/Caddyfile            Example reverse proxy (TLS, basic auth, /api/* vs the app)
docs/web-client-plan.md     Plan for the nyttig-api browser client (phases and decisions)
```

## Architecture notes (read before changing server code)

- **Daemon wiring lives in `cmd/nyttigd/main.go`.** The service layer
  (`internal/server/service`) is intentionally thin — it converts proto ⇄ db
  types and delegates to `internal/server/db`. Business logic that crosses
  subsystems (fetch → tag → push) is wired via callbacks in `main.go`, not
  inside the service. The service exposes setters/registration hooks
  (`SetRefreshSourceFunc`, `OnSourceAdded/Removed/Updated`, `Hub()`) that the
  daemon entrypoint fills in.
- **The Hub** (`service.go`) holds active `StreamItems` subscribers. The fetch
  pipeline calls `hub.Push(item)`; `Push` is **non-blocking** and drops items
  for slow subscribers (64-buffered channel). `StreamItems` re-filters pushed
  items per-subscriber against the current `StreamFilter`, including the
  search query (checked against `items_fts` with `db.ItemMatchesSearch`).
- **Adapters between layers** (e.g. `db.Source` ⇄ `scheduler.Source`,
  `db.TagRule` ⇄ `tagger.TagRule`) live in `cmd/nyttigd/main.go`. Each inner
  package defines its own store interfaces (`scheduler.SourceStore`,
  `tagger.RuleStore`) so they don't import the db package directly.
- **Fetching** uses one shared `http.Client` from `fetcher.NewHTTPClient`
  with a 30s timeout, and caps response bodies at 10 MiB (`fetcher/fetch.go`).
  Keep both bounds when changing the fetcher; feeds are untrusted input.
  With `block_private_addresses` the client refuses non-public destinations
  in the dialer's `Control` hook, i.e. on the resolved IP at connect time.
  Keep the check there: validating the URL or hostname beforehand is
  bypassable with DNS rebinding and redirects.
- **Validation** of everything a client sends lives in
  `service/validate.go` and runs in the service, so every client gets it.
  Config seeding in `cmd/nyttigd/main.go` writes to the db directly and is
  not validated (the config file is trusted).
- **Source edits reach the scheduler** through `OnSourceUpdated`, which the
  service calls only when `enabled`, `url` or `refresh_sec` changed. The daemon
  then calls `scheduler.RestartSource`: a running runner keeps the `Source` it
  was started with, so `EnableSource` alone would keep the old URL/interval.
- **Listeners** (`planListeners` in `cmd/nyttigd/main.go`): one gRPC
  server per listener, all serving the same service. By default there is
  one, `socket`, with mTLS if `[tls]` is set. With `[tls] listen` the
  daemon also serves mTLS on that TCP address and keeps `socket` as a
  plaintext Unix socket (for nyttig-api); a TCP `socket` is then refused so
  no plaintext port opens by accident.
- **Dedup** is by `UNIQUE(source_id, guid)`. GUID is the feed `<guid>` if
  present, else SHA-256 of `<link>`.
- **Search** input is wrapped by `ftsQuote` (`db/items.go`) so it is matched as
  a phrase and FTS5 query syntax in user input is neutralized. Don't pass user
  input to `MATCH` unquoted.
- **Tagging** is rule-based only (no manual tagging). Rules are regex over
  `title`/`description`/`both`, global or per-source, evaluated by `priority`.
- **View tracking** is K9s-style: the TUI marks items viewed as they scroll
  past, debounced into ~3s batches, sent via `MarkViewed`. A row exists in
  `view_state` ⟺ viewed.
- **nyttig-api** (`internal/api`) depends only on the generated
  `pb.NyttigClient` interface; `cmd/nyttig-api` builds it from
  `client.DialConn`. Handler tests use a fake client (`fake_test.go`).
  JSON is `protojson` with `UseProtoNames`, so int64 IDs are strings and
  zero values are left out; the TypeScript types in `web/src/lib/types.ts`
  mirror that. `/api/stream` opens one `StreamItems` per SSE connection and
  never changes its filter: the browser reconnects instead.
  Management bodies (`manage.go`) are read member by member into a
  `jsonBody`, which keeps field presence for PATCH (absent = unchanged,
  mapped to the proto3 `optional` fields) and rejects unknown, duplicate
  and `null` fields. Value validation is left to the daemon
  (`service/validate.go`); the API only checks shapes and IDs. A missing
  `enabled` on `POST /api/sources` means true, since proto3 defaults
  `AddSourceRequest.enabled` to false. The bridge
  sends its own first `reset` (the daemon only sends one on filter
  changes), and closes the connection when its bounded queue overflows so
  the browser resyncs. Items and colors are sanitized on the way out
  (`sanitize.go`), and again in the browser.
- **Web log-viewer features** (phase 3). `keymap.ts` holds every mode's keys
  as binding tables; the key handler and the help overlay (`help.ts`) both
  read them, so add a key there and it shows in `?`. `query.ts` parses and
  formats the `/` bar's syntax (`tag:` `src:` `is:unviewed` `sort:`) against
  the current sources and tags; the URL stays separate parameters with IDs
  (`filter.ts`), and a `q` in the URL is always plain text. `command.ts` is the
  pure `:` command parser (names resolved to IDs, completion); `commandline.svelte.ts`
  runs commands for both the feed and the management views. `reducer.ts` also
  does follow mode (`follow`, `pending`) and load older: `ranked` is the number
  of database rows the list covers from the top and is the next page's
  `offset`. Keep the invariant in its header comment (count too few rows,
  never too many, or a page skips rows). A reset drops older pages and
  `epoch` discards requests from before it. `highlight.ts` splits text into
  segments that are rendered as text nodes and `<mark>`; never build HTML.
  `policy.test.ts` fails on `{@html}`. The e2e feed server has bulk feeds
  (`/bulk/<name>.xml?n=230`, push with `/add?feed=bulk/<name>`) for the tests
  that need long lists; they add their own source and delete it.
- **Feed content in the browser is untrusted.** Never use `{@html}`; render
  text only (`htmlToText` in `web/src/lib/sanitize.ts`), links through
  `safeLink`, colors through `safeColor`, and don't load feed images. The
  CSP (`kit.csp` in `web/svelte.config.js`) forbids inline scripts except
  SvelteKit's bootstrap, which gets a per-request nonce; other page headers
  are set in `web/src/hooks.server.ts`. The feed renders client-side
  (`ssr = false`), so the Node server only sends the app shell.
- **Rule patterns are never evaluated in the browser.** Go RE2 syntax
  (`(?i)`, `(?P<name>…)`) differs from JavaScript's; the rule editor's
  preview calls `POST /api/rules/test` (TestTagRule) instead. There is no
  UpdateTagRule: the rules view edits by adding the new rule, then
  removing the old one.
- **Config** (`internal/config`): `socket`, `db_path` and `log_level` are
  top-level keys (there is no `[server]` table), followed by `[tls]`,
  `[[sources]]`, `[[tags]]` and `[[tag_rules]]`. `config.Load` rejects unknown
  keys. The daemon reads a file only when `--config` is passed (no default
  path), and flags override file values. If you change the config structs,
  update `sample_config.toml` and the README's example and reference tables.

## Build, test, run

These are the checks CI runs; run them before pushing. The Lint and Test jobs
fail on any of them.

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^third_party/')   # must print nothing
go mod tidy -diff                    # must print nothing
go vet ./...
golangci-lint run ./...              # CI only fails on issues new in the change; see below
go test -race -shuffle=on ./...      # CI runs the race detector with random test order
govulncheck ./...                    # must report no reachable vulnerabilities

go build ./...                       # build everything
go test ./internal/server/fetcher/   # run one package's tests

go run ./cmd/nyttigd --socket /tmp/nyttig.sock --config ./sample_config.toml --log-level debug
go run ./cmd/nyttig                  # launch the TUI (daemon must be running)
go run ./cmd/nyttig list-sources     # CLI subcommand
go run ./cmd/nyttig-api --origin http://localhost:5173   # web API, for "pnpm dev" in web/ (daemon must be running)
```

### Web app (`web/`)

Node.js 22+ and pnpm (the version is pinned by `packageManager` in
`web/package.json`; `corepack enable` picks it up). Run these in `web/`;
CI's Web job runs the same:

```bash
pnpm install --frozen-lockfile
pnpm check                           # svelte-kit sync + svelte-check (TypeScript), no warnings
pnpm test                            # vitest: reducer, keymap, filter, sanitizers, view tracker, forms
pnpm build                           # vite build; writes build/, run it with "node build"
pnpm test:e2e                        # Playwright; needs "pnpm build" first
pnpm dev                             # Vite dev server on :5173, /api proxied to 127.0.0.1:7070
```

- **Two services:** the Go side never serves the app and the Node side
  never serves `/api`; routing is the proxy's job (Caddy in production,
  Vite's dev proxy in `pnpm dev`, `e2e/proxy.mjs` in the tests). The app
  build has no runtime `dependencies`, so `web/build/` plus
  `web/package.json` is the whole deployable.
- **`pnpm dev`:** start nyttig-api with `--origin http://localhost:5173`;
  the Vite proxy keeps the browser's Host and Origin, which nyttig-api checks.
- **End-to-end tests** (`web/e2e/`): `stack.mjs` builds nyttigd and
  nyttig-api (or uses `NYTTIG_BIN_DIR`), starts a local RSS server
  (`feeds.mjs`), both binaries on a temporary database, the app's Node
  server, and a proxy on 7812 that routes like the Caddyfile.
  They run at a desktop and a mobile (Pixel 7) viewport. Where
  `playwright install` can't download browsers, point
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE` at an installed Chromium, e.g.
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium pnpm test:e2e`.
- **Dependencies** are kept to the list in `web/package.json` (no CSS
  framework, component library or font CDN). pnpm 10 runs no dependency
  install scripts unless listed in `onlyBuiltDependencies`
  (`web/pnpm-workspace.yaml`), which also sets `minimumReleaseAge`.

A C compiler is required: SQLite is built with cgo, and so is the race
detector. `golangci-lint` must be built with Go 1.26 or newer, or it refuses to
load the module.

Note: `go.mod` has a `replace` directive pointing `mattn/go-sqlite3` at
`./third_party/...`. Keep that vendored copy in place; builds depend on it.
It carries one local addition, `sqlite3_opt_fts5_default.go`, which enables
FTS5 without the `sqlite_fts5` build tag. The schema needs FTS5, so keep that
file if you ever update the vendored driver. The `replace` directive is also
why `go install github.com/sonhal/nyttig/cmd/...@latest` doesn't work.

### CI

`.github/workflows/ci.yml` runs on pull requests, on pushes to `main` and on
`v*` tags, on `ubuntu-latest` (the Build job in a `golang:1.26-trixie`
container), with the Go version taken from `go.mod`:

| Job | What it checks |
|---|---|
| Lint | gofmt, `go mod tidy -diff`, `go vet`, golangci-lint |
| Generated code | `buf generate` leaves `internal/proto` unchanged |
| Test | `go test -race -shuffle=on` with coverage |
| Web | `pnpm install --frozen-lockfile`, svelte-check, vitest, vite build, Playwright end-to-end; uploads the app build (`nyttig-web`) |
| Build | Builds the three binaries in Debian trixie (glibc of the servers, see `deploy/README.md`); runs only after Lint, Test and Web pass; uploads `nyttig-linux-amd64` |
| Vulnerability check | `govulncheck ./...` against the code paths the binaries call |
| Release | `v*` tags only, after every other job passes: bundles the binaries, the app build, `deploy/` and `sample_config.toml` into `nyttig-<tag>-linux-amd64.tar.gz`, adds `SHA256SUMS` and a build provenance attestation, and publishes a GitHub Release (a tag with a hyphen is a pre-release) |

- golangci-lint runs with `only-new-issues: true` because the code has a
  backlog of existing findings (mostly unchecked `Close()` errors). Don't add
  new ones; fixing old ones is welcome, and once they're gone the setting
  should be removed.
- The govulncheck job sets `go-version-input: ""`. Without it the action uses
  the latest stable Go instead of `go.mod`'s, so it would scan a different
  standard library from the one the binaries are built with.
- The Build job's `golang:1.26-trixie` image only supplies Debian's glibc and
  gcc; the Go version still comes from `go.mod`'s `toolchain` line. Change
  the image when the servers move to a newer Debian release, and never to a
  newer one than they run: a cgo binary needs the glibc it was built against
  or newer.

### Regenerating protobuf code

The proto is the API source of truth. Output lands in
`internal/proto/nyttig/v1/`. Never hand-edit the generated `*.pb.go` /
`*_grpc.pb.go` files. If you add or change an RPC, update the service impl in
`internal/server/service/` and the client wrapper in `internal/client/` to
match.

Generate from the repository root with local, pinned plugins (no Buf
Schema Registry access needed):

```bash
go install github.com/bufbuild/buf/cmd/buf@v1.73.0
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
buf generate
```

CI's **Generated code** job runs the same commands and fails if
`internal/proto` changes. That guards against a real past bug: fields were
added to the Go structs by hand, but protobuf-go serializes from the
descriptor embedded in the generated file, so `Source.color`/`abbreviation`
were silently dropped on the wire. `service_test.go`'s
`TestSourceColorSurvivesWire` checks this too. When bumping a plugin version,
change it in `buf.gen.yaml`'s comment, the CI job and here.

Managed mode is off on purpose: the proto's own `go_package` sets the Go
package (`v1`). `buf lint` reports about 25 naming-rule violations in the
existing API; fixing those means breaking API changes, so don't do it as a
drive-by.

Fields that clients patch use proto3 `optional` (field presence), e.g.
`UpdateSourceRequest` and `UpdateTagRequest`: unset = unchanged. Use the same
pattern for new update RPCs rather than treating zero values as "unset".

## Dependencies

- **Go toolchain:** the `go` line in `go.mod` is the minimum version users need;
  the `toolchain` line is what CI and local builds use (the `go` command
  downloads it automatically). When a new 1.26.x patch release comes out,
  bump the `toolchain` line; they usually carry security fixes. Only raise the
  `go` line when the code needs a newer language or standard-library feature.
- **Before bumping a dependency, run `govulncheck ./...`.** The newest release
  isn't always the patched one. For example, gRPC v1.84.0 was still affected by
  GO-2026-6443, while v1.83.2 had the fix. Read the advisory's affected ranges
  (on pkg.go.dev/vuln) rather than assuming "latest is safe".
- **gRPC** is reachable from the network in `nyttigd`, so its advisories
  matter. Keep it on a release govulncheck reports clean.
- **Dependabot** opens weekly grouped PRs for Go modules, GitHub Actions and
  the web app's npm packages (`/web`).
  It ignores `github.com/mattn/go-sqlite3`, because the `replace` directive means
  a version bump would change nothing that gets built. Update the vendored copy
  in `third_party/` by hand instead, keeping the FTS5 file.

## Conventions

- Standard Go style; keep the existing section-banner comment style
  (`// ── Foo ──`) when adding to files that already use it.
- Errors: wrap with `fmt.Errorf("context: %w", err)` internally; gRPC handlers
  return `status.Errorf(codes.X, ...)`.
- Logging is `slog` (structured key/value), JSON to stderr. No `fmt.Println`
  for server logs.
- CLI subcommands print user-facing output to stdout, errors to stderr, and
  `os.Exit(1)` on failure.
- Tests use the standard `testing` package and table-driven style; place them
  next to the code as `*_test.go`. Add a test when changing fetcher, tagger,
  scheduler, or db logic.
- Keep README.md in sync with behavior changes (flags, config keys, CLI
  subcommands). It has drifted before; verify its examples against the code
  rather than trusting them.

## Gotchas

- The repo README warns the project is LLM-generated; treat existing code as the
  spec and verify behavior with tests rather than assuming intent.
- Changing a tag's name or color must go through `UpdateTag`. `RemoveTag`
  cascade-deletes the tag's rules and item assignments.
- Two migration directories exist (`migrations/` and
  `internal/server/db/migrations/`); the DB applies the embedded set under
  `internal/server/db/migrations/`. Keep them in sync if you add migrations.
- **Scheduler tests and the fake clock:** `runSource` does its immediate fetch
  *before* it creates its ticker. A test that has seen that fetch cannot assume
  the ticker exists yet, and `fakeClock.deliverAllN` only reaches tickers that
  already exist. Call `clock.waitForTickers(t, n)` before `deliverAllN`, or the
  test will be flaky.
- A few comments in `fetcher/fetch_test.go` still mention gofeed; the project
  doesn't use it (parsing is `encoding/xml`).
- The end-to-end tests share one daemon across both viewports, so the
  management tests (`e2e/manage.spec.ts`) create their own sources, tags
  and rules (named after the Playwright project) and delete them again,
  and restore anything shared they change. New sources can point at
  `/extra/<name>.xml` on the feed server, which serves two items per name.
- The feed server in `web/e2e/feeds.mjs` escapes some descriptions twice on
  purpose: nyttigd strips tags and then unescapes entities, and that is how
  real markup reaches the browser.
- Build artifacts (`bin/`, `dist/`, `main`, `nyttig`, `nyttigd`, `nyttig-api`,
  and `web/build/` via `web/.gitignore`) and tool caches
  (`.deps/`, `.modcache/`, `.pi/`) are covered by `.gitignore`. Its patterns are
  anchored with a leading `/` so they don't match the `cmd/nyttig*` source
  directories; keep them that way. Don't force-add build output.
