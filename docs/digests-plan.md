# Digests plan: summaries and notes from assessors, kept as a history

Status: **phases 1–3 implemented, not yet merged.** Phases 1–7 below.

Assessments judge one item. A **digest** is a document an assessor writes
about **one to many items**: "today's CVE news", "September in review".
Digests are kept as a history, grouped in **series**, and an assessor can
read its earlier digests as input to the next one (a monthly digest built
from thirty dailies instead of three thousand items).

```
assessor claude
  ├─ series daily-cve
  │    ├─ digest #41  2026-10-04
  │    └─ digest #42  2026-10-05   (input: #41)
  └─ series monthly
       └─ digest #43  2026-09      (inputs: #12 … #40)
```

Like assessments, digests are written by programs outside the daemon (see
[Writing a digest](#writing-a-digest)). nyttig stores, links and shows them;
it never calls a model.

## Decisions

| Topic | Decision |
|---|---|
| Name | **Digest**: one document by one assessor. **Series**: a named, registered group of digests (`daily-cve`, `monthly`). "Note" stays the text of an assessment; "view" and "source" are taken |
| Separate from assessments | Yes. Assessments are one item, one key, latest value only; digests are many items and append-only. Nothing in `assessments`, `ItemFilter`, the Hub or the item stream changes |
| Series | **Required and registered** (`digest_series`): every digest belongs to exactly one series. A series belongs to **one assessor** (`claude/daily-cve` and `gpt/daily-cve` are different series), has a description and a position, and is what you navigate to and filter on, like a saved view |
| Series names | Unique per assessor, case-insensitive (`COLLATE NOCASE`), `validateName` rules (≤ 64 chars) |
| Creating a series | Explicitly (`AddDigestSeries`), or by the assessor program on first use with `client.EnsureDigestSeries`, the same find / create / find-again pattern as `client.EnsureMe` |
| Period | Every digest has `period_start` and `period_end`, absolute UTC with whole seconds, `period_end >= period_start`. "Today" and "this month" are the assessor's business; the daemon stores what was covered |
| Writes | `AddDigest` always creates a new digest. Re-running for the same period is just another digest. `UpdateDigest(id, …)` edits one in place and **overwrites** (no revisions); `updated_at` says when |
| History | Every digest is kept until it, its series or its assessor is deleted. "History" means the series of digests, not drafts of one |
| Links | A digest links to the **items** it is based on (`digest_items`) and to the **earlier digests** it used as input (`digest_inputs`). Links are provenance only: nothing walks them recursively, and a cycle created by an update is harmless |
| Standalone text | The body must make sense without its links. Items are only deleted with their source (`items.source_id ON DELETE CASCADE`; nyttig has no retention), which removes the link rows but never the digest |
| Format | **A Markdown subset** (phase 6): headings, paragraphs, lists, emphasis, inline code, code blocks, quotes, links, and `[#123]` item references. Rendered to text nodes, never to HTML; raw HTML in the body is shown as text. Until phase 6 the web shows the body as plain text |
| Size | Body ≤ 64 KiB (UTF-8 bytes), title ≤ 200 chars, ≤ 1000 linked items and ≤ 100 inputs per digest |
| Clients | **Web app and TUI** for reading; the CLI gets management and writing commands (for scripts, testing assessors, and parity with assessments) |
| Live updates | **None in v1.** Clients load digests when opened (the web's metadata timer may refresh the series list). No changes to `StreamItems` |
| Deleting | Deleting an assessor deletes its series and their digests; deleting a series deletes its digests; deleting a digest removes it from other digests' inputs. The web confirmation says how many digests go |

## Data model: migration `000009_digests` (both migration directories)

```sql
CREATE TABLE digest_series (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    assessor_id INTEGER NOT NULL REFERENCES assessors(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL COLLATE NOCASE,
    description TEXT,
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (assessor_id, name)
);

CREATE TABLE digests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id    INTEGER NOT NULL REFERENCES digest_series(id) ON DELETE CASCADE,
    title        TEXT    NOT NULL,
    body         TEXT    NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    CHECK (period_end >= period_start)
);
-- A series' history, newest period first.
CREATE INDEX idx_digests_series_period ON digests (series_id, period_end DESC, id DESC);

CREATE TABLE digest_items (
    digest_id INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL REFERENCES items(id)   ON DELETE CASCADE,
    PRIMARY KEY (digest_id, item_id)
);
-- ON DELETE CASCADE from items looks rows up by item_id.
CREATE INDEX idx_digest_items_item_id ON digest_items (item_id);

CREATE TABLE digest_inputs (
    digest_id INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    input_id  INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    PRIMARY KEY (digest_id, input_id),
    CHECK (digest_id <> input_id)
);
CREATE INDEX idx_digest_inputs_input_id ON digest_inputs (input_id);
```

- **Dates** (`period_*`, `created_at`, `updated_at`) are written by the db
  layer in Go as UTC with whole seconds, the same text shape as
  `items.published` (AGENTS.md, "Item dates are text in UTC"), so that
  ordering by text is ordering by time. Don't rely on `CURRENT_TIMESTAMP`
  for `digests`; its shape differs.
- **Position** orders series in the UI across all assessors (like
  `saved_views.position`); a new series goes last.
- The down migration drops the four tables (inputs, items, digests, series).

New `internal/server/db/digests.go`:

- series: `InsertDigestSeries`, `GetDigestSeries`, `ListDigestSeries(assessorID)`
  (0 = all; ordered by position, then id; each row carries `DigestCount` and
  `LatestPeriodEnd` for navigation), `UpdateDigestSeries`,
  `DeleteDigestSeries` (returns whether a row was deleted),
  `ReorderDigestSeries(ids)` (exactly the full set, in one transaction, like
  `ReorderSavedViews`)
- digests: `InsertDigest` (digest row plus its links in one transaction),
  `GetDigest`, `ListDigests(seriesID, beforeID, limit, withBody)`,
  `UpdateDigest` (fields and link sets, in one transaction),
  `DeleteDigest` (returns bool), `CountDigestsByAssessor`
- `GetDigest` and `ListDigests` fill the linked items (id, title, link,
  source name, published) and the inputs (id, title, series name, period
  end) in batches, like `loadTagsByItemIDs`.
- **Paging:** `ListDigests` orders by `period_end DESC, id DESC`. `beforeID`
  is a cursor: the next page starts after that digest in this order (look up
  its `period_end`, then `(period_end, id) < (?, ?)`). `limit` defaults to 20,
  at most 100.

## Protocol (`proto/nyttig/v1/nyttig.proto`)

```proto
message DigestSeries {
    int64  id            = 1;
    int64  assessor_id   = 2;
    string assessor_name = 3;
    string name          = 4;
    string description   = 5;
    int32  position      = 6;
    google.protobuf.Timestamp created_at = 7;
    int32  digest_count  = 8;
    google.protobuf.Timestamp latest_period_end = 9;  // unset when empty
}

message DigestItem {    // an item a digest links to
    int64  item_id     = 1;
    string title       = 2;
    string link        = 3;
    string source_name = 4;
    google.protobuf.Timestamp published = 5;
}

message DigestRef {     // an earlier digest used as input
    int64  id          = 1;
    string title       = 2;
    string series_name = 3;
    google.protobuf.Timestamp period_end = 4;
}

message Digest {
    int64  id            = 1;
    int64  series_id     = 2;
    string series_name   = 3;
    int64  assessor_id   = 4;
    string assessor_name = 5;
    string title         = 6;
    string body          = 7;   // empty in lists without include_body
    google.protobuf.Timestamp period_start = 8;
    google.protobuf.Timestamp period_end   = 9;
    google.protobuf.Timestamp created_at   = 10;
    google.protobuf.Timestamp updated_at   = 11;
    repeated DigestItem items  = 12;
    repeated DigestRef  inputs = 13;
}

message AddDigestSeriesRequest    { int64 assessor_id = 1; string name = 2; string description = 3; }
message UpdateDigestSeriesRequest { int64 id = 1; optional string name = 2; optional string description = 3; }
message RemoveDigestSeriesRequest { int64 id = 1; }
message ListDigestSeriesRequest   { int64 assessor_id = 1; }  // 0 = all
message ListDigestSeriesResponse  { repeated DigestSeries series = 1; }
message ReorderDigestSeriesRequest { repeated int64 ids = 1; }

message IDList { repeated int64 ids = 1; }  // wrapper for presence in updates

message AddDigestRequest {
    int64  series_id = 1;
    string title     = 2;
    string body      = 3;
    google.protobuf.Timestamp period_start = 4;
    google.protobuf.Timestamp period_end   = 5;
    repeated int64 item_ids  = 6;
    repeated int64 input_ids = 7;
}
message UpdateDigestRequest {
    int64 id = 1;
    optional string title = 2;
    optional string body  = 3;
    google.protobuf.Timestamp period_start = 4;  // unset = unchanged
    google.protobuf.Timestamp period_end   = 5;  // unset = unchanged
    IDList item_ids  = 6;                         // unset = unchanged, empty = none
    IDList input_ids = 7;
}
message RemoveDigestRequest { int64 id = 1; }
message GetDigestRequest    { int64 id = 1; }
message ListDigestsRequest  {
    int64 series_id    = 1;
    int64 before_id    = 2;  // cursor; 0 = from the newest
    int32 limit        = 3;  // 0 = 20, at most 100
    bool  include_body = 4;
}
message ListDigestsResponse { repeated Digest digests = 1; bool has_more = 2; }

rpc AddDigestSeries(AddDigestSeriesRequest) returns (DigestSeries);
rpc UpdateDigestSeries(UpdateDigestSeriesRequest) returns (DigestSeries);
rpc RemoveDigestSeries(RemoveDigestSeriesRequest) returns (google.protobuf.Empty);
rpc ListDigestSeries(ListDigestSeriesRequest) returns (ListDigestSeriesResponse);
rpc ReorderDigestSeries(ReorderDigestSeriesRequest) returns (ListDigestSeriesResponse);
rpc AddDigest(AddDigestRequest) returns (Digest);
rpc UpdateDigest(UpdateDigestRequest) returns (Digest);
rpc RemoveDigest(RemoveDigestRequest) returns (google.protobuf.Empty);
rpc GetDigest(GetDigestRequest) returns (Digest);
rpc ListDigests(ListDigestsRequest) returns (ListDigestsResponse);
```

Field numbers are suggestions for new messages; nothing existing changes.
Then `buf generate` with the local plugins. Never hand-edit `internal/proto/`.
A series is not moved between assessors (no `assessor_id` in the update).

## Validation (`service/validate.go`, `service/digests.go`)

- **Series:** name via `validateName`; duplicate (same assessor, any case)
  is `AlreadyExists` through `isUniqueViolation`; description ≤ 500 chars
  with no control characters (the assessor rules). Unknown assessor or series
  is `NotFound`. Reorder needs exactly the full set (`InvalidArgument`
  otherwise).
- **Digest:** title non-empty after trimming, ≤ 200 chars, no control
  characters; body non-empty, valid UTF-8, ≤ 65536 bytes, no control
  characters except newline and tab; both periods set on add, with
  `period_end >= period_start` (also after an update that sets only one).
  Periods are truncated to whole seconds in UTC.
- **Links:** ≤ 1000 item IDs and ≤ 100 input IDs, duplicates dropped; every
  item and input digest must exist (`NotFound` naming the first missing ID);
  a digest can't be its own input (`InvalidArgument`). Inputs may come from
  any series or assessor.
- **Removing** a series or digest that doesn't exist is `NotFound`; nyttig-api
  maps it to 404.

## HTTP API (nyttig-api, `internal/api/digests.go`)

Same patterns as `assessments.go`: bodies read with `readBody` / `jsonBody`
(unknown, duplicate and `null` fields rejected; presence kept for PATCH), IDs
as strings, protojson with `UseProtoNames` out, shape checks only (the daemon
validates values).

| Route | RPC |
|---|---|
| `GET /api/digest-series?assessor=<id>` | ListDigestSeries |
| `POST /api/digest-series` `{assessor, name, description?}` | AddDigestSeries |
| `PATCH /api/digest-series/{id}` `{name?, description?}` | UpdateDigestSeries |
| `DELETE /api/digest-series/{id}` | RemoveDigestSeries |
| `PUT /api/digest-series/order` `{"ids": [...]}` | ReorderDigestSeries |
| `GET /api/digests?series=<id>&before=<id>&limit=&body=1` | ListDigests |
| `POST /api/digests` `{series, title, body, period_start, period_end, items?, inputs?}` | AddDigest |
| `GET /api/digests/{id}` | GetDigest |
| `PATCH /api/digests/{id}` (any of the add fields except `series`) | UpdateDigest |
| `DELETE /api/digests/{id}` | RemoveDigest |

- Periods are RFC 3339 strings in requests and responses (protojson's
  `Timestamp`); `items` and `inputs` are arrays of ID strings.
- **Sanitizing on the way out** (`sanitize.go`): series names and
  descriptions, digest titles, bodies, and linked item titles go through
  `safeText` (keeps newline and tab); item links through the existing link
  sanitizer. Markup in a body stays text for the browser's renderer.
- `PUT /api/digest-series/order` is registered before
  `/api/digest-series/{id}`, as `PUT /api/views/order` is.
- The CSRF rules apply (`Content-Type: application/json`, matching
  `Origin`), as for every write.

## Web app

**Phase 5, the digests page** (`web/src/routes/digests/`, `'digests'`
added to `Page` / `PAGES` in `command.ts`, so `:digests` and the page
switcher reach it):

- `types.ts`: `DigestSeries`, `Digest`, `DigestItem`, `DigestRef`, mirroring
  protojson (string IDs, zero values left out). `api.ts`: the calls.
- **Layout.** A series list (grouped by assessor, in `position` order, each
  with the assessor's color via `safeColor`, digest count and latest period)
  and a reading pane with the selected digest: title, assessor/series,
  period, updated time, body, then "Based on N items" (each a link through
  `safeLink`, title as text) and "Inputs" (each opens that digest). Below or
  beside it, the series' history: older digests by title and period, paged
  with "load older" (`before`). On a phone the series list, the history and
  the reading pane are separate screens.
- **URL is the source of truth:** `/digests?series=<id>&digest=<id>`; no
  `digest` means the newest in that series; no `series` means the first.
- **Keys** go in `keymap.ts`'s binding tables so they show in `?`: moving
  between series, between digests in the history (older/newer), and opening
  an input. Pick keys free in that mode and record them in this plan.
- **Commands** in `command.ts`: `:digests` and `:digest <assessor>/<series>`
  (completion over series names).
- **Managing series** on the same page: rename, edit the description,
  reorder, delete (confirmation: "*N digests will be deleted*"), using the
  `ManageView` frame or its pieces. The assessors page's delete confirmation
  also says how many series and digests go.
- **Body as plain text** in this phase: `white-space: pre-wrap`, text nodes
  only. `policy.test.ts` still forbids `{@html}`.
- Vitest for every pure module touched (`command`, `keymap`, `api`, any new
  `digests.ts` for URL and grouping logic). Playwright (both viewports,
  names include the project, everything cleaned up): create an assessor,
  series and two digests through the API (the second with the first as
  input and two items linked), open `/digests`, see the newest, open the
  older one through the input link and through the history, delete the
  series.

**Phase 6, Markdown** (`web/src/lib/markdown.ts`, a `Markdown.svelte`
renderer):

- A small pure parser from text to a typed tree. Blocks: headings `#`–`###`,
  paragraphs, bullet and numbered lists (nesting capped, e.g. 3 levels),
  block quotes, fenced code blocks, horizontal rules. Inlines: `**strong**`,
  `*em*`, `` `code` ``, `[text](url)`, autolinked `https://…`, and `[#123]`.
- **Rendering builds elements and text nodes only**; no `{@html}`, no raw
  HTML (a `<script>` in the body shows as the text `<script>`), no images.
  Links go through `safeLink`; a link it rejects renders as its text.
  `[#123]` becomes a link to that item's article (`safeLink` of its `link`)
  with its title as the tooltip **only when item 123 is in the digest's
  linked items**; otherwise it stays the literal text.
- Bounded work: the parser is linear in the input, and nesting depth is
  capped, so a hostile body can't blow the stack.
- Vitest: each construct, hostile input (`javascript:` links, HTML, deep
  nesting, unclosed markers, 64 KiB of `*`), and `[#id]` with and without a
  matching item.

## TUI (phase 7)

- A digests screen opened with a free key (record it in the README's TUI
  keybindings) and left with `esc`/`q`, without disturbing the feed's
  stream or filter.
- Series list on the left (assessor color, digest count), the selected
  digest on the right in a scrollable pane, with the history reachable with
  keys (older/newer). Load on open and on `r`; no live updates.
- The body is the same Markdown subset rendered to styled terminal text with
  Lipgloss: headings bold, list bullets, code dimmed, links as
  `text (url)`, `[#123]` as the item's title when it is linked. Strip control
  characters before rendering (terminal escape injection); never pass body
  text to the terminal raw.
- Use only dependencies already in `go.mod`.
- Tests next to the code (`digests_test.go`): rendering, escape stripping,
  navigation.

## CLI (phase 3, `cmd/nyttig/digests.go`)

- `list-series [-assessor NAME]`, `add-series -assessor NAME -n NAME [-description TEXT]`,
  `update-series <series> [-n NAME] [-description TEXT]`, `remove-series <series>`,
  `reorder-series <series>...`.
- `list-digests <series> [-limit N] [-before ID]`, `show-digest <id>` (body,
  links and inputs), `add-digest <series> -title T -start TIME -end TIME
  [-body TEXT | -body-file PATH|-] [-items 1,2] [-inputs 4,5]`,
  `update-digest <id>` with the same optional flags (`-items ''` clears),
  `remove-digest <id>`.
- `<series>` is `assessor/name` or a numeric ID. Times accept RFC 3339 and
  `YYYY-MM-DD` (UTC midnight; for `-end`, the end of that day).
- Output to stdout, errors to stderr, exit 1 on failure; `_, _ =
  fmt.Fprintf` for linting.

## Writing a digest

For the README's "Writing an assessor" section:

1. Register the assessor (as today), and its series once, or call
   `client.EnsureDigestSeries` / `POST /api/digest-series` on first use and
   accept `AlreadyExists` / 409 by looking it up again.
2. Collect the input: items through `Search` / `GET /api/items` (a saved view
   with `?view=` works well, e.g. `?view=cve&after=<start of day>`), and
   earlier digests through `ListDigests` / `GET /api/digests?series=&body=1`.
3. Write the digest with `AddDigest` / `POST /api/digests`, linking the items
   it is based on and the digests it read.
4. Fixing one later: `UpdateDigest` / `PATCH /api/digests/{id}`.

## Security

- **Digests are untrusted text**, like notes: sanitized on the way out of
  nyttig-api, rendered by the Markdown renderer as text nodes in the
  browser, stripped of control characters in the TUI.
- **Prompt injection compounds.** A poisoned feed that gets an instruction
  into Monday's digest is carried into every digest that uses it as input,
  and into the monthly. Treat earlier digests as untrusted input in the
  assessor's prompt, exactly like feed text. `digest_inputs` and
  `digest_items` make the trail visible, so a claim can be traced back.
- The access model is unchanged (see the assessments plan): every credential
  is full admin, and the program, not the model, holds it.

## Phases (one commit each on one branch; conventional subjects)

1. **`feat(db): digests and digest series`.** Migration 9 in both
   directories, `db/digests.go`. Tests: the CHECKs (period order, self
   input), the unique series name per assessor and case, the cascades
   (assessor → series → digests → links; item delete keeps the digest; input
   delete keeps the other digest), ordering and the `before` cursor including
   equal `period_end`s, `withBody`, the date shape matching `items.published`,
   update replacing link sets, reorder.
2. **`feat(service): digest RPCs`.** Proto, `buf generate`,
   `service/digests.go`, validation, `internal/client` wrapper and
   `EnsureDigestSeries`. Tests: the validation table (sizes, UTF-8, control
   characters, periods, link limits), `NotFound` / `AlreadyExists`, a wire
   round-trip like `TestSourceColorSurvivesWire` (periods, links, inputs),
   `EnsureDigestSeries` racing.
3. **`feat(cli): digest series and digests`.** Subcommands, README (CLI
   section and "Writing an assessor").
4. **`feat(api): digest endpoints`.** Handlers, routes, sanitizing, handler
   tests against `fake_test.go`, README.
5. **`feat(web): digests page`.** As above, with the body as plain text.
6. **`feat(web): markdown in digests`.** As above.
7. **`feat(tui): digests screen`.** As above, README keybindings.

Each phase updates this plan's `Status:` line, the README and `AGENTS.md`
(layout, and an architecture note on digests next to the assessments note),
and records deviations below.

## Out of scope / follow-ups

- Live push of new digests (an SSE or stream event).
- Revisions of a digest (edits overwrite).
- "Mentioned in digests" on an item's detail (the reverse of `digest_items`;
  the index is already there).
- Linking a series to a saved view (a digest panel on that view's tab).
- Serving a series as an RSS/Atom feed, or e-mailing it.
- `[[digest_series]]` config seeding.
- Search over digest bodies (FTS).
- Per-assessor credentials (shared with the assessments follow-up).

## Verification

Before each push, the AGENTS.md checks: `gofmt -l`, `go mod tidy -diff`,
`go vet ./...`, golangci-lint `--new-from-rev=origin/main` with a
self-installed binary, `go test -race -shuffle=on -tags sqlite_fts5 ./...`,
`buf generate` with no diff, and in `web/` `pnpm check`, `pnpm test`,
`pnpm build`, `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium pnpm test:e2e`.
govulncheck can't reach vuln.go.dev from the sandbox; leave it to CI.

By hand, with nyttigd on `sample_config.toml`:

1. `nyttig add-series -assessor claude -n daily-cve`
2. `nyttig add-digest claude/daily-cve -title "CVE news, 5 Oct" -start 2026-10-05 -end 2026-10-05 -body-file notes.md -items <ids>`
3. A second one with `-inputs <first id>`.
4. Read both in the web app (`/digests`) and the TUI, follow the input link,
   and check that a `<script>` in the body shows as text.

## Deviations from this plan

- **Phase 1:** `db/digests.go` also has `FirstMissingItemID` and
  `FirstMissingDigestID` (one batched query each), which the service uses for
  its `NotFound` checks; `ListDigests` returns `([]*Digest, hasMore, error)`
  and an unknown `beforeID` (or one from another series) gives an empty page.

- **Phase 3:** the series and digest commands take `<series>` / `<digest-id>`
  as the first argument, before the flags (as `assess` does), except
  `reorder-series`, whose references come after the flags like
  `reorder-views`. `internal/tui` gained `SanitizeText` (control characters
  out, newline and tab kept), which `show-digest` uses and the TUI screen
  will reuse. README: the digests concept section ("Digests", with "Writing a
  digest") is a top-level section after "Assessments".
