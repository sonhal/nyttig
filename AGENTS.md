# AGENTS.md

Rules for AI agents working in nyttig. `CLAUDE.md` imports this file.

## Must do

- Build and test the daemon with `-tags sqlite_fts5`, or `db.Open` fails with `db.ErrNoFTS5`.
- Run the [checks](#checks) before every push. CI runs the same ones.
- Commit subjects and PR titles are Conventional Commits (see [Commits](#commits-and-prs)).
- Push and get CI green. Don't merge unless the owner asks.
- Never hand-edit `internal/proto/`; run `buf generate`.
- Keep `migrations/` and `internal/server/db/migrations/` identical (the DB applies the embedded one).
- One writer per working tree: while a sub-agent works in this checkout, don't edit or commit there.

## Writing style

Applies to code comments, docs, README, plans, commit messages and PR bodies.

- Be short. Say each thing once, in the place a reader will look for it.
- Comments explain *why* or a non-obvious constraint. Don't restate the code
  or narrate what you changed.
- No session narrative: no dates, incident stories, "learned the hard way",
  or notes about what an agent tried.
- Never write details of your environment: home directories, usernames,
  hostnames, IP addresses, sandbox or scratchpad paths, tokens, e-mail
  addresses. Use placeholders (`<scratchpad>`, `example.com`, RFC 5737 IPs).
- README is for people who run and use nyttig: install, config, CLI, deploy.
  Internals belong in code comments and tests, not the README.
- Update this file only when a rule or a cross-cutting invariant changes, and
  then edit the existing entry instead of appending a new paragraph.

## Project

Docker-style daemon plus clients over gRPC.

| Binary | Role |
|---|---|
| `cmd/nyttigd` | Daemon: fetches feeds on a schedule, tags items, serves gRPC. Wiring in `main.go`. |
| `cmd/nyttig` | TUI (no args, Bubble Tea) and CLI subcommands. |
| `cmd/nyttig-api` | HTTP API (JSON + SSE) for the web app, a gRPC client of the daemon. |
| `web/` | SvelteKit 2 / Svelte 5 app on adapter-node. The proxy (Caddy) routes `/api/*` to nyttig-api, the rest to the app. |

Go 1.26 (`go.mod`), SQLite with FTS5 (cgo, mattn/go-sqlite3), proto3/gRPC,
TOML config, `slog` JSON logs. Clients connect over a Unix socket or mTLS TCP.
The proto (`proto/nyttig/v1/nyttig.proto`) is the API source of truth.

## Checks

```bash
gofmt -l $(git ls-files '*.go')                      # prints nothing
go mod tidy -diff                                    # prints nothing
go vet ./...
.deps/bin/golangci-lint run --new-from-rev=origin/main --max-issues-per-linter=0 --max-same-issues=0 ./...
go test -race -shuffle=on -tags sqlite_fts5 ./...
govulncheck ./...
```

Install golangci-lint with Go 1.26+:
`GOBIN=$PWD/.deps/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
CI only fails on new issues. The common one is `errcheck` on `Close` or
`fmt.Fprintf`: write `defer func() { _ = rows.Close() }()`.

In `web/`, when you touched the web app (Node 22+, pnpm via corepack):

```bash
pnpm install --frozen-lockfile && pnpm check && pnpm test && pnpm build && pnpm test:e2e
```

`pnpm check` must have no warnings. The e2e stack (`web/e2e/stack.mjs`)
builds and starts everything itself.

Regenerate protobuf code with the plugin versions pinned in `buf.gen.yaml`
(`go install` buf, protoc-gen-go and protoc-gen-go-grpc, then `buf generate`).
A changed RPC also needs `internal/server/service/` and `internal/client/`.

Release builds use `-tags libsqlite3` (system SQLite) instead of `sqlite_fts5`.

## Commits and PRs

PRs are squash-merged, so the PR title becomes the commit on `main`, and
`scripts/next-version.sh` reads only that subject to decide the release:

- `feat:` → minor; `fix:`, `perf:` → patch; `feat!:` or `BREAKING CHANGE:` → major (minor on 0.x).
- `docs:`, `ci:`, `chore:`, `build:`, `refactor:`, `test:`, or a non-conventional title → no release.
- Scopes: `web`, `api`, `db`, `service`, `fetcher`, `scheduler`, `tagger`, `tui`, `cli`, `deploy`, `ci`.
- When a PR is created for you, set its title to a conventional subject right away.
- PR body: what changed, why, deviations from the plan, what you checked. A few lines.

## Feature workflow

1. For a new feature, read the code it touches, then ask the design questions
   that change the shape of the work, with a recommendation. Don't implement
   while the owner has asked only for a plan.
2. Write the plan to `docs/<feature>-plan.md`: a `Status:` line, decisions,
   and numbered phases that each fit one PR. It must stand on its own.
3. Implement phase by phase with the tests the plan lists; record deviations
   in the plan and keep `Status:` true.
4. A delegated brief can say "read `AGENTS.md` and the plan" plus what is
   specific to the task. The sub-agent runs the checks, commits and pushes.

## Invariants

Cross-cutting rules that are easy to break. Details live in the code and tests named.

**Server**
- The service layer is thin: proto ⇄ db conversion plus validation
  (`service/validate.go`, every client input). Cross-subsystem logic
  (fetch → tag → push) is wired with callbacks in `cmd/nyttigd/main.go`,
  as are the adapters between packages. Config seeding writes to the db
  directly and is not validated.
- `Hub.Push` never blocks; slow subscribers drop items. `StreamItems`
  re-filters per subscriber with `itemMatchesFilter`, which must agree with
  `db.ListItems` (tag subtree, date window, assessment scope). Tests:
  `TestItemMatchesFilter_AgreesWithListItems`,
  `TestItemMatchesFilter_AfterAgreesWithListItems`,
  `TestAssessmentFilters_WithWindowAgree`.
- `item_update` updates or inserts in clients, never removes.
- Source edits reach the scheduler via `OnSourceUpdated` →
  `scheduler.RestartSource`; a running runner keeps its old `Source`.
- Fetching: one shared HTTP client, 30 s timeout, 32 MiB body cap. The
  private-address block runs in the dialer's `Control` hook on the resolved
  IP. Keep it there; URL checks are bypassable by DNS rebinding.
- Item dates are UTC text with whole seconds (`YYYY-MM-DD HH:MM:SS+00:00`)
  and sort as text. Every writer must produce that shape.
- Dedup: `UNIQUE(source_id, guid)`; GUID is `<guid>` or SHA-256 of the link.
- Search input goes through `ftsQuote`. Never pass user input to `MATCH` unquoted.
- Tag parents form a DAG, applied at query time (`db.SubtreeSQL` and
  `service/tagtree.go`). Cycles are rejected in `db.SetTagParents`.
- Assessment filters count only assessments in scope for the filter's tag.
  `min_score` or the score sort without an assessor is an error. The upsert
  conflicts on `IFNULL(tag_id, 0)`.
- The date window: clients store a duration (`since`) but send the daemon an
  absolute cutoff (`after`), fixed once per snapshot. Never add a `since`
  parameter to the HTTP API.
- Saved views resolve client-side into a plain filter; the stream, Hub and
  `ListItems` know nothing about views. `ViewFilter.columns` is display only.
  `update-view` in the CLI copies filter fields one by one, so a new field
  must be added there.
- Digests are separate from assessments and the item stream (no live push).
  Periods are written from Go as UTC whole seconds.
- Update RPCs use proto3 `optional` or wrapper messages for field presence
  (unset = unchanged).
- Removing something that doesn't exist is `codes.NotFound` (404 in nyttig-api).

**Source types** (`rss`, `atom`, `bluesky`, `euvd`). A new type touches
`validate.go`, the CLI's `--type` help, the proto comments, `scheduler.Source.Type`,
`web/src/lib/forms.ts` (`sourceForm` coerces unknown types to `rss`),
`SourceForm.svelte`, the README tables, and the fetcher. For `rss`/`atom`
the parser is picked from the document (`feedKind`), so a new feed format is
a `feedKind` change, not a new type.

**nyttig-api and web**
- nyttig-api depends only on `pb.NyttigClient`; tests use `fake_test.go`.
  JSON is protojson with proto names (int64 IDs as strings), except saved
  views (`viewJSON`). Management bodies keep field presence and reject
  unknown, duplicate and `null` fields.
- Feed content, notes, assessor names and digest bodies are untrusted.
  Never `{@html}` (`policy.test.ts` enforces it); use `htmlToText`,
  `safeLink`, `safeColor`; no feed images. Digest Markdown is parsed into a
  typed tree, and its parsers (web and TUI) must stay linear on hostile input.
- Rule regexes are Go RE2 and never run in the browser; previews call
  `POST /api/rules/test`.
- In the web app the URL is the state. Keys live in `keymap.ts` (the help
  overlay reads it). Keep the `ranked` invariant in `reducer.ts`.
- Go never serves the app and Node never serves `/api`.

**Containers.** nyttigd serves plaintext gRPC only on the Unix socket in the
`nyttig-socket` volume; no network carries it. Images build nyttigd with
`-tags libsqlite3` and the clients with `CGO_ENABLED=0`. New build inputs
outside `cmd/`, `internal/` and `web/` go in `.dockerignore`'s allowlist.

**Config.** `config.Load` rejects unknown keys. Changing the config structs
means updating `sample_config.toml` (a test loads it) and the README.

## Conventions

- Standard Go style; keep `// ── Foo ──` banners in files that use them.
- Wrap errors with `fmt.Errorf("...: %w", err)`; gRPC handlers return `status.Errorf`.
- Server logs via `slog`. CLI output to stdout, errors to stderr, `os.Exit(1)` on failure.
- Table-driven tests next to the code. Add tests for fetcher, tagger,
  scheduler and db changes.
- The code was LLM-generated: treat it and its tests as the spec, and verify
  README examples against the code.

## Gotchas

- Scheduler tests: call `clock.waitForTickers(t, n)` before `deliverAllN`.
- e2e: one daemon serves both viewports, so tests create and delete their own
  sources, tags and rules. Items pushed in the same second have no fixed order.
- Svelte 5: `$state` proxies break identity comparisons; use `$state.raw`
  for objects replaced whole.
- svelte-check covers `src/**` including tests, with no `@types/node`; Node
  code goes in `web/e2e/`.
- TypeScript major bumps in `web/` are ignored on purpose (svelte-check).
- Before bumping a Go dependency, run `govulncheck`; the latest release isn't
  always the patched one.
- `.gitignore` patterns are anchored with `/` so they don't match `cmd/nyttig*`.

## Cloud sandbox

- The preinstalled golangci-lint is too old; install your own (above).
- `govulncheck` can't reach its database; say so in the PR and let CI run it.
- Most external hosts are blocked. Use the e2e feed server (`web/e2e/feeds.mjs`)
  or fixtures instead of live feeds.
- Docker image builds can't reach Debian's mirror; rely on CI's Images job.
- Playwright: point `PLAYWRIGHT_CHROMIUM_EXECUTABLE` at the preinstalled
  Chromium; never run `playwright install`.
- `pkill -f` can kill your own shell; kill PIDs, or use a pattern like `'[s]tack.mjs'`.
- The Stop hook wants everything committed. Don't commit a running sub-agent's files.
