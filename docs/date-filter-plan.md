# Date filter plan

Status: **phases 1–6 implemented and merged** (#25: the database, the daemon, the CLI, the HTTP API and the web app). The assessments branch later renumbered its own migration (8) and proto fields around this one; see `docs/assessments-plan.md`.

Views (and the feed filter in general) get a rolling time window: "only items
from the last day / week / month". A view called `today` is `since:24h`, one
called `this week in security` is `tag:security since:7d`.

## Decisions

| Topic | Decision |
|---|---|
| Window kind | **Rolling durations** relative to now: `since:24h`, `since:7d`, `since:2w`, `since:1mo`, `since:1y`. No calendar periods ("today" from local midnight) and no absolute dates; both are follow-ups |
| Which date | **Published, else fetched**: `COALESCE(published, fetched_at)`, the same date the feed sorts by. An item whose feed gives no date counts from when nyttigd first fetched it |
| Scope | **A general filter**, not a view-only field: the `/` bar, the feed URL, the HTTP API, `SearchRequest`/`StreamFilter`, the CLI's `search`, `add-view` and `update-view`. A view stores it like it stores `tag` or `q`, and keeps resolving client-side to a plain filter (the saved-views design) |
| Aging out | **On reload only.** The window is applied when the list loads: opening or switching the filter/view, and every (re)connect of the stream. Rows already on screen are not removed as they get older |

### Further decisions

| Topic | Decision |
|---|---|
| Syntax | `<n><unit>`, `n` 1–9999, units `h` (hours), `d` (days), `w` (weeks), `mo` (calendar months), `y` (calendar years). `m` is rejected as ambiguous (minutes or months?). Lower case only; canonical spelling is what was typed (`30d` stays `30d`, not `1mo`) |
| Months and years | Calendar arithmetic in UTC (`time.AddDate(0, -n, 0)` / `setUTCMonth`), so `since:1mo` on 31 March is 28/29 February, the same as Go's `AddDate` normalisation. Both implementations must agree; a shared table of cases is tested in Go and Vitest |
| Upper bound | 9999 of any unit is accepted (≈ 27 years in days); no extra cap needed |
| Stored form | The duration **string** (`"7d"`), not seconds: the view keeps the spelling the user chose. Column `saved_views.since TEXT NOT NULL DEFAULT ''`, validated on write |
| Wire form for queries | An **absolute cutoff**, `google.protobuf.Timestamp after`, on `SearchRequest` and `StreamFilter`; HTTP `after=<unix seconds>`. See "Why an absolute cutoff" |
| Who resolves `since` → `after` | The client: the web app in `stream.svelte.ts`, the CLI before calling `Search`. The daemon never sees a duration in a query, only in a saved view |
| Future dates | An item with a published date in the future always passes (it is "after" any cutoff). Bogus feed dates are a separate problem |
| TUI | Not now. The TUI has no saved views yet; a `since` cycle key is a follow-up together with views in the TUI |

### Why an absolute cutoff on the wire

The web feed is one stream snapshot plus older pages fetched by offset
(`reducer.ts`, the `ranked` invariant: never count more rows than the
database has above the next page). If every request carried `since=7d` and
the server took "now" each time, the window would slide between the
snapshot and the next page. With `sort:oldest`, rows at the top of the
result leave the window, every offset moves up, and a page skips rows,
which is exactly what the invariant forbids. So the client fixes one
cutoff per snapshot (per `epoch`) and sends that same `after` with the
stream and with every older page. A reconnect starts a new snapshot and
may take a new cutoff, which is the "aging out on reload" decision.

Clock skew between browser and server shifts the window by the skew; that
is acceptable for windows measured in hours or more.

## Data model: migration `000007_saved_view_since` (both migration dirs)

```sql
ALTER TABLE saved_views ADD COLUMN since TEXT NOT NULL DEFAULT '';
```

Down: `ALTER TABLE saved_views DROP COLUMN since;` (SQLite ≥ 3.35, which the
bundled and Debian trixie libraries both are).

`db.SavedView`/its filter struct gain `Since string`; insert, update, get
and list read and write it.

## Query (`internal/server/db/items.go`)

- `ItemFilter.After time.Time` (zero = no filter).
- Condition: `COALESCE(i.published, i.fetched_at) >= ?`, bound as the
  **string** `after.UTC().Format("2006-01-02 15:04:05")`, never as a
  `time.Time`. Dates are compared as text (see AGENTS.md, "Item dates are
  text in UTC"): `published` is `YYYY-MM-DD HH:MM:SS+00:00` and `fetched_at`
  (`CURRENT_TIMESTAMP`) is `YYYY-MM-DD HH:MM:SS`. Against a bound without a
  suffix both compare correctly, including in the same second. A test pins
  both shapes at the boundary second.
- Performance: the newest-first query walks `idx_items_published` and stops
  at the limit; `COUNT(*)` already scans. No new index now. If it shows up,
  rewrite as `(i.published >= ? OR (i.published IS NULL AND i.fetched_at >= ?))`
  so the published index can serve the range.
- New `db.ItemDate(item)`-style helper is **not** needed in SQL, but the Hub
  needs the same rule in Go (next section).

## Protocol (`proto/nyttig/v1/nyttig.proto`)

```proto
message ViewFilter   { ...; string since = 6; }   // "24h", "7d", "2w", "1mo", "1y"; "" = no window
message SearchRequest { ...; google.protobuf.Timestamp after = 9; }  // unset = no window
message StreamFilter  { ...; google.protobuf.Timestamp after = 7; }  // unset = no window
```

Then `buf generate` (local plugins; never hand-edit `internal/proto/`).

## Shared duration parser: new package `internal/since`

- `Parse(s string) (Window, error)` and `(Window) Cutoff(now time.Time) time.Time`,
  implementing the syntax above. Used by `service/validate.go` (view filters),
  the CLI, and nothing else on the server.
- Table test with the same cases as `web/src/lib/since.test.ts`
  (including 31 March − 1mo and 29 February − 1y).

## Daemon (`internal/server/service`)

- `validate.go`: `validateViewFilter` checks `since` with `since.Parse`
  (`InvalidArgument` with the parser's message). `after` on `SearchRequest`
  and `StreamFilter` must be a valid timestamp (`CheckValid`) if set.
- `Search` and the stream's `ListItems` call pass `After` through.
- Hub, `itemMatchesFilter`: if `filter.After` is set, the item's
  `Published` (else `FetchedAt`) must not be before it. This matters for a
  newly added source: its backlog is pushed live, and old items must not
  appear in a `since:24h` feed.
- `TestItemMatchesFilter_AgreesWithListItems` gains `after` cases, including
  an item with no published date and one exactly at the cutoff second.
- Wire round-trip test for `ViewFilter.since`, like `TestSourceColorSurvivesWire`.
- `internal/client`: no new methods; the fields ride the existing ones.

## CLI (`cmd/nyttig/main.go`)

- `search -since 7d`: resolved to `after` with `since.Parse(...).Cutoff(time.Now())`.
  With `-view`, the view's `since` applies unless `-since` is passed.
- `add-view -since 7d`, `update-view -since 7d` / `-no-since` (like `-no-source`).
- `list-views` shows `since:7d` in the formatted filter.
- README CLI section.

## HTTP API (`internal/api`)

- `GET /api/items` and `GET /api/stream` accept `after=<unix seconds>`
  (decimal, ≥ 0; anything else is 400), mapped to the proto `after`.
- View JSON: `filter` gains `since` (string, left out when empty), in
  requests and responses, read with the existing `jsonBody` strictness.
  Value validation stays in the daemon.
- Handler tests against `fake_test.go`: `after` parsing (valid, negative,
  garbage, absent), view `since` round trip and PATCH replacing the filter.

## Web (`web/`)

**Modules**
- New `lib/since.ts` (+ `since.test.ts`): `parseSince(text)` → window or
  error, `cutoff(window, now)`, `SINCE_RE`, the shared case table.
- `types.ts`: `Filter.since: string` ("" = no window); `SavedView.filter.since?`.
- `filter.ts`: `defaultFilter.since = ''`; `filterFromParams` reads `since`
  (invalid → ""), `filterToParams` writes it when set. That covers the page
  URL, `sameFilter` and therefore `isModified` (`*` on a tab).
  New `apiParams(f, after)`: `filterToParams(f)` without `since`, plus
  `after` when set. `api.ts` (`searchItems`) and `stream.svelte.ts` use it
  instead of `filterToParams`/`filterQuery` directly.
- `stream.svelte.ts`: in `open()`, compute `this.after` from
  `this.filter.since` and `Date.now()`; `loadOlder` sends the same
  `this.after`. Each `open()` is a new snapshot, so the invariant holds
  (see "Why an absolute cutoff"). Extend the reducer/stream tests where
  they cover request parameters.
- `query.ts`: `since:` operator (`since:7d`), with errors from
  `parseSince`, completion of common values (`24h 7d 2w 1mo 1y`), and
  `QUERY_KEYS` gains `since:<n>h|d|w|mo|y` so it shows in `?` help.
  `format` writes it after `is:` and before `sort:`.
- `views.ts`: `viewToFilter`/`filterToViewBody` carry `since`.
- `command.ts`: nothing new; `:save` saves whatever the filter has.

**Components**
- `FilterBar`/`FilterSheet` (mobile): show the active window like the other
  filter parts, and on mobile a small select (any time, 24h, 7d, 30d, 1y)
  so the window can be set without typing.
- `ViewForm.svelte`: nothing new; it already takes the `/` bar syntax.
- The feed's empty state mentions the window ("nothing in the last 7d")
  when `since` is set, since an empty list is the likely surprise.

## Phases (one commit each; conventional subjects)

1. **`feat(db): date window in item queries and saved views`.** Migration 7
   (both dirs), `ItemFilter.After`, `SavedView` `since`. Tests: cutoff with
   published, with NULL published (fetched_at), both shapes at the boundary
   second, combined with tag/search/unviewed, count matches rows; view
   `since` round trip.
2. **`feat(service): since window for searches, streams and views`.**
   `internal/since`, proto + `buf generate`, validation, Hub check, tests
   listed above.
3. **`feat(cli): -since for search and views`.** Flags, `list-views`, README.
4. **`feat(api): after parameter and view since`.** Routes' parameters,
   view JSON, handler tests.
5. **`feat(web): since filter and view windows`.** Everything under Web.
   Vitest for `since.ts`, `filter.ts`, `query.ts`, `views.ts`, the stream's
   request parameters. Playwright (`e2e/since.spec.ts`, both viewports):
   a source with backdated items (feed server: `/dated/<name>.xml`, items at
   1 h, 2 d, 10 d and 60 d old) → `/since:7d` shows two → `:save` → tab →
   `0` → tab restores the window → `since:1mo` shows three → manage page
   shows `since:7d` → cleanup.
6. **`docs: date filter`.** README (query syntax, CLI, web), `AGENTS.md`
   (the saved-views note: filter keys now include `since`; the absolute
   `after` on the wire and why), this plan's Status line.

## Out of scope / follow-ups

- Calendar periods (`since:today`, `since:week` from local midnight / Monday),
  which need the client's timezone.
- Absolute dates and ranges (`since:2026-09-01 until:2026-09-30`).
- Aging rows out of an open feed without a reload.
- `since` in the TUI (with saved views in the TUI).
- An expression index if the date filter turns out slow on large databases.

## Deviations

- The plan says 31 March − 1mo is "28/29 February, the same as Go's
  `AddDate` normalisation". `AddDate` (and JS `setUTCMonth`) overflow forward
  instead: 31 March − 1mo is **3 March** (2 March in a leap year) and 29
  February − 1y is **1 March**. Both implementations do that and the shared
  case table pins it; clamping to the end of the month would have been a
  second rule to keep in step.
- `since.Parse` and `parseSince` accept leading zeros (`07d` is 7 days, kept
  as typed) and reject five or more digits, so `00007d` is an error.
- A cutoff is never before the Unix epoch: `sinceAfter` (web) and
  `cutoffFromSince` (CLI) clamp, because `9999y` reaches before year 1, which
  a protobuf Timestamp cannot hold and `after` (≥ 0) cannot carry.
- `after` on `GET /api/items` and `/api/stream` is also capped at the last
  second a Timestamp can hold (253402300799), so nyttig-api answers 400
  instead of passing a value the daemon would reject mid-stream.
- The stream applies its cutoff in two steps: `connect` (a new filter) takes
  it at once, but after a reconnect `FeedStream.after` changes only when the
  daemon's reset arrives. Until then the rows on screen belong to the old
  snapshot, and an older page requested for them must keep the old cutoff.
- The feed's count of unviewed items (`loadUnviewed`) sends the stream's
  cutoff too, so the status bar counts inside the window.
- The desktop chip is a button that cycles any time, 24h, 7d, 30d and 1y (the
  presets of the phone select); a window typed in the bar that is not one of
  them goes back to any time on the next click. The plan only asked that the
  chip show the window.
- The e2e feed server's "hour" item is 1.5 hours old (not 1), so that a
  `since:1h` window is empty however fast the test runs.
- The TUI is unchanged, as planned.
- Merged with the assessments branch: the window and the assessment filters
  combine (AND) in `ListItems`, the Hub (`TestAssessmentFilters_WithWindowAgree`),
  saved views, the CLI, nyttig-api and the web filter. The `sort:score` order
  and `update` events obey the window; an item outside it is never inserted by
  a live update.
