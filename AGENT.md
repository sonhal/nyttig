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
(`/tmp/nyttig.sock`), or TCP+TLS for remote daemons. The daemon pushes newly
fetched items to connected TUIs in real time via a **bidirectional gRPC stream**.

- Language: **Go 1.26+**
- TUI: **Bubble Tea** + **Lipgloss** (a custom table, not the bubbles table)
- Wire: **Protocol Buffers (proto3)** + **gRPC** (bidi streaming)
- Storage: **SQLite** with **FTS5** full-text search
- Config: **TOML**
- Logging: `slog` with JSON output to stderr

## Repository layout

```
cmd/nyttig/main.go          Client entrypoint: TUI launch + CLI subcommands
cmd/nyttigd/main.go         Daemon entrypoint: wires db → fetcher → tagger → scheduler → gRPC
proto/nyttig/v1/nyttig.proto   Source-of-truth API definition
proto/buf.yaml, buf.gen.yaml    buf config for codegen
internal/proto/nyttig/v1/   GENERATED Go from the proto (do not hand-edit)
internal/config/            TOML config loading + ~ expansion
internal/client/            gRPC client wrapper + StreamSub helper used by the TUI
internal/tui/               Bubble Tea Model, filter bar, table, status bar, view tracking
internal/server/service/    gRPC service impl + Hub (broadcasts pushed items to subscribers)
internal/server/db/         SQLite layer: items, sources, tags, views; embedded migrations
internal/server/fetcher/    Feed fetch/parse + GUID-based dedup
internal/server/tagger/     Regex-based auto-tagging engine
internal/server/scheduler/  Per-source fetch timers; refresh/enable/disable lifecycle
migrations/                 Top-level copy of init migration (DB migrations are embedded from internal/server/db/migrations)
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
- **Dedup** is by `UNIQUE(source_id, guid)`. GUID is the feed `<guid>` if
  present, else SHA-256 of `<link>`.
- **Tagging** is rule-based only (no manual tagging). Rules are regex over
  `title`/`description`/`both`, global or per-source, evaluated by `priority`.
- **View tracking** is K9s-style: the TUI marks items viewed as they scroll
  past, debounced into ~3s batches, sent via `MarkViewed`. A row exists in
  `view_state` ⟺ viewed.

## Build, test, run

```bash
go build ./...                       # build everything
go vet ./...                         # vet
go test ./...                        # run all tests
go test ./internal/server/fetcher/   # run one package's tests

go run ./cmd/nyttigd --socket /tmp/nyttig.sock --config ./sample_config.toml --log-level debug
go run ./cmd/nyttig                  # launch the TUI (daemon must be running)
go run ./cmd/nyttig list-sources     # CLI subcommand
```

Note: `go.mod` has a `replace` directive pointing `mattn/go-sqlite3` at
`./third_party/...`. Keep that vendored copy in place; builds depend on it.

### Regenerating protobuf code

The proto is the API source of truth. After editing
`proto/nyttig/v1/nyttig.proto`, regenerate with **buf** (config in `proto/`):

```bash
cd proto && buf generate
```

Output lands in `internal/proto/nyttig/v1/`. Never hand-edit the generated
`*.pb.go` / `*_grpc.pb.go` files. If you add or change an RPC, update the
service impl in `internal/server/service/` and the client wrapper in
`internal/client/` to match.

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

## Gotchas

- The repo README warns the project is LLM-generated; treat existing code as the
  spec and verify behavior with tests rather than assuming intent.
- `UpdateSource` cannot distinguish "enabled not set" from "enabled=false" in
  proto3 — see the comment in `service.go` before touching enable/disable.
- Two migration directories exist (`migrations/` and
  `internal/server/db/migrations/`); the DB applies the embedded set under
  `internal/server/db/migrations/`. Keep them in sync if you add migrations.
- Build artifacts (`bin/`, `main`, `nyttig`, `nyttigd`) and tool caches
  (`.deps/`, `.modcache/`, `.pi/`) may appear untracked in the working tree —
  do not commit them.
```
