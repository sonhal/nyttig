# AGENT.md

Guidance for AI coding agents working in the **nyttig** repository.

## What this project is

Nyttig is a news aggregator with a developer-oriented terminal UI. It follows the
Docker model — **two binaries** that talk over **gRPC**:

| Binary    | Path             | Role                                                                          |
|-----------|------------------|-------------------------------------------------------------------------------|
| `nyttigd` | `cmd/nyttigd`    | Daemon/server. Fetches RSS/Atom feeds on a schedule, tags items, serves gRPC. |
| `nyttig`  | `cmd/nyttig`     | Client. Bubble Tea TUI (no args) or CLI subcommands for headless management.  |

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

## Repository layout

```
cmd/nyttig/main.go          Client entrypoint: TUI launch + CLI subcommands
cmd/nyttigd/main.go         Daemon entrypoint: wires db → fetcher → tagger → scheduler → gRPC
proto/nyttig/v1/nyttig.proto   Source-of-truth API definition
proto/buf.yaml, buf.gen.yaml    buf config for codegen (currently broken, see below)
internal/proto/nyttig/v1/   GENERATED Go from the proto (do not hand-edit)
internal/config/            TOML config loading + ~ expansion
internal/client/            gRPC client wrapper + StreamSub helper used by the TUI
internal/mtls/              Mutual-TLS credential loading shared by daemon and client
internal/tui/               Bubble Tea Model, filter bar, table, status bar, view tracking
internal/server/service/    gRPC service impl + Hub (broadcasts pushed items to subscribers)
internal/server/db/         SQLite layer: items, sources, tags, views; embedded migrations
internal/server/fetcher/    Feed fetch/parse + GUID-based dedup
internal/server/tagger/     Regex-based auto-tagging engine
internal/server/scheduler/  Per-source fetch timers; refresh/enable/disable lifecycle
migrations/                 Copy of the migrations; the DB applies the embedded set in internal/server/db/migrations
third_party/                Vendored, patched mattn/go-sqlite3 (see note below)
scripts/gen-certs.sh        Generates a private CA plus server/client certs for mTLS
.github/workflows/ci.yml    CI pipeline (see below)
.github/dependabot.yml      Weekly grouped dependency updates
sample_config.toml          Example config
nyttigd.service             Example systemd user unit
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
  items per-subscriber against the current `StreamFilter`.
- **Adapters between layers** (e.g. `db.Source` ⇄ `scheduler.Source`,
  `db.TagRule` ⇄ `tagger.TagRule`) live in `cmd/nyttigd/main.go`. Each inner
  package defines its own store interfaces (`scheduler.SourceStore`,
  `tagger.RuleStore`) so they don't import the db package directly.
- **Fetching** uses one shared `http.Client` with a 30s timeout and caps
  response bodies at 10 MiB (`fetcher/fetch.go`). Keep both bounds when
  changing the fetcher; feeds are untrusted input.
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
```

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

`.github/workflows/ci.yml` runs on pull requests and on pushes to `main`, all
on `ubuntu-latest`, with the Go version taken from `go.mod`:

| Job | What it checks |
|---|---|
| Lint | gofmt, `go mod tidy -diff`, `go vet`, golangci-lint |
| Test | `go test -race -shuffle=on` with coverage |
| Build | Builds both binaries; runs only after Lint and Test pass |
| Vulnerability check | `govulncheck ./...` against the code paths the binaries call |

- golangci-lint runs with `only-new-issues: true` because the code has a
  backlog of existing findings (mostly unchecked `Close()` errors). Don't add
  new ones; fixing old ones is welcome, and once they're gone the setting
  should be removed.
- The govulncheck job sets `go-version-input: ""`. Without it the action uses
  the latest stable Go instead of `go.mod`'s, so it would scan a different
  standard library from the one the binaries are built with.

### Regenerating protobuf code

The proto is the API source of truth. Output lands in
`internal/proto/nyttig/v1/`. Never hand-edit the generated `*.pb.go` /
`*_grpc.pb.go` files. If you add or change an RPC, update the service impl in
`internal/server/service/` and the client wrapper in `internal/client/` to
match.

**`buf generate` does not currently work.** `proto/buf.yaml` declares
`modules: - path: proto`, which is resolved relative to `proto/` itself, so buf
looks in `proto/proto/` and fails with `Module "path: "proto"" had no .proto
files`. Changing it to `path: .` lets `cd proto && buf generate` find the file.
Generation then uses the remote plugins pinned in `buf.gen.yaml`, which needs
network access to the Buf Schema Registry. After fixing the path, `buf lint`
reports about 25 naming-rule violations in the existing API; fixing those
means breaking changes to the API, so don't do it as a drive-by. Generated
code must be gofmt-clean, and buf's output already is.

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
- **Dependabot** opens weekly grouped PRs for Go modules and GitHub Actions.
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
- `UpdateSource` cannot distinguish "enabled not set" from "enabled=false" in
  proto3 — see the comment in `service.go` before touching enable/disable.
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
- Build artifacts (`bin/`, `dist/`, `main`, `nyttig`, `nyttigd`) and tool caches
  (`.deps/`, `.modcache/`, `.pi/`) are covered by `.gitignore`. Its patterns are
  anchored with a leading `/` so they don't match the `cmd/nyttig*` source
  directories; keep them that way. Don't force-add build output.
