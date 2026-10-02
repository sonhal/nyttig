# AGENT.md

Guidance for AI coding agents working in the **nyttig** repository.

**Every PR title must follow [Commits and pull requests](#commits-and-pull-requests).**
The title becomes the commit on `main`, and CI cuts releases from it.

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
- Storage: **SQLite** with **FTS5** full-text search (cgo via mattn/go-sqlite3; see the build-tag note under [Build, test, run](#build-test-run))
- Feed parsing: standard library `encoding/xml` (RSS 2.0 and Atom), with
  `golang.org/x/net/html/charset` for non-UTF-8 feeds
- Config: **TOML**
- Logging: `slog` with JSON output to stderr
- Web client: **SvelteKit 2 + Svelte 5** (runes, TypeScript strict) on
  `@sveltejs/adapter-node`; **pnpm**, Vitest, Playwright

## Commits and pull requests

PRs are squash-merged: **the PR title becomes the one commit on `main`**, and
the Tag release job reads only that subject line to decide whether to cut a
release (see [Releases](#releases)). The commits on your branch and the PR body
don't count. A title without a valid prefix releases nothing, even if the PR
ships a feature. #19 (tag hierarchy) and #21 (home screen app) both shipped
features under plain titles and didn't release.

### The title format

```
<type>(<scope>)<!>: <summary> (#<PR>)    # GitHub adds the (#<PR>) suffix
```

**Type**: what the change means to someone running nyttig. It sets the release:

| Type | Use for | Release |
|---|---|---|
| `feat` | New behavior a user can see or use: a CLI subcommand or flag, a TUI or web feature, a config key, an API endpoint or field | minor |
| `fix` | Wrong behavior made right: a bug, crash, data race, security hole | patch |
| `perf` | Same behavior, measurably faster or smaller | patch |
| `refactor` | Code restructured, no behavior change | none |
| `test` | Tests only | none |
| `docs` | README, AGENT.md, deploy guide, comments | none |
| `build` | Go modules, pnpm, build tags, Dependabot bumps (`build(deps):`) | none |
| `ci` | `.github/`, `scripts/next-version.sh` | none |
| `chore` | Anything else that ships no change to users | none |

**Scope** (optional, recommended): the part of nyttig that changes, as one
lowercase word. Use the package or area name: `db`, `fetcher`, `tagger`,
`scheduler`, `service`, `config`, `cli`, `tui`, `api`, `web`, `proto`,
`mtls`, `deploy`. Leave the scope out when the change spans several areas,
like a feature built across the stack.

**`!`**: add it after the type or scope (`feat(config)!:`) for a breaking
change, and explain the migration in a `BREAKING CHANGE: ...` paragraph in the
PR body. Here, breaking means an existing setup stops working after the
upgrade:
- a config key renamed or removed (`config.Load` rejects unknown keys)
- a CLI subcommand or flag renamed or removed
- an `/api/*` endpoint or JSON field removed or changed incompatibly
- a proto field renumbered or removed, or its type changed
- a migration that can't be reversed or needs manual steps

While the version is `0.x`, a breaking change bumps the minor version, the same
as `feat`. Mark it anyway; the `!` is what tells users to read the notes.

**Summary**: what changes for the user, not how you built it.
- Imperative mood, lowercase after the colon, no trailing period: `add`, `fix`,
  `show`, not `Added`, `Fixes`, `Showing`.
- At most about 72 characters, prefix included. Release notes and `git log
  --oneline` list titles, so they must read on their own.
- Name the behavior: `fix(fetcher): decode ISO-8859-1 feeds`, not
  `fix(fetcher): use charset.NewReaderLabel`.
- One change per title. If the title needs "and", or a list after a
  semicolon, the PR probably needs splitting. If it can't be split, title it
  by the change that matters most to users.

### Choosing the type for a mixed PR

Pick the type of the most significant change: a breaking change beats `feat`,
`feat` beats `fix`, `fix` beats `perf`, and any of those beats the
no-release types. The docs and tests that come with a feature don't change
its type. Check two things:
- A user-visible feature is `feat` even when most of the diff is a refactor.
  Don't hide it under `refactor:` or `chore:`, or it won't release.
- Internal-only work (a new helper, a test fixture, a CI job) is never `feat`.

### Examples

| Instead of | Write |
|---|---|
| `Add tag hierarchy support with parent-child relationships` | `feat: add parent tags, so filtering by a tag includes its children` |
| `Add home screen app support with web manifest and icons` | `feat(web): install the web client as a home screen app` |
| `Use upstream go-sqlite3 with the system SQLite in releases; fix six review findings` | split it: `build: link the system SQLite in release builds`, then a `fix(...)` PR per finding |
| `Skip golangci-lint on release tag runs` | `ci: skip golangci-lint on release tag runs` |
| `Ignore TypeScript major bumps in Dependabot` | `build(deps): ignore TypeScript major bumps` |
| `feat(fetcher): Added timeout.` | `fix(fetcher): time out stalled feed requests` |
| `fix(config): rename refresh_sec to refresh` | `feat(config)!: rename refresh_sec to refresh`, with a `BREAKING CHANGE:` paragraph |

### When to check the title

- **When you open the PR.** Some tools fill the title in from the branch or the
  first commit. Replace it with one written to these rules.
- **When the PR's scope changes.** If review turns a `fix` into a feature, or
  a feature into a refactor, retitle the PR before it merges.
- **Branch commits.** Write commit subjects to the same format. They only
  show up in the squash body, but they make the history readable and help you
  write the title.

Commits pushed straight to `main` (rare; prefer a PR) follow the same rules,
since the job reads them the same way.

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
                            format, tagtree) with Vitest tests next to them, plus the Svelte
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
docs/tag-tree-plan.md       Plan for parent tags (the tag tree): decisions and phases
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
  `Hub.Close` ends every active and later `StreamItems` call with
  `codes.Unavailable`; the daemon calls it before `GracefulStop`, which would
  otherwise wait out its timeout for streams that only end when the client
  leaves. Clients reconnect.
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
- **Item dates are text in UTC.** `items.published` is sorted as text, so
  every value must have one shape. The fetcher normalizes parsed dates to UTC
  with whole seconds, `db.InsertItem` does it again for any caller, and
  migration 3 rewrote older rows. The driver stores such a time as
  `YYYY-MM-DD HH:MM:SS+00:00` (not `T...Z`); a test pins the migration's
  output to what an insert writes, so keep them in step. Migration 4 adds the
  indexes ListItems relies on.
- **Fetch errors.** A failure to insert an item is reported as the source's
  `fetch_error` (the first failure plus a count), and a clean fetch clears it.
- **Removing** a source, tag or rule that does not exist is `codes.NotFound`
  (the `db.Delete*` functions return whether a row was deleted); nyttig-api
  maps it to 404.
- **Dedup** is by `UNIQUE(source_id, guid)`. GUID is the feed `<guid>` if
  present, else SHA-256 of `<link>`.
- **Search** input is wrapped by `ftsQuote` (`db/items.go`) so it is matched as
  a phrase and FTS5 query syntax in user input is neutralized. Don't pass user
  input to `MATCH` unquoted.
- **Tag tree.** A tag can have several parents (`tag_parents`, a DAG;
  `Tag.parent_ids`). It applies **when querying**: `ListItems` expands a tag
  filter to the tag and its descendants with the recursive `db.SubtreeSQL`
  (`ItemFilter.TagExact` / `SearchRequest.tag_exact` turn that off), and the
  Hub's `itemMatchesFilter` does the same walk over an in-memory snapshot of
  the edges (`service/tagtree.go`, rebuilt after every tag change; the
  daemon's seeding runs before `service.New`, which loads it). The two paths
  must agree: `TestItemMatchesFilter_AgreesWithListItems` compares them.
  `item_tags`, the tagger and item chips are unchanged: a row still means
  "a rule matched this item". Cycles are rejected in `db.SetTagParents`
  (check and write in one transaction, typed `ErrTagCycle`, mapped to
  `InvalidArgument`); the CTE uses `UNION` so it ends even if one got in.
  `UpdateTagRequest.parents` is a `TagParents` wrapper for field presence
  (unset = unchanged, empty = top-level). Config seeding adds parent edges
  in a second pass and never removes any. Any count of "items with this tag"
  that describes what a delete removes (the web confirmation) must use
  `tag_exact`.
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
gofmt -l $(git ls-files '*.go')      # must print nothing
go mod tidy -diff                    # must print nothing
go vet ./...
golangci-lint run ./...              # CI only fails on issues new in the change; see below
go test -race -shuffle=on -tags sqlite_fts5 ./...   # CI runs the race detector with random test order
govulncheck ./...                    # must report no reachable vulnerabilities

go build -tags sqlite_fts5 ./...     # build everything (the tag: see the SQLite note below)
go test -tags sqlite_fts5 ./internal/server/fetcher/   # run one package's tests

go run -tags sqlite_fts5 ./cmd/nyttigd --socket /tmp/nyttig.sock --config ./sample_config.toml --log-level debug
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

A C compiler is required: the SQLite driver (`mattn/go-sqlite3`) is cgo, and
so is the race detector. `golangci-lint` must be built with Go 1.26 or newer,
or it refuses to load the module.

**SQLite and build tags.** The schema needs SQLite's FTS5 extension, and a
build tag decides which SQLite library a binary gets:

- `-tags sqlite_fts5` compiles the driver's bundled SQLite with FTS5. Use it
  for development, `go test`, and the e2e stack (`web/e2e/stack.mjs` passes
  it). It is the only tag the bundled copy needs.
- `-tags libsqlite3` links the system's `libsqlite3` instead, so the server's
  package manager keeps SQLite patched without a rebuild. The release Build
  job uses it (and installs `libsqlite3-dev` first). Debian's and Ubuntu's
  libraries are built with FTS5.
- With neither tag the binary builds, but `db.Open` fails at startup with
  `db.ErrNoFTS5` (the bundled SQLite is then compiled without FTS5). The check
  reads `pragma_compile_options`, so it covers a system library without FTS5
  too. `go vet`, golangci-lint and govulncheck don't need either tag.

nyttigd logs the library's version at startup (`sqlite_version` in the
`database opened` line).

### CI

`.github/workflows/ci.yml` runs on pull requests, on pushes to `main`, on
`v*` tags and on `workflow_dispatch`, on `ubuntu-latest` (the Build job in a `golang:1.26-trixie`
container), with the Go version taken from `go.mod`:

| Job | What it checks |
|---|---|
| Lint | gofmt, `go mod tidy -diff`, `go vet`, golangci-lint |
| Generated code | `buf generate` leaves `internal/proto` unchanged |
| Test | `go test -race -shuffle=on -tags sqlite_fts5` with coverage |
| Web | `pnpm install --frozen-lockfile`, svelte-check, vitest, vite build, Playwright end-to-end; uploads the app build (`nyttig-web`) |
| Build | Builds the three binaries in Debian trixie (glibc and libsqlite3 of the servers, see `deploy/README.md`) with `-tags libsqlite3`, so nyttigd links the system SQLite; checks that with `ldd`; runs only after Lint, Test and Web pass; stamps a `v*` tag into `internal/version.Version` with `-ldflags -X` (other builds report the git pseudo-version); uploads `nyttig-linux-amd64` |
| Vulnerability check | `govulncheck ./...` against the code paths the binaries call |
| Tag release | Pushes to `main` only, after every other job passes: tags the commit with the version `scripts/next-version.sh` computes (see [Releases](#releases)), if any, and dispatches this workflow on the new tag to run Release |
| Release | `v*` tags only, after every other job passes: bundles the binaries, the app build, `deploy/` and `sample_config.toml` into `nyttig-<tag>-linux-amd64.tar.gz`, adds `SHA256SUMS` and a build provenance attestation, and publishes a GitHub Release (a tag with a hyphen is a pre-release) |

- golangci-lint runs with `only-new-issues: true` because the code has a
  backlog of existing findings (mostly unchecked `Close()` errors). Don't add
  new ones; fixing old ones is welcome, and once they're gone the setting
  should be removed. golangci-lint is skipped on `v*` tag runs: a new tag
  has no baseline commit, so `only-new-issues` would report the whole
  backlog and block the release. Tag commits that have been through `main`.
- The govulncheck job sets `go-version-input: ""`. Without it the action uses
  the latest stable Go instead of `go.mod`'s, so it would scan a different
  standard library from the one the binaries are built with.
- The Build job's `golang:1.26-trixie` image only supplies Debian's glibc,
  gcc and `libsqlite3-dev`; the Go version still comes from `go.mod`'s
  `toolchain` line. Change the image when the servers move to a newer Debian
  release, and never to a newer one than they run: a cgo binary needs the
  glibc (and, with `-tags libsqlite3`, the libsqlite3) it was built against
  or newer.

### Releases

Releases are cut from `main` by CI. After a push to `main` passes every job,
the Tag release job runs `scripts/next-version.sh`, which reads the
[Conventional Commits](https://www.conventionalcommits.org) subjects of the
commits since the latest stable `v*` tag and takes the largest bump:

| Commit subject | Bump |
|---|---|
| `feat: ...` / `feat(web): ...` | minor |
| `fix: ...`, `perf: ...` | patch |
| `type!: ...`, or a `BREAKING CHANGE:` footer in the body | major (minor while the version is `0.x`) |
| anything else (`docs:`, `ci:`, `chore:`, `refactor:`, a non-conventional title) | no release |

PRs are squash-merged, so **the PR title is the commit subject**; see
[Commits and pull requests](#commits-and-pull-requests) for how to write one.
`scripts/next-version.sh`
prints what the current branch would release (the per-commit reasoning goes to
stderr). Leaving `0.x` is deliberate: push `v1.0.0` by hand.

- A tag pushed with `GITHUB_TOKEN` doesn't trigger workflows, so the job
  dispatches `ci.yml` on the new tag (`workflow_dispatch` is the exception).
  That run is a normal tag run: the whole pipeline, then Release.
- A failed run on `main` doesn't lose a release: the next green push reads
  every commit since the last tag. A commit already contained in a `v*` tag
  (tagged by hand, or by a later commit's run) is skipped.

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
  the web app's npm packages (`/web`). A bump of `github.com/mattn/go-sqlite3`
  changes the bundled SQLite that `-tags sqlite_fts5` builds compile in;
  release builds (`-tags libsqlite3`) only get the Go binding. The Test job
  exercises the bundled copy, the Build job the system library.

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
- Changing a tag's name, color or parents must go through `UpdateTag`.
  `RemoveTag` cascade-deletes the tag's rules, item assignments and parent
  edges (its children are kept and, with no other parent, become top-level).
- Two migration directories exist (`migrations/` and
  `internal/server/db/migrations/`); the DB applies the embedded set under
  `internal/server/db/migrations/`. Keep them in sync if you add migrations.
- **Scheduler tests and the fake clock:** `runSource` does its immediate fetch
  *before* it creates its ticker. A test that has seen that fetch cannot assume
  the ticker exists yet, and `fakeClock.deliverAllN` only reaches tickers that
  already exist. Call `clock.waitForTickers(t, n)` before `deliverAllN`, or the
  test will be flaky.
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
