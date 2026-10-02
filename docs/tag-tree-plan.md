# Tag tree plan: parent tags

Status: **in progress**. Phase 1 (schema + db layer) done.

Tags can have parent tags. Filtering by a parent shows items tagged with the
parent **or any tag below it**. Example:

```
cyber security
├── CVE
└── linux security ◀─┐  (one tag, two parents)
linux                │
└── linux security ──┘
```

`tag:"cyber security"` shows CVE news, linux security news and anything the
`cyber security` tag's own rules matched. `tag:CVE` shows only CVE news.
`tag:linux` also shows linux security news.

## Decisions

| Topic              | Decision                                                                     |
|--------------------|------------------------------------------------------------------------------|
| Shape              | A DAG: a tag can have **several parents** (and several children)             |
| Where it applies   | **When querying.** `item_tags` still records only what a rule matched; filters expand a tag to its subtree |
| Item chips         | **Direct tags only.** An item tagged `CVE` shows `CVE`, not `cyber security`  |
| Deleting a parent  | Its edges are deleted. Children with no other parent become top-level; their rules and items are kept |
| Rules on parents   | Allowed. Any tag can have rules, with or without children                    |
| Cycles             | Rejected by the daemon when an edge is added (self, direct or indirect)      |
| Filter semantics   | `tag_id` in `StreamFilter` / `SearchRequest` now means "this tag or a descendant". New `tag_exact` on `SearchRequest` for the old meaning |

### Why expand when querying instead of when tagging

The other option is for the tagger to write ancestor tags into `item_tags`
when it assigns a tag (assign `CVE`, also insert `cyber security`).
Expanding when querying is better here:

- **Changes to the tree apply at once and to old items too.** If
  `linux security` is moved under `cyber security`, old items show up under
  the new parent with no backfill. Removing an edge leaves no stale rows to
  clean up.
- **`item_tags` keeps one meaning**: "a rule matched this item". Chips,
  `TestTagRule` and the delete confirmation's count still make sense.
- **The tagger does not change.**

The cost is a recursive CTE on each filtered query, and a descendant lookup
in the Hub on each pushed item. With a few dozen tags both are negligible
(see [Performance](#performance)).

## Data model

Migration `000005_tag_parents` (in `internal/server/db/migrations/` **and**
the copy in `migrations/`):

```sql
-- up
CREATE TABLE tag_parents (
    child_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    parent_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (child_id, parent_id),
    CHECK (child_id <> parent_id)
);
-- The subtree CTE walks parent → children; the PK only serves child → parents.
CREATE INDEX idx_tag_parents_parent_id ON tag_parents (parent_id);

-- down
DROP TABLE tag_parents;
```

- `ON DELETE CASCADE` on both columns means deleting a tag removes its
  edges in both directions. A child left with no parent rows is top-level by
  definition, so "children become top-level" needs no code. (Foreign keys
  are already on: `PRAGMA foreign_keys=ON` in `db.go`.)
- The `CHECK` stops self-loops. Longer cycles are checked in Go (below).
  The CTE also uses `UNION`, not `UNION ALL`, so it terminates even if a
  cycle ever gets into the table.

### Subtree query

One shared SQL fragment in `internal/server/db/tags.go`:

```sql
WITH RECURSIVE subtree(id) AS (
    SELECT ?
    UNION
    SELECT tp.child_id FROM tag_parents tp JOIN subtree s ON tp.parent_id = s.id
)
SELECT id FROM subtree
```

`ListItems` changes its tag condition from

```sql
i.id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)
```

to

```sql
i.id IN (SELECT item_id FROM item_tags WHERE tag_id IN (<subtree>))
```

unless `ItemFilter.TagExact` is set. `IN` dedups, so an item tagged both
`CVE` and `cyber security` appears once. `idx_item_tags_tag_id` still
serves the lookup.

### New db functions (`internal/server/db/tags.go`)

| Function                                   | Purpose                                                     |
|--------------------------------------------|-------------------------------------------------------------|
| `ListTagEdges(db) ([]TagEdge, error)`      | All `(child, parent)` rows; used by `ListTags` and the Hub's graph |
| `TagDescendants(q, id) ([]int64, error)`   | The subtree CTE (includes `id` itself)                       |
| `SetTagParents(db, childID, parentIDs)`    | Replace a tag's parent set **in one transaction**: cycle check, delete old edges, insert new ones |

`Tag` gets `ParentIDs []int64`. `ListTags` fills it from one extra
`ListTagEdges` query (no N+1).

**Cycle check**: adding parent `P` to tag `C` makes a cycle iff
`P == C` or `P` is in `TagDescendants(C)`. `SetTagParents` runs the check
and the writes in one transaction. The daemon uses one connection
(`SetMaxOpenConns(1)`), so transactions run one at a time and two
concurrent edits cannot each pass the check and together form a cycle.
A violation returns a typed `ErrTagCycle` that the service maps to
`InvalidArgument`, with the path in the message (`"CVE" is already an
ancestor of "cyber security"`).

## Protocol (`proto/nyttig/v1/nyttig.proto`)

```proto
message Tag {
    int64  id    = 1;
    string name  = 2;
    string color = 3;
    repeated int64 parent_ids = 4;   // direct parents; empty = top-level
}

message AddTagRequest {
    string name  = 1;
    string color = 2;
    repeated int64 parent_ids = 3;
}

// A repeated field cannot be optional in proto3, so the parent set is wrapped
// in a message to give it presence: unset = unchanged, set (even to an
// empty list) = replace.
message TagParents {
    repeated int64 ids = 1;
}

message UpdateTagRequest {
    int64           id      = 1;
    optional string name    = 2;
    optional string color   = 3;
    optional TagParents parents = 4;
}

message SearchRequest {
    // ... existing fields ...
    bool tag_exact = 8;   // match tag_id only, not its descendants
}
```

`StreamFilter.tag_id` and `SearchRequest.tag_id` keep their field numbers.
Only their meaning grows. Old clients keep working, and a tag with no
children filters exactly as before.

Rejected alternative: separate `AddTagParent` / `RemoveTagParent` RPCs.
Replacing the whole set matches the existing patch style of `UpdateTag`
and the web form (a multi-select), and it is one transaction.

Then `buf generate` (never hand-edit `internal/proto/`).

## Daemon (`internal/server/service`)

- **`AddTag`**: inserts the tag and its parents in one transaction
  (`InsertTag` + `SetTagParents`). Unknown parent ID → `NotFound`.
- **`UpdateTag`**: if `parents` is set, calls `SetTagParents`.
- **`ListTags`**: returns `parent_ids`.
- **`validate.go`**: at most **16 parents per tag**. No duplicates; parent
  IDs must be > 0.
- **Search**: passes `TagExact` through to `ItemFilter`.

### Hub: matching pushed items

`itemMatchesFilter` today checks `t.Id == filter.TagId` against the item's
tags. With the tree it has to ask "is any of the item's tags in the
filter tag's subtree?".

- The `Service` keeps a **tag graph snapshot** (`parent → children` map) in
  an `atomic.Pointer`. It is loaded at startup and rebuilt after every
  successful `AddTag`, `UpdateTag` and `RemoveTag`.
- `itemMatchesFilter` walks the snapshot from `filter.TagId` (breadth-first,
  with a visited set) and checks the item's tags against the result. This
  is in memory and fast for a few dozen tags. It could be memoized per
  snapshot if that ever matters.
- It must stay **consistent with `ListItems`**. A shared test pushes items
  through both paths with the same tree and filter and compares the
  results.

**Known gap**: an open stream filtered on `cyber security` does not get
*historical* items resent when a new child is attached. New pushes match
right away. Old items appear when the client sends its filter again (any
filter change, or a reload). This matches how other changes to
tags/rules behave today, so it is accepted.

## Config seeding (`cmd/nyttigd/main.go`, `internal/config`)

```toml
[[tags]]
name = "cyber security"
color = "#D7263D"

[[tags]]
name = "CVE"
parents = ["cyber security"]

[[tags]]
name = "linux security"
parents = ["cyber security", "linux"]
```

- Two passes: (1) insert all tags as today, (2) add parent edges. Parents
  can be declared after their children in the file.
- Seeding stays **additive**, like the rest of it. Missing edges are added
  and edges are never removed, so a parent set in the UI survives a
  restart.
- A parent name that is not declared is created (the same as a rule that
  names an unknown tag today).
- An edge that would make a cycle is skipped with a warning. Startup does
  not fail.
- `sample_config.toml` gets this example. A test loads that file, so it
  must stay valid.

## CLI (`cmd/nyttig/main.go`)

- `add-tag -n CVE -parent "cyber security"`: `-parent` is repeatable and
  takes a name or ID, resolved like `-tag` in `add-tag-rule`.
- `update-tag ... -parent X -parent Y` replaces the parent set.
  `-no-parents` makes the tag top-level.
- `list-tags` prints the tree indented. A tag with several parents appears
  under each one, marked `(also under: linux)`.
- `search -tag` includes descendants. New `-exact` flag.

## TUI (`internal/tui`)

- `FilterBar.SetTags`: order the dropdown **depth-first from the roots**,
  with each tag shown **once** at its first position, indented by depth.
  The `t` key keeps cycling in that order.
- The filter bar label shows how many tags the filter covers:
  `tag: [cyber security +2]`.
- Item chips do not change (direct tags only).
- `TagInfo` gets `ParentIDs` and `Depth`. The tree flattening is a pure
  function with table tests.

## Web (`web/`, `internal/api`)

**API (`internal/api/manage.go`)**

- `tagFields` gains `parent_ids` (a list of ID strings, validated like other
  IDs). `POST /api/tags` passes them on. `PATCH /api/tags/{id}` maps
  `parent_ids` → `UpdateTagRequest.parents` (present = replace; `[]` = make
  top-level).
- `GET /api/items` accepts `tag_exact=1`.

**App**

- `types.ts`: `Tag.parent_ids?: string[]`.
- New pure module `lib/tagtree.ts`: `buildTree(tags)`, `flatten(tree)`,
  `descendants(id)`, `wouldCycle(child, parent)`, with Vitest tests. Used by
  every view below. The daemon is still the authority on cycles; the client
  check only greys out invalid options.
- `TagForm.svelte`: a **parents** multi-select. It hides the tag itself and
  its descendants.
- `routes/tags/+page.svelte`: rows indented as a tree. A tag with several
  parents shows under each, with repeats muted.
  - **Delete confirmation**: the item count must use `tag_exact` (it
    counts the assignments this delete removes, not the subtree). It also
    says "_N child tags become top-level_" for children that have no
    other parent.
- `Picker` / `FilterSheet` tag lists: tree order + indentation (via
  `flatten`).
- `query.ts`: no syntax change. The help text for `tag:<name>` gets
  "includes child tags". Name resolution and autocomplete stay as they are.
- `FeedRow` chips: unchanged.

## Performance

- The subtree CTE runs over `tag_parents`, which has as many rows as there
  are edges (tens). Its cost is noise next to the `item_tags` lookup it
  feeds, and that lookup uses the existing `idx_item_tags_tag_id`.
- The Hub walk is an in-memory BFS over the snapshot on each pushed item ×
  subscriber. That is microseconds at this scale. Memoize per snapshot only
  if profiling says so.

## Phases (one PR each)

1. **Schema + db layer.** Migration (both copies), `tag_parents` functions,
   cycle check, `ListItems` subtree expansion + `TagExact`. Tests: diamond
   (`linux security` under two parents appears once), deep chain, cycle
   rejection (self/direct/indirect), delete-parent leaves children
   top-level with their `item_tags` intact.
2. **Proto + service + Hub.** Fields above, `buf generate`, validation,
   graph snapshot and `itemMatchesFilter`, the ListItems-vs-Hub
   consistency test.
3. **Config seeding + CLI.** `parents` in TOML, two-pass seed, `-parent`
   / `-no-parents` / `-exact`, tree output in `list-tags`.
   `sample_config.toml`, README.
4. **TUI.** Tree-ordered dropdown, `+N` label.
5. **Web.** API fields, `tagtree.ts`, form, tags page, delete-count fix,
   pickers, help text. Vitest + one Playwright e2e (create parent → filter
   shows a child's items).
6. **Docs.** README tag section, `AGENT.md` layout notes.

After phase 1 the backend does everything (edges can be added through the
config in phase 3). The later phases only expose it in each client.

## Out of scope (possible follow-ups)

- An "exact" toggle in the TUI/web filter (the API supports it from
  phase 2).
- Resending history to open streams when the tree changes.
- Showing ancestor chips or path chips on items.
- Rules that apply only within a subtree.
