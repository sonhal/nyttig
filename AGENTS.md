# AGENTS.md

Guidance for AI coding agents working in the **nyttig** repository.
`CLAUDE.md` imports this file, so Claude Code loads it in every session, and
other tools read `AGENTS.md` by name. Read the whole file before changing code; the sections
below the first two are reference, not optional.

## Read this first

- **Build tag.** Every `go build`, `go run` and `go test` of the daemon needs
  `-tags sqlite_fts5`, or `db.Open` fails with `db.ErrNoFTS5`. See
  [Build, test, run](#build-test-run).
- **Before every push, run the CI checks locally**, exactly as listed under
  [Build, test, run](#build-test-run): gofmt, `go mod tidy -diff`, `go vet`,
  golangci-lint on the new code (see [Linting](#linting)), the race tests,
  and in `web/` `pnpm check`, `pnpm test`, `pnpm build` and the e2e suite
  when you touched the web app. CI runs the same and fails on any of them.
- **Commit subjects and PR titles are Conventional Commits.** The PR title
  becomes the squash commit on `main`, and that subject alone decides
  whether a release is cut. See
  [Commits and pull requests](#commits-and-pull-requests).
- **Don't merge.** Push the branch, make CI green, and leave merging to the
  owner unless they ask you to merge.
- **One writer per working tree.** If you delegate to a sub-agent in this
  checkout, it commits and pushes its own work; don't edit or commit in the
  tree while it runs. See [Feature work and delegation](#feature-work-and-delegation).
- **Keep the docs in step.** Behaviour changes update `README.md`, this
  file, and the feature's plan under `docs/` (its `Status:` line included).
- **Generated code is never hand-edited** (`internal/proto/`); run
  `buf generate`. Both migration directories must stay identical.
- **The cloud sandbox has quirks** (blocked hosts, a golangci-lint that
  refuses the module, `pkill -f` killing your own shell). See
  [Working in the Claude cloud sandbox](#working-in-the-claude-cloud-sandbox).

## Commits and pull requests

PRs are squash-merged, so **the PR title is the commit subject on `main`**,
and the Tag release job reads only that subject (`scripts/next-version.sh`,
see [Releases](#releases)). Use a
[Conventional Commits](https://www.conventionalcommits.org) prefix on every
commit subject and on the PR title:

| Subject prefix | Release it cuts |
|---|---|
| `feat: ...` / `feat(web): ...` | minor |
| `fix: ...`, `perf: ...` | patch |
| `feat!: ...`, or a `BREAKING CHANGE:` footer | major (minor while on `0.x`) |
| `docs:`, `ci:`, `chore:`, `build:`, `refactor:`, `test:` | none |

- A plain title such as "Add tag hierarchy support" cuts **no release**,
  even if the branch's own commits are conventional: the squash puts them in
  the commit body, which the script ignores. Two features merged this way
  on 2026-10-02 (#19 and #21) and silently skipped their minor bump.
- PRs are often created from the Claude Code UI with a title you didn't
  pick. When the session is told "A pull request was just created for this
  branch", **set the PR title yourself** to a conventional subject before
  anything else happens to it. Renaming a PR before merge is free; a missed
  release is not.
- Scope names in use: `web`, `api`, `db`, `service`, `fetcher`, `scheduler`,
  `tagger`, `tui`, `cli`, `deploy`, `ci`. Pick the one that names the part
  that changed; leave it out when the change spans the repo.
- Dependabot's `build(deps):` titles are correct as they are and never
  release.
- Write the PR body as the record of the change: what, why, deviations from
  the plan, and what was checked. The owner merges; don't merge unless asked.

## Feature work and delegation

This is the workflow the owner uses; follow it unless told otherwise.

1. **Plan before code.** For a new feature, read the code paths it touches,
   then ask the design questions that change the shape of the work, with
   options and a recommendation. Don't start implementing while the owner
   has asked only for a plan.
2. **Write the plan to `docs/<feature>-plan.md`** in the style of the
   existing plans: a `Status:` line at the top, decisions, and numbered
   phases that each fit one PR. The plan is the spec for whoever implements
   it, possibly another session, so it must stand on its own.
3. **Implement in phases**, one commit (or PR) per phase, with the tests the
   plan lists. Record deviations in the plan's "Deviations" or phase notes.
4. **Keep the `Status:` line true.** Update it when a phase lands and again
   when the PR merges; "implemented, not yet merged" must not outlive the
   merge.
5. **Delegating.** When you hand work to a sub-agent or a fresh session,
   the brief can say "read `AGENTS.md` and `docs/<feature>-plan.md`" instead
   of restating the checks and conventions; add only what is specific to
   the task (branch name, which phases, decisions already made). A
   sub-agent that works in this checkout owns the working tree until it
   returns: it runs the checks, commits per phase and pushes. Don't edit
   files or commit in that tree in parallel, and don't let a Stop hook
   push its half-finished work. If it stops without committing, run the
   checks on what it left and commit it yourself.

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

## Repository layout

```
cmd/nyttig/main.go          Client entrypoint: TUI launch + CLI subcommands (views.go: saved
                            views, assess.go: assessors and assessments)
cmd/nyttigd/main.go         Daemon entrypoint: wires db → fetcher → tagger → scheduler → gRPC
proto/nyttig/v1/nyttig.proto   Source-of-truth API definition
buf.yaml, buf.gen.yaml      buf config for codegen; run `buf generate` from the repo root
internal/proto/nyttig/v1/   GENERATED Go from the proto (do not hand-edit)
internal/config/            TOML config loading + ~ expansion
internal/since/             Parser for rolling windows (7d, 1mo): Parse + Cutoff, shared rules with web/src/lib/since.ts
internal/client/            gRPC client wrapper + StreamSub helper used by the TUI
internal/mtls/              Mutual-TLS credential loading shared by daemon and client
internal/tui/               Bubble Tea Model, filter bar, table, status bar, view tracking;
                            assess.go: score chips, the score order for live inserts, the
                            detail line
internal/api/               nyttig-api's HTTP API: routing (server.go), JSON handlers (api.go),
                            source/tag/rule management (manage.go), assessors and
                            assessments (assessments.go), SSE bridge (stream.go),
                            security middleware (security.go)
cmd/nyttig-api/main.go      nyttig-api entrypoint: flags, listen-address guard, HTTP server
web/                        The SvelteKit app (pnpm); "pnpm build" writes a Node server to web/build/
web/src/lib/                Pure modules (reducer, keymap, filter, query, command, highlight,
                            fuzzy, history, help, sanitize, viewed, forms, latest, meta, since,
                            format, tagtree, views, scores) with Vitest tests next to them, plus the
                            Svelte components (ViewTabs.svelte is the saved views' tab row above
                            the filter bar); metadata.svelte.ts holds the sources, tags and
                            saved views every page shares, prefs.svelte.ts the time format
                            (localStorage)
web/src/routes/             / is the feed; sources/, tags/, rules/, views/ and assessors/ are the
                            management pages (ManageView.svelte is their shared frame)
web/e2e/                    Playwright tests; stack.mjs starts a feed server, nyttigd, nyttig-api,
                            the app server and a Caddy-like proxy (proxy.mjs)
internal/server/service/    gRPC service impl + Hub (broadcasts pushed items to subscribers);
                            validate.go holds all client-input validation
internal/server/db/         SQLite layer: items, sources, tags, views, assessors and assessments
                            (assessments.go); embedded migrations
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
docs/saved-views-plan.md    Plan for saved views (named filters, feed tabs): decisions and phases
docs/date-filter-plan.md    Plan for the date window (since:7d) in filters and views: decisions and phases
docs/assessments-plan.md    Plan for assessments (scores and notes from external assessors): decisions and phases
docs/assessor-auth-plan.md  Plan for per-assessor tokens (Caddy basic auth, nyttig-api's assessor listener)
CLAUDE.md                   `@AGENTS.md`: makes Claude Code load this file
```

Plan documents under `docs/` carry a `Status:` line at the top that says
which phases are done and whether they are merged; keep it current.

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
  `Hub.PushUpdate` sends an item whose assessments changed (`PutAssessment`
  and `RemoveAssessment` build the full item and call it) as `item_update`
  to every subscriber, flagged with `update_matches` (the subscriber's
  `itemMatchesFilter`, including `assessmentsMatch` for `min_score` and
  `unassessed_by`); clients update an item they show, insert a matching one
  they don't, and never remove one live.
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
- **Saved views** (`saved_views`, `docs/saved-views-plan.md`) are named
  filters stored in the daemon (`ViewFilter`: search, one source, one tag,
  sort, unviewed, since, and the assessment fields) and shared by the web app and the CLI. They are **resolved
  client-side into a plain filter**: nothing in the stream, the Hub,
  `ListItems` or `itemMatchesFilter` knows about views. A deleted source or
  tag is `ON DELETE SET NULL` on the view, so the view stays and loses that
  part. Names are unique and case-insensitive; `position` orders the views
  and `ReorderSavedViews` must get exactly the full set. In the web app the
  URL stays the source of truth: `/?view=<id>&<full filter params>`, where
  `view` only says which tab is active (`filterFromParams` ignores it, and
  `setFilter`, `metadata.feedSearch` and the off-feed filter commands keep
  it); a tab shows `*` when the filter differs from the saved one
  (`views.ts`: `isModified`). The tabs are the favorites in position order,
  and only the first nine have a number key. **nyttig-api's view JSON is not
  protojson** (`viewJSON` in `internal/api/manage.go`): a view is
  `{id, name, filter: {q, source, tag, since, sort, unviewed, assessor, min_score, unassessed}, favorite, position}`
  with string IDs, zero values left out and `filter` always present (the
  daemon spells out `sort: "newest"`). The filter keys are the web `Filter`
  type's and the feed URL's, in requests and responses alike, and a PATCH
  with `filter` replaces the whole filter. Lists and the reorder answer are
  `{"views": [...]}`. In the web app `command.ts`'s old `View` is now `Page`
  (the routes); "view" always means a saved view.
- **Date window** (`since`, `docs/date-filter-plan.md`): a rolling
  duration (`24h`, `7d`, `2w`, `1mo`, `1y`; parsed by `internal/since` and
  `web/src/lib/since.ts`, which share a table of cases) and a general filter
  like `tag`: the `/` bar, the feed URL, `search -since`, and a saved view,
  which stores the **duration string** (`saved_views.since`) while the
  daemon's queries only ever see an **absolute cutoff**:
  `SearchRequest.after` / `StreamFilter.after` (`ItemFilter.After`), HTTP
  `after=<unix seconds>`. The client fixes one cutoff per snapshot
  (`FeedStream.after`, set in `connect` and, after a reconnect, when the
  daemon's reset arrives) and sends it with the stream and with every older
  page. Taking "now" per request would let the window slide between the
  snapshot and the next offset page and skip rows (the `ranked` invariant),
  so never add a `since` query parameter to the API. The date is
  `COALESCE(published, fetched_at)` compared as text against a bound with no
  `+00:00` suffix (see "Item dates are text in UTC"); the Hub's
  `itemInWindow` applies the same rule to pushed items, in whole seconds, and
  `TestItemMatchesFilter_AfterAgreesWithListItems` keeps the two in step.
  Months and years are calendar arithmetic in UTC with `AddDate`'s
  end-of-month overflow (31 March minus 1mo is 3 March), the same in Go and
  JS. Rows age out of an open list only on reload or reconnect, by design.
- **Assessments.** An assessor (`assessors`) writes at most one assessment
  per `(item, assessor, tag)` (`assessments`; the tag is optional, NULL = the
  whole item). `PutAssessment` is an upsert whose conflict target is the
  expression `IFNULL(tag_id, 0)`, matching the unique index, because a plain
  UNIQUE treats NULLs as distinct. Filters (`ItemFilter.AssessorID`,
  `MinScore`, `UnassessedBy`, `Sort: "score"`) only count assessments **in
  scope** for the filter's tag: no tag in the filter, a whole-item
  assessment, or a tag in the filter tag's subtree (the tag itself with
  `TagExact`). `min_score` or the score sort without an assessor is an error
  (`ErrAssessorRequired`). Scores of different assessors are never merged.
  Deleting an assessor deletes its assessments; saved views that use it keep
  existing (migration 8's trigger clears `min_score` and a `score` sort, the
  foreign keys clear the ids). Notes and assessor names are untrusted text.
  The service (`service/assessments.go`) validates scores (NaN and ±Inf
  explicitly, since every comparison with NaN is false), notes and the filter
  fields shared by `SearchRequest`, `StreamFilter` and `ViewFilter`
  (`validateAssessmentFilter`); an unknown item, assessor or tag is
  `NotFound`. A score is `optional double` on the wire so 0 differs from none.
  The assessment filters and the date window (`ItemFilter.After`) combine with
  AND: `itemMatchesFilter` checks `itemInWindow` first, so an `item_update`
  for an item outside the window has `update_matches = false` and no client
  inserts it (`TestAssessmentFilters_WithWindowAgree`). Migration 8 (the
  assessments) follows migration 7 (`saved_views.since`); its `saved_views`
  rebuild carries `since`. On `ViewFilter` the assessment fields are 7-9, on
  `SearchRequest` 10-12 and on `StreamFilter` 8-10 (after main's `since` /
  `after`).
- **TUI assessments.** The filter bar cycles the assessor (`a`) and the
  minimum score (`m`); the score sort exists only while an assessor is
  selected, and dropping the assessor drops the minimum and the sort
  (`dropAssessor`). `item_update` becomes `ItemUpdateMsg`; `Table.ApplyUpdate`
  replaces a shown item in place, inserts a matching one in `itemOrder` and
  never removes one. `itemOrder` and `scoreOf` (`assess.go`) mirror the
  daemon's order and scope rules; keep them in step with `db.ListItems`.
- **Rating yourself** (phase 8). `me` is an ordinary assessor that the
  *clients* create the first time they need it: `client.EnsureMe` (CLI and
  TUI, `nyttig rate`, `=` in the TUI) and `ensureMe` in `web/src/lib/rate.ts`
  (`:rate`, `=` in the web app). Both look it up by name and, on
  `AlreadyExists` / 409, look again, so two clients racing is fine. The daemon
  knows nothing special about it. Ratings are for the item as a whole.
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
  Assessor and assessment bodies (`assessments.go`) follow the same pattern;
  assessor and tag are ID strings, `/api/items` and `/api/stream` take
  `assessor`, `min_score`, `unassessed` and `sort=score`, and the SSE bridge
  sends `event: update` with `{matches, item}` for `item_update`. Notes and
  assessor names go through `safeText` (`sanitize.go`).
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
- **Fetching by view** (phase 9). `GET /api/items?view=<id or name>` and
  `/api/stream?view=` resolve a saved view in nyttig-api (`feedRequest` in
  `internal/api/api.go`), through the same functions as `nyttig search -view`
  (`internal/client/viewquery.go`: `FindView`, `ViewSearchRequest`,
  `StreamFilterOf`) so the two cannot drift. The view's `since` becomes
  `after = now - window` once per request or stream snapshot (`Config.Now`
  fixes the clock in tests); there is still no `since` HTTP parameter. A
  parameter that is present replaces the view's field and present-but-empty
  (or `0`/`false`) clears it; clearing the assessor also drops the minimum
  and a score sort. Unknown view: 404 (`client.ErrViewNotFound`).
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
- **Web assessments** (phase 6). `Filter` has `assessor`, `minScore` and
  `unassessed` next to `sort: 'score'`; `filter.ts` drops a minimum score or
  the score sort that has no assessor, as the daemon would refuse them.
  `query.ts` reads `score:<assessor>[>=0.7]`, `unassessed:<assessor>` and
  `sort:score` (an error without a `score:` term), and quotes an assessor name
  that contains `>`. `scores.ts` is the one place that decides which assessments
  count (`inScope`, `scoreOf`) and which chips a row shows. The reducer handles
  `update` events (replace in place, insert when the daemon says it matches,
  never remove) and orders by `state.score` under `sort: 'score'`, new items
  last. `a` / `A` pick and clear the assessor (`1`-`9`, `0`, `v` are saved views).
  Notes and assessor names are untrusted: render them with `htmlToText` /
  `oneLine`, never `{@html}`.
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
  `[[sources]]`, `[[tags]]`, `[[tag_rules]]` and `[[assessors]]` (seeded by
  name, never overwritten). `config.Load` rejects unknown
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

### Linting

CI runs golangci-lint with `only-new-issues: true` (see [CI](#ci)), so a
clean local run of `golangci-lint run ./...` is not the bar: the backlog
makes it fail anyway, and new findings hide in the noise. Run it the way CI
does, against the merge base:

```bash
GOBIN=$PWD/.deps/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest   # .deps/ is gitignored
.deps/bin/golangci-lint run --new-from-rev=origin/main --max-issues-per-linter=0 --max-same-issues=0 ./...
```

It must print no findings. The one that keeps coming back is `errcheck` on
`Close`: the backlog has bare `rows.Close()` calls, but a new one fails CI.
On new lines write `defer func() { _ = rows.Close() }()` or `_ = f.Close()`,
and check the error where it matters. The same goes for `fmt.Fprintf` to
stdout in the CLI (`_, _ = fmt.Fprintf(...)` or `fmt.Printf`).

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

### Working in the Claude cloud sandbox

Sessions started from the Claude Code UI run in a cloud container with an
egress allowlist. What that changes, learned the hard way:

- **golangci-lint:** the preinstalled binary was built with Go 1.25 and
  refuses this module ("the Go language version used to build golangci-lint
  is lower than the targeted Go version"). Install your own as shown under
  [Linting](#linting); the download takes about a minute.
- **govulncheck can't run:** `vuln.go.dev` is blocked, so it fails to fetch
  the database. Say so in the PR and let CI's Vulnerability check run it;
  don't bump dependencies you can't check.
- **`buf.build` is blocked.** Codegen already uses local plugins installed
  with `go install` (see [Regenerating protobuf code](#regenerating-protobuf-code)),
  so `buf generate` works offline.
- **No Docker.** The release Build job's `golang:1.26-trixie` container
  can't be reproduced here; test the bundle with a native build and let CI
  do the container build.
- **Most external sites are blocked** (feed hosts, `bsky.app`, package
  registries other than the Go proxy and npm). Use the e2e feed server
  (`web/e2e/feeds.mjs`) or hand-written fixtures instead of live feeds.
- **Playwright:** Chromium is preinstalled; run the e2e suite with
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium` and never run
  `playwright install`. A script that launches Chromium directly (not
  through `playwright.config.ts`) must pass `executablePath` itself, or it
  fails looking for the headless shell.
- **`pkill -f` kills your own shell** when the pattern matches the command
  line of the Bash call that runs it (exit code 144 with no output). Stop
  the e2e stack by killing the PIDs you started, or use a pattern that can't
  match your own command, e.g. `pkill -f '[s]tack.mjs'`.
- **The Stop hook** (`~/.claude/stop-hook-git-check.sh`) asks for every
  uncommitted change to be committed and pushed. Commit your own finished
  work; don't commit a running sub-agent's files to satisfy it.
- `.deps/`, `.modcache/` and `.pi/` are gitignored for tool installs and
  caches; use the session scratchpad for everything else.

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

PRs are squash-merged, so the PR title is the commit subject; the rules for
titling are under [Commits and pull requests](#commits-and-pull-requests).
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
  TypeScript **major** bumps in `/web` are ignored on purpose: svelte-check 4
  runs on TypeScript 7 only alongside TypeScript 6 and behind its
  experimental `--tsgo` flag, so a TypeScript 7 PR breaks `pnpm check`.
  Review a Dependabot PR by reading its CI run, not by re-deriving this.

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
- **e2e ordering:** two items pushed in the same second have no fixed order
  (`published` has whole-second precision). A test that pushes several items
  must give them unique titles and must not assume the order they arrive in.
- **Svelte 5 runes:** `$state` wraps objects in proxies, so identity
  comparisons (`panel === p`) fail; use `$state.raw` for objects that are
  replaced whole. Whitespace at the edge of an element is trimmed, so write
  `{'label '}` when a trailing space matters.
- **svelte-check covers `src/**`, tests included**, and the repo has no
  `@types/node` on purpose. A `*.test.ts` under `src/` can't use `node:fs`,
  `__dirname` or other Node APIs; `web/e2e/` is outside the checked set and
  is where Node code goes.
- **Adding a source type** (today `rss` and `atom`) touches five places,
  and missing one fails quietly: `validateFeedType` in
  `internal/server/service/validate.go`, the `--type` help in
  `cmd/nyttig/main.go`, the proto comment on `Source.type`, the `type`
  union in `web/src/lib/forms.ts` (it coerces anything that isn't `atom`
  to `rss`, so an unknown type gets rewritten on edit) and the select in
  `web/src/lib/SourceForm.svelte`. The fetcher itself picks the parser from
  the document, not from `type`.
- Build artifacts (`bin/`, `dist/`, `main`, `nyttig`, `nyttigd`, `nyttig-api`,
  and `web/build/` via `web/.gitignore`) and tool caches
  (`.deps/`, `.modcache/`, `.pi/`) are covered by `.gitignore`. Its patterns are
  anchored with a leading `/` so they don't match the `cmd/nyttig*` source
  directories; keep them that way. Don't force-add build output.
