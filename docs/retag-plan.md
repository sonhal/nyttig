# Retagging plan: apply tag rules to stored items

Status: **implemented, not yet merged** (phases 1 to 4, one commit each, on `claude/lucid-shannon-zu409c`). See Deviations.

Today a tag rule only reaches items fetched after the rule exists: the
tagger runs inside `doFetch` (`cmd/nyttigd/main.go`) on `result.NewItems`
and nowhere else. A rule added after the first fetch leaves every stored
item untagged (about 600 posts in the owner's database), and a removed or
edited rule leaves its old tags behind.

This plan adds one **manual** action, `nyttig apply-tag-rules`, that re-runs
the rules on stored items: it adds the tags they give and removes the ones
they no longer give. Nothing else changes: adding, removing or editing a
rule, or a tag, still never touches stored items by itself.

## Decisions

| Topic | Decision |
|---|---|
| Trigger | **Manual only.** A new `ApplyTagRules` RPC, run by `nyttig apply-tag-rules`. `AddTagRule`, `RemoveTagRule`, the tag RPCs and config seeding keep their current behaviour. |
| Semantics | **Full sync.** After a run, for every tag in scope, `item_tags` holds exactly the items the tag's current rules match: missing rows are added, rows no rule gives are removed. |
| Rule edits | Unchanged. The web editor saves an edit as add-new then remove-old; new items get the new pattern's tags, and stored items keep the old ones until the next `apply-tag-rules`. |
| Tags with no rules | In scope like any other tag, so a full run removes all their assignments. There is no manual tagging, so a row that no rule gives is stale by definition. |
| Invalid patterns | A tag with **any** rule whose pattern doesn't compile is **skipped** (nothing added or removed) and reported. Otherwise a broken rule would strip its tag from every item. `AddTagRule` already rejects bad patterns, so this only protects rows seeded from config or written by older versions. |
| Live updates | **`item_update` per changed item** through `Hub.PushUpdate`, the path assessments use. See [Live updates](#live-updates) for what that does on large runs. |
| Preview | `apply-tag-rules -dry-run`: the same calculation, rolled back, nothing pushed. Prints the counts per tag. |
| Surfaces | **CLI only.** No web button, `:` command, nyttig-api endpoint or TUI key. |
| Scope of a run | Everything by default. `-tag <name\|id>` limits it to one tag, `-source <id>` to one source's items. No `-rule`: under full sync a tag's rows depend on all its rules together, so the tag is the smallest unit that can be synced. |
| Tag tree | Unaffected. `item_tags` still means "a rule of this tag matched", and ancestors are still expanded when querying (`docs/tag-tree-plan.md`). Syncing a parent tag syncs only its own rules' rows. |

## The sync

For a scope (set of tags `T`, optionally one source `S`):

1. Load the rules of every tag in `T` and compile them. Tags with an
   uncompilable rule move from `T` to `skipped`.
2. Load the items in scope (all items, or those of `S`): `id`, `source_id`,
   `title`, `description`.
3. **Desired** = the `(item, tag)` pairs where some rule of the tag applies
   to the item's source (global, or the item's source) and matches its
   field, exactly as the tagger matches today.
4. **Current** = the `item_tags` rows with `tag_id IN T` (and the item in
   `S`).
5. Insert desired minus current, delete current minus desired.
6. Report, per tag: `added`, `removed`; overall: `items_scanned`,
   `items_changed`, `skipped` (tag IDs and names).

Steps 1 to 5 run in **one transaction**. The pool has a single connection
(`db.go`: `SetMaxOpenConns(1)`), so the run is serialized against fetches
and other RPCs, and a rule added or removed during the run can't interleave
with it. With `dry_run` the transaction is rolled back after step 5, so the
counts are exactly what a real run would do.

The matching moves into the `tagger` package as pure functions, shared by
the fetch path, the sync and `TestTagRule` (which today repeats the field
switch in `service.go`). The sync itself lives in the service; it imports
`tagger` (which imports nothing internal) and the db layer gets plain
read/write helpers. The fetch pipeline is unchanged: it still only adds
tags to new items.

### Cost

Every stored item is matched against every rule of the tags in scope. With
600 items and a few dozen rules that is well under a second. RE2 on a title
and a short description is in the order of a microsecond, so 50 000 items
times 30 rules is a few seconds, during which the single connection is held
and fetches and stream snapshots wait. Since the run is manual, that is
acceptable; if databases get much larger, batch by item ID range with one
transaction per batch. Not needed now.

## Live updates

After a committed run, the service reloads each item whose tags changed
(`db.GetItem`, as `pushAssessmentUpdate` does) and calls `hub.PushUpdate`.
`StreamItems` already sets `update_matches` per subscriber, the web reducer
and the TUI's `Table.ApplyUpdate` already replace an item they show and
insert a matching one, and an item that no longer matches stays until the
next reload ("never remove one live"). Nothing on the client side changes.

**Large runs drop updates.** Each subscriber has a 64-message buffer and
`PushUpdate` never blocks, so a run that changes your 600 items sends the
first few dozen to a slow subscriber and drops the rest:

- **nyttig-api** closes the SSE connection when its own queue overflows,
  and the browser reconnects and gets a fresh snapshot, so the web app ends
  up correct either way.
- **The TUI** keeps whatever it received; the rest show their new tags
  after the next reconnect or filter change.

The CLI prints a note when a run changed more than 64 items. A
server-initiated reset (re-snapshot every subscriber) would fix this
properly but changes the stream protocol and the clients' snapshot cutoff
handling (`FeedStream.after`), so it is out of scope here.

`PushUpdate`'s and `item_update`'s comments say "assessments changed"; they
become "tags or assessments changed".

## API

```proto
// ApplyTagRules makes item_tags match the current rules for the tags in
// scope: adds the tags the rules give, removes the ones they no longer give.
rpc ApplyTagRules(ApplyTagRulesRequest) returns (ApplyTagRulesResponse);

message ApplyTagRulesRequest {
    int64 tag_id    = 1;  // 0 = every tag
    int64 source_id = 2;  // 0 = every source's items
    bool  dry_run   = 3;  // compute and report, change nothing
}

message TagSyncCount {
    int64  tag_id   = 1;
    string tag_name = 2;
    int32  added    = 3;
    int32  removed  = 4;
}

message ApplyTagRulesResponse {
    int32 items_scanned           = 1;
    int32 items_changed           = 2;
    repeated TagSyncCount tags    = 3;  // only tags with a change
    repeated TagSyncCount skipped = 4;  // tags left alone: a rule doesn't compile
}
```

- Unknown `tag_id` or `source_id`: `NotFound`.
- The run is logged (`tag rules applied`, with the scope, `dry_run`,
  `items_changed`, and the added/removed totals).
- nyttig-api gets no endpoint for it.

## CLI

```
nyttig apply-tag-rules [-tag <name|id>] [-source <id>] [-dry-run]
```

Output:

```
Scanned 612 items; 3 tags changed on 241 items.
  security   +180  -0
  rust       +58   -2
  golang     +3    -0
Skipped (a rule's pattern doesn't compile): legacy
Open TUIs may miss some of these updates; they show them after a reconnect.
```

With `-dry-run` the first line says "Would change" and nothing else
differs. Exit status 1 on an RPC error, 0 otherwise (also when tags were
skipped).

## Phases

Each phase is one commit; all four fit one PR, titled
`feat: apply tag rules to stored items with nyttig apply-tag-rules`.

### Phase 1: tagger matching as pure functions

- `tagger.Compile(rules []TagRule) (compiled []CompiledRule, bad []TagRule)`
  and `tagger.Matches(cr CompiledRule, item Item) bool` (source scope and
  field). `TagItem` / `TagItems` use them; behaviour unchanged.
- `tagger.Desired(compiled []CompiledRule, items []Item) map[int64][]int64`
  (item ID to tag IDs).
- `TestTagRule` uses `Matches` instead of its own field switch.
- Tests: table-driven `Matches` (title/description/both/empty field, global
  vs. per-source, other source); `Desired` with two rules on one tag and an
  item matching both; `Compile` returns the bad rule. Existing tagger tests
  stay green.

### Phase 2: db helpers

In `internal/server/db/tags.go`, taking a `Querier`-like interface that
`*sql.Tx` satisfies:

- `ListTagRulesForTags(q, tagIDs []int64)` (nil = all).
- `ListItemsForTagging(q, sourceID int64)`: id, source, title, description;
  no tags, no view state.
- `ListItemTagPairs(q, tagIDs []int64, sourceID int64)`.
- `AssignTagsToItems(q, pairs)` (`INSERT OR IGNORE`) and
  `RemoveTagsFromItems(q, pairs)`.
- Tests: each helper on a small fixture, including the source filter and an
  item of another source left untouched.

### Phase 3: the sync and the RPC

- Proto: `ApplyTagRules` and its messages as above; `buf generate`.
- `service/retag.go`: `syncTags(tx, scope) (result, changedItemIDs)`
  implementing [The sync](#the-sync), the `ApplyTagRules` handler (with
  `dry_run` rollback), and the push of `item_update` after commit.
- `internal/client`: wrapper for the new RPC.
- Comments on `Hub.PushUpdate` and `item_update` updated.
- Tests (`service/retag_test.go`):
  - Backfill: items stored before the rule; `AddTagRule` alone leaves them
    untagged, `ApplyTagRules` tags the matching ones and only those.
  - Per-source rule tags only that source's items.
  - Removing one of two rules on a tag, then applying: items the other rule
    matches keep the tag, the rest lose it; with the last rule removed, all
    lose it.
  - Edit as the web does it (add new pattern, remove old), then apply:
    result equals the new pattern's matches.
  - `tag_id` scope (other tags' rows untouched), `source_id` scope (other
    sources' rows untouched), and `dry_run` (counts equal a real run's, db
    unchanged, nothing pushed).
  - A tag with an uncompilable rule (inserted with `db.InsertTagRule`) is
    reported in `skipped` and keeps its rows.
  - A tag with no rules loses its rows on a full run.
  - Idempotent: a second run reports no changes.
  - Unknown tag / source: `NotFound`.
  - A subscriber gets `item_update` for a changed item, with the new tags,
    and `update_matches` follows its tag filter.
  - Agreement: after a sync, for each tag, `ListItems` with `tag_exact`
    returns exactly the items `TestTagRule` matches for its single rule.

### Phase 4: CLI and docs

- `nyttig apply-tag-rules` (`cmd/nyttig/main.go`): flags as above, `-tag`
  resolved by name or ID as `add-tag-rule` does, output as above; listed in
  the usage text.
- `add-tag-rule` and `remove-tag-rule` help: keep "Rules apply to items
  fetched after the rule is added" and add "run `nyttig apply-tag-rules` to
  apply the current rules to stored items".
- `web/src/lib/RuleForm.svelte`: the hint "a new rule tags items fetched
  from now on; existing items are not retagged" gains "run `nyttig
  apply-tag-rules` to retag them". Text only; `pnpm check`, `pnpm test`,
  `pnpm build` and the e2e suite must stay green.
- `README.md`: the paragraph on rules applying only to new items (around
  "Tag rules added with `add-tag-rule`"), the CLI reference block, and the
  rules section near "Editing a rule adds the new rule and then removing".
- `AGENTS.md`: the **Tagging** architecture note (`ApplyTagRules` is the
  only path that changes stored items' tags; full-sync semantics;
  invalid-pattern tags skipped; the push behaviour).
- This plan's `Status:` line.

## Out of scope

- Retagging automatically when a rule or tag changes (decided against:
  retagging is a manual action).
- A web button or `:` command, a nyttig-api endpoint and a TUI key.
- Server-initiated stream resets for runs larger than the subscriber
  buffer.
- An `UpdateTagRule` RPC.
- Manual tagging. Full sync assumes every `item_tags` row comes from a
  rule; adding manual tags later would need a column saying where a row
  came from, and the sync would leave manual rows alone.

## Deviations

- **Phase 1.** `Matches` is a method, `CompiledRule.Matches(item)`, and
  there is also `CompiledRule.MatchesText(title, description)` (field only,
  no source check) plus `NewCompiledRule(rule, re)`, so `TestTagRule` can
  keep compiling and validating the pattern with `compilePattern` and still
  match through the tagger. Unknown fields are still logged by the
  `Tagger`'s rule loading; the pure functions don't log.
- **Phase 2.** The helpers take one `tagID int64` (0 = every tag) instead
  of a slice: a run covers one tag or all, and skipped tags are filtered in
  Go. They live in `internal/server/db/retag.go` with an exported
  `QueryExecer` (a `Querier` that can `Exec`), and phase 3 needed three
  more: `TagExists`, `SourceExists` (for `NotFound` inside the transaction;
  the pool has one connection, so the service can't use `s.db` there) and
  `ListTagNames` (names for the report, including tags without rules).
- **Phase 3.** The update push reuses `pushItemUpdate` from
  `service/assessments.go`; its log message became `push item update`.
- **Phase 4.** The command lives in `cmd/nyttig/retag.go` (like
  `views.go` and `assess.go`) with its output in `printApplyResult`, which
  `retag_test.go` covers. With no changes it prints `Scanned N items.
  Nothing to change.` (`Nothing would change.` with `-dry-run`), and counts
  use the singular for one. The web hint shows the command in `<code>`.
