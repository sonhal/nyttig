# Saved views plan

Status: **phases 1–3 implemented, not yet merged** (the backend, and the CLI). Phases 4–7 are planned.

Saved views are persistent, named feed filters, like Linear's views. You can
bring one up with a key or a tab instead of retyping
`tag:"cyber security" is:unviewed`. Favorite views show as tabs in a new bar
at the top of the feed.

## Decisions

| Topic | Decision |
|---|---|
| Storage | **Daemon/SQLite**: new table, gRPC CRUD, shared by web, CLI and (later) TUI |
| What a view holds | **Today's filter**: `q`, one source, one tag (plus its children), `unviewed`, `sort`. Multi-value filters are a separate future feature |
| Clients | **Web + CLI** now; TUI in "Out of scope / follow-ups" |
| Source/tag deleted | **Keep the view, drop the field** (`ON DELETE SET NULL`); the web delete confirmation warns about it |
| Keys | `1`–`9` go to favorite tab N, `0` to the unfiltered feed, `v` opens a fuzzy picker over all views, plus `:view <name>`, `:save [name]` and `:views` |
| Tab counts | Not now (follow-up) |

### Further decisions

| Topic | Decision |
|---|---|
| Naming | Called "views" in the UI. The table is `saved_views`, the proto message `SavedView`. "view" is already used for `view_state` (viewed tracking), and `command.ts`'s `View`/`VIEWS` means the routes (feed/sources/tags/rules). That second one gets renamed to `Page`/`PAGES` in the web phase |
| Names | Unique, case-insensitive (`COLLATE NOCASE`), ≤ 64 chars, `validateName` rules |
| Favorites order | An explicit `position` column. `ReorderSavedViews(ids)` sets the order in one transaction |
| Limits | ≤ 100 views, search ≤ 500 chars (matches the web's `MAX_QUERY`) |
| Active view in the URL | `/?view=<id>&<full filter params>`. The URL stays the source of truth for the filter; `view` only says which tab is active. When the filter differs from the view's saved filter, the tab shows a `*` (modified). `:save` writes it back; picking the tab again resets it |
| Tabs beyond 9 | Every favorite is a tab, but only the first nine have a number key |

## Data model: migration `000006_saved_views` (both migration dirs)

```sql
CREATE TABLE saved_views (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    search        TEXT    NOT NULL DEFAULT '',
    source_id     INTEGER REFERENCES sources(id) ON DELETE SET NULL,
    tag_id        INTEGER REFERENCES tags(id)    ON DELETE SET NULL,
    sort          TEXT    NOT NULL DEFAULT 'newest' CHECK (sort IN ('newest','oldest')),
    unviewed_only INTEGER NOT NULL DEFAULT 0,
    favorite      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
```

Foreign keys are already on (`PRAGMA foreign_keys=ON`, `db.go`).
New `internal/server/db/saved_views.go`: `InsertSavedView`, `GetSavedView`
(nil when missing), `ListSavedViews` (ordered by `position, id`),
`UpdateSavedView`, `DeleteSavedView` (returns bool, like `DeleteTag`),
`ReorderSavedViews` (one transaction; the ids must be exactly the full set),
and `CountViewsUsing(sourceID|tagID)` for the delete warning. New views go
to `max(position)+1`.

## Protocol (`proto/nyttig/v1/nyttig.proto`)

```proto
message ViewFilter {
    string search = 1; int64 source_id = 2; int64 tag_id = 3;
    string sort = 4; bool unviewed_only = 5;
}
message SavedView {
    int64 id = 1; string name = 2; ViewFilter filter = 3;
    bool favorite = 4; int32 position = 5;
}
message AddSavedViewRequest    { string name = 1; ViewFilter filter = 2; bool favorite = 3; }
message UpdateSavedViewRequest { int64 id = 1; optional string name = 2;
                                 ViewFilter filter = 3;          // message field: has presence; set = replace whole filter
                                 optional bool favorite = 4; }
message RemoveSavedViewRequest { int64 id = 1; }
message ReorderSavedViewsRequest { repeated int64 ids = 1; }
message ListSavedViewsResponse { repeated SavedView views = 1; }

rpc AddSavedView(AddSavedViewRequest) returns (SavedView);
rpc UpdateSavedView(UpdateSavedViewRequest) returns (SavedView);
rpc RemoveSavedView(RemoveSavedViewRequest) returns (google.protobuf.Empty);
rpc ListSavedViews(google.protobuf.Empty) returns (ListSavedViewsResponse);
rpc ReorderSavedViews(ReorderSavedViewsRequest) returns (ListSavedViewsResponse);
```

`ListSources`/`ListTags` stay unchanged. The web counts the views that use
a source or tag client-side, from the view list it already has. Then
`buf generate` (local plugins; never hand-edit `internal/proto/`).

## Daemon (`internal/server/service`)

- Handlers follow the tag CRUD template (`AddTag`/`UpdateTag`/`RemoveTag`,
  `service.go:307–431`, `dbTagToProto`, `optionalString`).
- `validate.go`: `validateViewFilter` (sort ∈ {"", newest, oldest}, search
  ≤ 500 chars with no control characters, ids ≥ 0) and `maxSavedViews = 100`.
  An unknown `source_id`/`tag_id` is `NotFound`. A duplicate name is
  `AlreadyExists` (`isUniqueViolation`). A reorder whose ids don't match the
  full set is `InvalidArgument`.
- The views are not part of the Hub or the stream: a view resolves to a plain
  `StreamFilter` client-side, so `itemMatchesFilter` and `ListItems` are untouched.
- `internal/client` wrapper methods for the CLI.

## CLI (`cmd/nyttig/main.go`)

- `list-views`: name, ★, and the filter formatted like the web `/` bar.
- `add-view -n NAME [-q TEXT] [-source S] [-tag T] [-unviewed] [-sort oldest] [-favorite]`
  (source and tag by name or ID, resolved like `add-tag-rule -tag`).
- `update-view -id N|-n NAME ...` (flags present = changed; `-no-source`,
  `-no-tag`, `-favorite=false`).
- `remove-view`, `reorder-views ID...`.
- `search -view NAME`: run a view's filter headless.

## HTTP API (`internal/api`)

- Routes (`server.go`): `GET/POST /api/views`, `PATCH/DELETE /api/views/{id}`,
  `PUT /api/views/order` (`{"ids": [...]}`).
- `manage.go`: `viewFields = name, filter, favorite`. `filter` is a nested
  object `{q, source, tag, sort, unviewed}`, the same keys as the web
  `Filter` and the feed URL. It is read with the same strictness as
  `readBody` (unknown, duplicate and null members rejected), via a small
  `readObject` helper factored out of `readBody`. In PATCH, `filter` present
  replaces the whole filter (→ `UpdateSavedViewRequest.filter`). IDs go
  through `id`/`idList`, value checks are left to the daemon, and
  `writeRPCError` maps codes (404/409/400).
- Tests against `fake_test.go`, in the style of the existing tag handler tests.

## Web (`web/`)

**Modules**
- `types.ts`: `SavedView { id; name; filter?: {...}; favorite?; position? }`,
  mirroring protojson (zero values left out, int64 as strings).
- `api.ts`: `listViews`, `addView`, `updateView`, `removeView`, `reorderViews`.
- `metadata.svelte.ts`: `views` (`$state.raw`), `reloadViews()`, included
  in `reload()` and the feed's 30 s timer.
- New pure module `lib/views.ts` (+ `views.test.ts`):
  `viewToFilter(v)`, `filterToViewBody(f)`, `viewHref(v)` (→ `/?view=id&…`),
  `activeView(params, views)`, `isModified(view, filter)` (uses `sameFilter`),
  `favorites(views)`, `viewForKey(views, n)`, `viewsUsing({source|tag}, views)`.
- `filter.ts`: `filterFromParams` keeps ignoring `view`. `metadata.feedSearch`
  must keep the `view` param, so returning from `/tags` lands on the same tab.
- `command.ts`: rename `View`/`VIEWS`/`VIEW_PATHS` → `Page`/`PAGES`/`PAGE_PATHS`
  (and `viewHref`/`goView` in `commandline.svelte.ts` → `pageHref`/`goPage`).
  Then add commands:
  - `:view <name>|all` opens a view (completion over view names).
  - `:save` updates the active view with the current filter. `:save <name>`
    creates a new view, favorited by default.
  - `:views` opens the management page.
  - `ALIASES`: `v` → `view`.
- `keymap.ts` `NORMAL`: digits `1`–`9` → `{type:'viewTab', n}`, `0` →
  `{type:'viewTab', n:0}` (the unfiltered feed), `v` → `{type:'viewPicker'}`.
  They appear in `?` through `feedHelp()` automatically.
- `picker.svelte.ts`: `PickKind` gains `'view'`. `PickOption` already fits
  (id, label). Favorites are listed first with a ★.

**Components**
- New `ViewTabs.svelte`, a row above `FilterBar` on the feed page (the page
  grid becomes `auto auto 1fr auto`): `all │ 1 security │ 2 linux* │ … │ + save`.
  The active tab is marked like `ManageView`'s `[current]`. Tabs are `<a>`
  links (`viewHref`). On mobile (< 720 px) the row scrolls horizontally
  inside itself (no page scroll) with 44 px touch targets, and the hidden
  number hints. Text only: view names go through text interpolation, never
  `{@html}` (`policy.test.ts`).
- The `+ save` button and `:save <name>` both prompt for a name, reusing the
  command line (`:save ` prefilled) so there is no new modal.
- New route `routes/views/+page.svelte` on `ManageView`, following
  `routes/tags/+page.svelte`. Rows show `★ name  tag:"cyber security"
  is:unviewed` (the filter rendered with `query.format`).
  - Tools: add, edit, delete, toggle (favorite, `Space`), plus new `move`
    tools `J`/`K` (reorder → `reorderViews`).
  - New `ViewForm.svelte`: name, a **query text field parsed with
    `query.parse`** (the same syntax as the `/` bar, with its errors shown),
    and a favorite checkbox. `forms.ts` gets `viewAddBody`/`viewPatchBody` + tests.
  - `PAGES` gains `views`, so the management tab strip shows it, and
    `FilterSheet` gets a `/views` link on mobile.
- Delete confirmations in `routes/sources` and `routes/tags` add
  "_N views filter on this source; they will stop filtering on it_"
  (`viewsUsing`).

## Phases (one commit each; conventional subjects)

1. **`feat(db): saved views table and queries`.** Migration (both dirs) and
   `saved_views.go`. Tests: CRUD, NOCASE uniqueness, reorder (full set,
   rejects partial or unknown), `ON DELETE SET NULL` for source and tag,
   new views appended at the end.
2. **`feat(service): saved views RPCs`.** Proto, `buf generate`, handlers,
   validation, client wrapper. Tests: validation table, NotFound for unknown
   source/tag/view, AlreadyExists, the 100 cap, and a wire round-trip like
   `TestSourceColorSurvivesWire`.
3. **`feat(cli): manage saved views`.** The CLI subcommands and `search -view`,
   plus the README CLI section.
4. **`feat(api): saved views endpoints`.** Routes, `readObject` refactor,
   handlers and handler tests.
5. **`refactor(web): rename View to Page`.** No behavior change; keeps
   phase 6's diff readable.
6. **`feat(web): saved views tabs, keys and management`.** Everything under
   Web. Vitest for `views.ts`, `command.ts`, `keymap.ts`, `forms.ts`, plus
   Playwright e2e (`e2e/views.spec.ts`, both viewports): `:save` a filter →
   tab appears → `0` → `1` restores it → change the filter shows `*` →
   `:save` clears it → `v` picker → manage page favorite toggle/reorder/delete
   → deleting the view's tag keeps the view and shows the warning. Per the
   e2e conventions, names include the Playwright project and everything is
   cleaned up.
7. **`docs: saved views`.** README (CLI, keys, web), `AGENTS.md` (layout:
   `views.ts`, `ViewTabs`, `routes/views`; an architecture note on views
   being client-resolved filters), and this plan's `Status:` line.

After phase 2 the backend is complete. Phases 3–4 and 5–6 only expose it.

## Known gaps (accepted)

- A bookmarked view URL carries the full filter, so it is stale if the view
  is edited later. The tab link is always current.
- Views aren't seeded from the TOML config.

## Out of scope / follow-ups

- **TUI:** a tab row, `1`–`9` keys, and a views list (needs `ListSavedViews` only).
- Unviewed counts on tabs (best with a batched count RPC).
- `[[views]]` config seeding.
- A default "home" view for `/`.
- Multi-value / exclusion filters (Linear-style), which change `StreamFilter`,
  `ListItems` and the Hub.

## Verification

Each phase runs the AGENTS.md checks before pushing:
- `gofmt -l`, `go mod tidy -diff`, `go vet ./...`
- golangci-lint `--new-from-rev=origin/main` with a self-installed binary
- `go test -race -shuffle=on -tags sqlite_fts5 ./...`
- `buf generate` leaves no diff
- in `web/`: `pnpm check`, `pnpm test`, `pnpm build`, and
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium pnpm test:e2e`

Manually: run nyttigd with `sample_config.toml`, `nyttig add-view -n sec -tag
"cyber security" -favorite`, then confirm the tab appears in the web app and
`1` switches to it. govulncheck can't reach vuln.go.dev from the sandbox,
so leave it to CI.

## Deviations from this plan

- The migration also creates `idx_saved_views_source_id` and
  `idx_saved_views_tag_id`, so `ON DELETE SET NULL` and
  `CountViewsUsing*` don't scan the table.
- Adding a view past the cap of 100 is `FailedPrecondition` (nyttig-api maps
  it to 400), a code the plan left open.
- `CountViewsUsing(sourceID|tagID)` is two functions,
  `CountViewsUsingSource` and `CountViewsUsingTag`.
- A new view's position is `max(position)+1`, with the first at 0.
- CLI: `update-view` selects the view with `-id N` or `-n NAME`, so the new
  name is `-rename NAME` (the plan's `-n` could not mean both). A view
  reference is an ID when a view has it, else a name (case-insensitive).
  `reorder-views` also accepts names. `add-view -source` and `-tag` take a
  name or ID (a source by name or abbreviation).
- CLI: any filter flag on `update-view` replaces the whole filter, built from
  the stored one plus the flags given (the RPC replaces the filter as a whole).
- CLI: `search -view` lets flags passed explicitly (query, `-s`, `-tag`,
  `-sort`, `-unviewed`) override the view's filter.
