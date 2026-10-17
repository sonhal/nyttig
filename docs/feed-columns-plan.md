# Feed columns plan

Status: **phases 1 and 2 implemented, not yet merged** (the daemon, nyttig-api, the CLI, and the web app's model and rendering); phase 3 (the web controls) planned.

Today every feed row shows the same columns in a fixed order: unviewed dot,
date, source, tags, score chips, title, description and domain. The feed
gets a **column selection**: show only some of them, for example "title and
source", "date and title" or "title and score", and keep that choice in a
saved view, so one view can be a dense headline list and another a scored
triage list.

## Decisions

| Topic | Decision |
|---|---|
| Where it lives | **Inside the filter**: `ViewFilter.columns` (proto field 10), the web `Filter.columns`, the feed URL's `cols=` and the `/` bar's `cols:` term. A view's columns are saved, compared (`*` modified) and written back by `:save` like any other filter field |
| What the daemon does | Stores and validates it, nothing more. `SearchRequest`, `StreamFilter`, `ListItems`, the Hub and `itemMatchesFilter` never see columns, and nyttig-api's `?view=` resolution (`internal/client/viewquery.go`) ignores them |
| Clients | **Web app and CLI**. The CLI sets and prints a view's columns; the TUI has no saved views and keeps its fixed columns (see Follow-ups) |
| Order | **Show or hide only, fixed order.** Selected columns always appear in today's order; `cols:title,date` and `cols:date,title` are the same selection |
| Columns | `date`, `source`, `tags`, `score`, `title`, `desc`, `domain`. The unviewed dot and the selection highlight are not columns and always show |
| Default | Empty means **all columns**, today's layout. Selecting every column is normalized to empty, so a view with all columns and one without the field compare equal, and old views and bookmarks are unchanged |
| At least one | An empty selection can't be made: the picker refuses to clear the last column, and `cols:` with no names is a parse error |
| Spelling | Canonical names on the wire, in the database and in URLs. When parsing, clients also accept the aliases `time` (date), `src` (source), `tag` (tags), `scores` (score) and `description` (desc). The daemon accepts canonical names only |
| Changing columns | `:columns <list>` (`:cols` too) with completion, a `C` key that opens a checklist picker, the `cols:` term in the `/` bar, and the view form on `/views` (it edits the filter as query text, so `cols:` comes with it) |
| Stream | Changing only the columns **must not reconnect the stream** or reload the list; it re-renders the rows |

## Shared column rules

Like `since` (`internal/since` and `web/src/lib/since.ts`), the rules live in
one Go package and one TS module that are tested on the **same table of
cases**:

- **Go:** new package `internal/columns`: `All` (the canonical order),
  `Normalize([]string) ([]string, error)` (canonical names only, rejects
  unknown names and duplicates, returns them in canonical order, and returns
  nil for the full set), `Parse(string) ([]string, error)` (a comma-separated
  list with the aliases, then `Normalize`), `Format([]string) string`
  (comma-joined, `""` for nil).
- **TS:** `web/src/lib/columns.ts`: `COLUMNS` (canonical order, with each
  column's label for the picker), `parseColumns(text)`, `normalizeColumns`,
  `formatColumns`, `shows(columns, name)` (true for every name when the list
  is empty) and `toggleColumn(columns, name)` (never leaves an empty
  selection; turning on the last missing column gives `[]`).

Cases (both test files): `""` → all; `title,source` → `source,title`;
`Title, SRC` → `source,title` (trimmed, case-insensitive); `time,title` →
`date,title`; every column → `[]`; `title,title` → error (duplicate);
`title,bogus` → error naming `bogus`; `,` → error (empty name).

## Phase 1: daemon, nyttig-api and CLI (`feat: store feed columns in saved views`)

- `proto/nyttig/v1/nyttig.proto`: `repeated string columns = 10;` on
  `ViewFilter`, commented "canonical column names in display order; empty =
  all columns. Display only: the daemon stores it and never filters by it".
  Run `buf generate`.
- Migration `000010_saved_view_columns` (**both** migration directories):
  `ALTER TABLE saved_views ADD COLUMN columns TEXT NOT NULL DEFAULT '';`
  holding `columns.Format` of the list. The down migration drops the column.
- `internal/server/db/saved_views.go`: `SavedView.Columns []string`, read and
  written through `savedViewColumns`, the insert and the update; stored with
  `columns.Format`, read back by splitting on commas (`""` → nil). Test the
  round trip, and that an existing row reads back as nil.
- `internal/server/service/validate.go`: `validateViewFilter` runs
  `columns.Normalize`; an error is `InvalidArgument`. The service stores the
  **normalized** list (the canonical order, nil for the full set), so
  `AddSavedView` / `UpdateSavedView` answer with what was stored. Tests: an
  unknown name, a duplicate, the reordering and the full set → empty.
- `internal/api/manage.go`: `filterFields` gains `cols`; `filterJSON.Cols
  []string \`json:"cols,omitempty"\``; `viewFilter` reads it as an array of
  strings (a non-array or a non-string element is a 400, like other shape
  errors). The daemon validates the names. Handler tests: a view round-trips
  `cols`, a PATCH with a filter without `cols` clears them (a filter replaces
  the whole filter), and `cols: "title"` (not an array) is a 400.
- `cmd/nyttig/views.go`:
  - `add-view -columns <list>` (the aliases accepted, via `columns.Parse`).
  - `update-view -columns <list>` and `-no-columns` (back to all), refused
    together, like `-since` / `-no-since`; either one counts as a filter
    change, so the rest of the filter is kept from the current view.
  - `formatViewFilter` appends `cols:<list>` when set, so `list-views` shows
    it in the `/` bar's syntax; `queryWord` quotes a free-text word starting
    with `cols:`.
  - Tests in `views_test.go` for the flags and the formatting.
- `search -view` and nyttig-api's `?view=` ignore columns (assert this in a
  `viewquery` test so a future change has to make a choice).
- Docs: README's `add-view` / `update-view` flags and the saved views
  section; AGENTS.md's saved views paragraph (the filter keys and `cols`, and
  "display only"); the repository layout entry for `internal/columns`.

**Phase 1 notes.** As planned. Two things found on the way: the CLI's
`update-view` copies the stored filter field by field, so it copies
`Columns` too (or `-since` alone would have dropped them; AGENTS.md now says
so); and `TestMigration8_RebuildKeepsSavedViews` migrates through 10, since
`ListSavedViews` reads the new column. Until phase 2 the web app doesn't know
`cols`: saving or editing a view's filter there (`:save`, the view form)
sends a filter without it and so clears the view's columns (fixed by
phase 2).

## Phase 2: web model and rendering (`feat(web): choose the feed's columns`)

- `web/src/lib/types.ts`: `Filter.columns: string[]` (`[]` = all) and
  `ViewFilter.cols?: string[]`.
- `web/src/lib/filter.ts`:
  - `defaultFilter.columns = []`. `filterFromParams` reads `cols` with
    `parseColumns`; a malformed value falls back to `[]`, as other malformed
    parameters do. `filterToParams` writes `cols=<formatColumns>` when
    non-empty.
  - `apiParams` **deletes `cols`**, as it does `since`: the API must not get
    it (nyttig-api would ignore it, but stray parameters hide bugs).
  - New `sameItems(a, b)`: compares `apiParams`, i.e. whether two filters
    select the same items. `+page.svelte`'s stream effect (today
    `sameFilter(connected, f)`) and anything else that decides to reload
    (the unviewed count) use `sameItems`, so a column change re-renders
    without reconnecting. `sameFilter` keeps comparing everything and is still
    what `setFilter` and `isModified` use. Check every `sameFilter` call site
    for which one it means.
- `web/src/lib/views.ts`: `viewToFilter` reads `cols` through
  `normalizeColumns` (unknown names dropped, not an error: the daemon
  validated them, and an older client must still open the view);
  `filterToViewBody` writes `cols` when non-empty. `isModified` then covers
  columns without other changes.
- `web/src/lib/query.ts`: the `cols:` key (alias `columns:`) with
  `parseColumns`, a usage line and description for the bar's help and
  completion of column names after `cols:` and after commas in its value.
  `format` writes `cols:<list>` last. Tests: parse/format round trip, an
  unknown name is an error naming it, completion.
- `web/src/lib/FeedRow.svelte`: a `columns: string[]` prop; each cell renders
  only when `shows(columns, name)`. Layout rules:
  - Desktop: hidden cells are absent, not `visibility: hidden`. The title
    keeps `max-width: 60%` only while the description shows too; whichever
    of title and description is the last text column fills the row, and the
    domain stays right-aligned. With neither, the chips sit left and the
    domain right.
  - Mobile (two lines, 44px, unchanged height): line 1 holds the shown meta
    cells (date, source, tags, score, domain). Line 2 is the title; when the
    title is hidden and `desc` is shown, line 2 is the description instead
    (today the description never shows on mobile). With neither, line 2 is
    empty and line 1 stays where it is: rows keep one height, as the
    virtual list needs (`--row-h`).
  - The expanded row (`ItemDetail.svelte`) is unchanged: it always shows
    everything.
- `web/src/routes/+page.svelte` passes `filter.columns` to every row.
- `ViewForm.svelte` needs no new field: it edits the filter as query text,
  so `cols:` goes through `query.ts`. Its hint mentions `cols:`. The views
  page rows show it through `format`.
- Tests: Vitest for `columns.ts` (the shared cases), `filter.ts` (URL round
  trip, `apiParams` without `cols`, `sameItems` vs `sameFilter`), `views.ts`
  (`viewToFilter`, `filterToViewBody`, `isModified` on a column change) and
  `query.ts`. e2e (`web/e2e/`), at both viewports: open
  `/?cols=title,source`, check that rows have no date, tags or domain cells
  and that the `/` bar shows `cols:source,title`; save a view with columns,
  switch tabs and back, and check that the columns come back and that
  changing only the columns marks the tab `*` without reloading the list
  (the rows' DOM nodes are kept, or the stream's request count is unchanged).

**Phase 2 notes.** As planned, with these differences:

- On mobile, with neither title nor description shown, the single line is
  centred vertically in the 44px row (the cells' `align-content: center`),
  not kept at the top. The row height is unchanged.
- `columns.ts` also has `knownColumns` (what `viewToFilter` uses: unknown
  names dropped, never an error) and the `COLUMN_NAMES` list for completion.
- `cols:` with no names is an error ("needs column names") rather than
  meaning every column, since it is almost always unfinished typing. Two
  `cols:` terms that agree are accepted, like `since:`.
- Completing a `cols:` value offers the names not yet in the list for the
  part after the last comma; accepting one adds a space, as other
  completions do.
- The e2e test checks that changing only the columns sends no request to
  `/api/stream` or `/api/items` (and fails if the stream effect goes back
  to `sameFilter`; checked by hand).

## Phase 3: web controls (`feat(web): column picker and :columns`)

- `web/src/lib/command.ts`: `:columns <list>|all` (alias `:cols`), parsed
  with `parseColumns`; `all` clears to `[]`; completion of column names,
  including after a comma. `:columns` alone opens the picker. Run through
  `commandline.svelte.ts` like `:since`-style filter commands, so off the
  feed it navigates to the feed with the columns set and the active view kept.
- `web/src/lib/keymap.ts`: `C` in the feed's normal mode, group "Filter",
  "choose the columns". It shows in `?` through `help.ts`.
- The picker: `Picker.svelte` today picks one entry and closes. Add a
  checklist mode (or a small `ColumnPicker.svelte` that reuses its frame and
  styles): `j`/`k` move, Space or Enter toggles the column under the cursor
  and applies it at once (the feed re-renders behind it), and `Escape` / `q`
  closes. Each entry shows `[x]` / `[ ]` and the label. Toggling the last
  shown column off is refused with a status message. It works from the
  mobile filter sheet too (`FilterSheet.svelte`: a "columns" row that opens
  it).
- Tests: `command.test.ts` (parsing, `all`, errors, completion),
  `keymap.test.ts` (`C` is bound and listed), the toggle rules in
  `columns.test.ts`, and an e2e that presses `C`, hides the date and source,
  and checks the rows and the URL's `cols=`.
- Docs: README's web app keys and commands; AGENTS.md's web log-viewer
  paragraph (`C`, `:columns`, `cols:` in `query.ts`).

## Follow-ups (out of scope)

- **TUI columns.** The TUI has no saved views. A session-only toggle (a key,
  the same names) would need `renderRow`'s width math in
  `internal/tui/table.go` to handle missing columns; once the TUI gets saved
  views it would read `ViewFilter.columns` as is.
- **`nyttig search -view` honoring the view's columns** in its output.
- **User-defined order** (`score,title` ≠ `title,score`). The stored list
  is already ordered, so this would mean dropping the normalization's
  sorting, not changing the schema.
- **Column widths** or a fixed-width title.
