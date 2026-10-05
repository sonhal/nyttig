# Retagging plan: apply tag rules to stored items

Status: **planned** (no phase implemented yet).

Today a tag rule only reaches items fetched after the rule exists: the
tagger runs inside `doFetch` (`cmd/nyttigd/main.go`) on `result.NewItems`
and nowhere else. A rule added after the first fetch leaves every stored
item untagged (about 600 posts in the owner's database), and a removed or
edited rule leaves its old tags behind. This plan makes `item_tags` follow
the current rules:

- **`nyttig apply-tag-rules`** re-runs the rules on stored items, adding the
  tags they give and removing the ones they no longer give.
- **Adding or removing a rule** does the same for that rule's tag at once,
  from every client (CLI, web, API).

## Decisions

| Topic | Decision |
|---|---|
| Trigger | **Both.** `AddTagRule` and `RemoveTagRule` sync the rule's tag over all stored items. A new `ApplyTagRules` RPC syncs on demand, which also covers rules seeded from `config.toml` (seeding writes to the db directly). |
| Semantics | **Full sync.** After a run, for every tag in scope, `item_tags` holds exactly the items the tag's current rules match: missing rows are added, rows no rule gives are removed. |
| Removing a rule | Syncs its tag against the remaining rules. The web editor saves an edit as add-new then remove-old, so an edited pattern ends up with exactly the new pattern's items. |
| Tags with no rules | In scope like any other tag, so a full run removes all their assignments. There is no manual tagging, so a row that no rule gives is stale by definition. |
| Invalid patterns | A tag with **any** rule whose pattern doesn't compile is **skipped** (nothing added or removed) and reported. Otherwise a broken rule would strip its tag from every item. `AddTagRule` already rejects bad patterns, so this only protects rows seeded from config or written by older versions. |
| Live updates | **`item_update` per changed item** through `Hub.PushUpdate`, the path assessments use. See [Live updates](#live-updates) for what that does on large runs. |
| Preview | `apply-tag-rules -dry-run`: the same calculation, rolled back, nothing pushed. Prints the counts per tag. |
| Surfaces | **CLI only** for the on-demand run. The web app gets the add/remove sync for free through the existing endpoints; it gets no new button. No TUI change. |
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
and other RPCs, and a rule added or removed in the middle can't make it
remove rows the new rule just wrote. With `dry_run` the transaction is
rolled back after step 5, so the counts are exactly what a real run would do.

The matching moves into the `tagger` package as pure functions, shared by
the fetch path, the sync and `TestTagRule` (which today repeats the field
switch in `service.go`). The sync itself lives in the service, which
already owns rule changes; it imports `tagger` (which imports nothing
internal) and the db layer gets plain read/write helpers. The fetch
pipeline is unchanged: it still only adds tags to new items.

### Cost

Every stored item is matched against every rule of the tags in scope. With
600 items and a few dozen rules that is well under a second. RE2 on a title
and a short description is in the order of a microsecond, so 50 000 items
times 30 rules is a few seconds, during which the single connection is held
and fetches and stream snapshots wait. `AddTagRule` and `RemoveTagRule`
only sync one tag, so they scale with that tag's rules. If databases get
much larger, batch by item ID range with one transaction per batch (the
race above then needs a mutex around rule changes and runs); not needed now.

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
    int32 items_scanned          = 1;
    int32 items_changed          = 2;
    repeated TagSyncCount tags   = 3;  // only tags with a change
    repeated TagSyncCount skipped = 4; // tags left alone: a rule doesn't compile
}
```

- Unknown `tag_id` or `source_id`: `NotFound`.
- `AddTagRule` and `RemoveTagRule` keep their request and response types.
  Their sync runs after the rule is written, in the same transaction (a
  failed sync fails the RPC and leaves the rule unchanged), and is logged
  (`tag rule applied`, with `added` / `removed`). Their responses don't
  carry the counts; `nyttig add-tag-rule` prints "Applied to existing
  items" and points at `apply-tag-rules -dry-run` for numbers.
- nyttig-api gets no new endpoint (CLI only, per the decisions). Its
  `POST /api/rules` and `DELETE /api/rules/{id}` sync through the RPCs.

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

Each phase is one commit; phases 1 to 4 can be one PR, titled
`feat(tagger): apply tag rules to stored items`.

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

### Phase 3: the sync, the RPC, rule changes

- Proto: `ApplyTagRules` and its messages as above; `buf generate`.
- `service/retag.go`: `syncTags(tx, scope) (result, changedItemIDs)`
  implementing [The sync](#the-sync), the `ApplyTagRules` handler (with
  `dry_run` rollback), and the push of `item_update` after commit.
- `AddTagRule` / `RemoveTagRule`: write the rule and sync its tag in one
  transaction, then push. `RemoveTagRule` reads the rule's tag before
  deleting it.
- `internal/client`: wrapper for the new RPC.
- Comments on `Hub.PushUpdate` and `item_update` updated.
- Tests (`service/retag_test.go`):
  - 600-item style backfill: items stored before the rule, `AddTagRule`
    tags the matching ones and only those.
  - Per-source rule tags only that source's items.
  - Removing one of two rules on a tag keeps the items the other matches
    and removes the rest; removing the last rule removes all.
  - Edit as the web does it (add new pattern, remove old): result equals
    the new pattern's matches.
  - `ApplyTagRules` with `tag_id`, with `source_id` (other sources' rows
    untouched), and with `dry_run` (counts equal a real run's, db unchanged,
    nothing pushed).
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
- `add-tag-rule` help: replace "Rules apply to items fetched after the rule
  is added" with the new behaviour; `remove-tag-rule` help says the tag is
  removed from items no remaining rule matches.
- `web/src/lib/RuleForm.svelte`: the hint "a new rule tags items fetched
  from now on; existing items are not retagged" becomes "saving applies the
  rule to stored items too". This is the only web change; `pnpm check`,
  `pnpm test` and the e2e suite must stay green (`e2e/manage.spec.ts`
  creates and deletes rules, which now retag).
- `README.md`: the paragraph on rules applying only to new items (around
  "Tag rules added with `add-tag-rule`"), the CLI reference block, and the
  rules section near "Editing a rule adds the new rule and then removing".
- `AGENTS.md`: the **Tagging** architecture note (rules now sync on
  add/remove and via `ApplyTagRules`; full-sync semantics; invalid-pattern
  tags skipped; the push behaviour), and this plan in the repository
  layout's `docs/` list.
- This plan's `Status:` line.

## Out of scope

- A web button or `:` command for the on-demand run, and a TUI key.
- Server-initiated stream resets for runs larger than the subscriber
  buffer.
- An `UpdateTagRule` RPC (edits stay add-then-remove, which now syncs
  correctly).
- Manual tagging. Full sync assumes every `item_tags` row comes from a
  rule; adding manual tags later would need a column saying where a row
  came from, and the sync would leave manual rows alone.

## Deviations

None yet.
