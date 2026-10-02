# Assessments plan: scores and notes from external assessors

Status: **phases 1–9 implemented, not yet merged** (database layer, proto and RPCs, live updates, CLI and config, HTTP API, web app, TUI, rating yourself, fetching work by saved view). `main`'s date window (#25) is merged into the branch; assessments and `since:` work together everywhere.

Other systems can attach a judgement to a news item: an optional **score**
from 0.0 to 1.0, an optional **note**, and the **assessor** that made it.
Examples:

- Claude reads items tagged `CVE` and scores how much each matters for that
  tag. A critical (CVSS 10.0) CVE in Cisco IOS gets 1.0. The same severity in
  an obscure router gets 0.3, with a note saying why.
- A deterministic CVE reader looks up the CVE IDs in an item and writes
  `CVSS/10` as its score and `CVE-2026-1234, CVSS 9.8` as its note.
- Later, other models, or you yourself (phase 8).

Each assessor's scores are kept apart. You can show, filter and sort by any
one assessor's scores in the TUI, the web app, the CLI and saved views:

```
tag:CVE score:claude>=0.7 sort:score      Claude's important CVEs, highest first
tag:CVE score:cvss                        CVE news, newest first, with the CVSS reader's scores shown
```

An assessor finds its work through a filter: "items tagged `CVE` that I have
not assessed yet".

## Decisions

| Topic | Decision |
|---|---|
| Name | **Assessment**: one judgement on an item. **Assessor**: a registered system that writes assessments. "Source" already means a feed (`source_id`, `src:`), so it is not reused |
| Shape | `score` (optional, 0–1) and `note` (optional, plain text); at least one of them |
| Scope | Keyed by `(item, assessor, tag)`, with the **tag optional**. NULL means the item as a whole. An item tagged `CVE` and `linux security` can score 0.9 for one and 0.2 for the other |
| Re-assessment | **Upsert.** A new assessment with the same key replaces the old one; `updated_at` says when. No history |
| Structured data | None in v1. Score and note only. A JSON column can come later in its own migration without breaking anything |
| Filtering | Full: minimum score, sort by score and "not assessed by", in `Search`, `StreamItems`, the CLI, the TUI, the web app and saved views, **with live re-evaluation in the Hub** |
| Sort | **Always explicit.** `score:claude` picks the assessor without changing the order; only `sort:score` sorts by score |
| Saved views | A view saves the assessor, minimum score, "not assessed by" and its sort, `score` included |
| Integrations | Core only. Claude, the CVE reader and others are separate clients of the gRPC API or nyttig-api. See [Writing an assessor](#writing-an-assessor) |
| Manual rating | The last phase: a built-in assessor for yourself, set from the TUI and the web app |
| Combining assessors | Never stored or shown merged. Every filter and sort names one assessor |

### Why scores are never combined

Scores from different assessors don't mean the same thing. Claude's 0.7 is
its judgement of importance for a tag. The CVE reader's 0.7 is a CVSS score
of 7.0. Averaging them would produce a number with no meaning. So each
assessor carries a `description` that says what its scale means, the UI
shows each assessor's scores separately (in its color), and every filter or
sort names one assessor. A combined score can come later as an assessor of
its own that reads the others' assessments and writes its own, which keeps it
visible and explainable.

### Why upsert instead of history

Assessments change: a CVE gets exploited in the wild, or a model improves.
Keeping only the latest value keeps the key unique, the queries simple and
the table small. Comparing how an assessor's scores drift over time is a
real use case, but it can come later as an `assessment_history` table that
the upsert appends to. That wouldn't change any query that reads the current
value.

## Data model: migration `000008_assessments` (both migration directories)

Migration 6 is saved views and migration 7 adds `saved_views.since` (the date window). Foreign keys are already on (`db.go`).

```sql
CREATE TABLE assessors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,          -- what the score means; shown in the UI
    color       TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE assessments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id     INTEGER NOT NULL REFERENCES items(id)     ON DELETE CASCADE,
    assessor_id INTEGER NOT NULL REFERENCES assessors(id) ON DELETE CASCADE,
    tag_id      INTEGER          REFERENCES tags(id)      ON DELETE CASCADE, -- NULL = whole item
    score       REAL CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    note        TEXT,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (score IS NOT NULL OR note IS NOT NULL)
);

-- One assessment per item, assessor and tag; NULL tags compare equal here.
CREATE UNIQUE INDEX idx_assessments_key ON assessments (item_id, assessor_id, IFNULL(tag_id, 0));
-- min_score and sort:score look assessments up by assessor and score.
CREATE INDEX idx_assessments_assessor_score ON assessments (assessor_id, score);
-- ON DELETE CASCADE from tags looks rows up by tag_id.
CREATE INDEX idx_assessments_tag_id ON assessments (tag_id);
```

The same migration changes `saved_views`; see
[Saved views](#saved-views).

- **Upsert:** `INSERT ... ON CONFLICT (item_id, assessor_id, IFNULL(tag_id, 0)) DO UPDATE SET score = excluded.score, note = excluded.note, updated_at = excluded.updated_at`.
  Phase 1 must check that SQLite accepts the expression in the conflict
  target. If it doesn't, the fallback is a delete followed by an insert in
  one transaction. A plain `UNIQUE(item_id, assessor_id, tag_id)` is not
  enough, because SQLite treats NULLs as distinct there.
- **Cascades:** deleting an item or an assessor deletes all of its
  assessments. Deleting a tag deletes the assessments scoped to that tag;
  whole-item assessments stay.
- **Dates:** write `updated_at` as UTC with whole seconds, the same text
  shape as `items.published` (see AGENTS.md, "Item dates are text in UTC").
- **The tag doesn't have to be on the item.** A tagger rule can change after
  an item was assessed, and a rule edit shouldn't delete assessments. The
  tag only has to exist.

New `internal/server/db/assessments.go`:

- assessors: `InsertAssessor`, `GetAssessor`, `ListAssessors`,
  `UpdateAssessor`, `DeleteAssessor` (returns whether a row was deleted,
  like `DeleteTag`)
- assessments: `PutAssessment`, `DeleteAssessment` (by key, returns bool),
  `loadAssessmentsByItemIDs` (batched like `loadTagsByItemIDs`)
- `CountViewsUsingAssessor` in `saved_views.go`, next to
  `CountViewsUsingSource` and `CountViewsUsingTag`

## Filter semantics

These rules are shared by `db.ListItems` (SQL) and the Hub's
`itemMatchesFilter` (in memory), and the two must agree.

An assessment by assessor A is **in scope** for a filter when:

- the filter has no tag, or
- the assessment's `tag_id` is NULL (a whole-item assessment), or
- its tag is in the filter tag's subtree (`db.SubtreeSQL`, or the in-memory
  graph in `service/tagtree.go`). With `tag_exact` it must be the filter tag
  itself.

On top of that:

- **`min_score`:** the item has an in-scope assessment by `assessor_id`
  with `score >= min_score`.
- **`sort: "score"`:** order by the highest in-scope score from
  `assessor_id`, descending. Items without such a score go last, and
  `published` / `fetched_at` (newest first) break ties.
- **`unassessed_by`:** the item has no in-scope assessment by that assessor,
  scored or not. This is how an assessor finds work:
  `tag_id=<CVE> unassessed_by=<claude>`.
- `assessor_id` on its own doesn't filter or change the order. It selects the
  assessor whose scores the clients show first and that `min_score` and
  `sort: "score"` use.
- `min_score` or `sort: "score"` without `assessor_id` is `InvalidArgument`.

**The sort is always explicit.** `score:claude` alone doesn't switch to
score order. A view can filter on Claude's scores and stay chronological,
which suits a live feed, where new items arrive without a score.

`TestItemMatchesFilter_AgreesWithListItems` is extended to cover the scope
rules, `min_score` and `unassessed_by` with the tag tree: a parent filter,
`tag_exact`, whole-item assessments and assessments on unrelated tags.

## Protocol (`proto/nyttig/v1/nyttig.proto`)

```proto
message Assessor {
    int64  id          = 1;
    string name        = 2;
    string description = 3;
    string color       = 4;
    google.protobuf.Timestamp created_at = 5;
}

message Assessment {
    int64  id            = 1;
    int64  item_id       = 2;
    int64  assessor_id   = 3;
    string assessor_name = 4;
    int64  tag_id        = 5;  // 0 = the item as a whole
    optional double score = 6;  // unset = no score (0 is a score)
    string note          = 7;
    google.protobuf.Timestamp updated_at = 8;
}

message Item {
    // ... existing fields ...
    repeated Assessment assessments = 13;
}

message AddAssessorRequest    { string name = 1; string description = 2; string color = 3; }
message UpdateAssessorRequest { int64 id = 1; optional string name = 2;
                                optional string description = 3; optional string color = 4; }
message RemoveAssessorRequest { int64 id = 1; }
message ListAssessorsResponse { repeated Assessor assessors = 1; }

// Replaces the whole assessment for (item_id, assessor_id, tag_id): an unset
// score or empty note is stored as NULL. At least one must be given.
message PutAssessmentRequest {
    int64 item_id = 1; int64 assessor_id = 2; int64 tag_id = 3;
    optional double score = 4; string note = 5;
}
message RemoveAssessmentRequest { int64 item_id = 1; int64 assessor_id = 2; int64 tag_id = 3; }

rpc AddAssessor(AddAssessorRequest) returns (Assessor);
rpc UpdateAssessor(UpdateAssessorRequest) returns (Assessor);
rpc RemoveAssessor(RemoveAssessorRequest) returns (google.protobuf.Empty);
rpc ListAssessors(google.protobuf.Empty) returns (ListAssessorsResponse);
rpc PutAssessment(PutAssessmentRequest) returns (Assessment);
rpc RemoveAssessment(RemoveAssessmentRequest) returns (google.protobuf.Empty);
```

Filters (the same field names on all three messages):

- `SearchRequest`, `StreamFilter` and `ViewFilter` gain
  `int64 assessor_id`, `optional double min_score` and
  `int64 unassessed_by`, with field numbers following each message's last
  one.
- `sort` accepts `"score"` in all three.
- `ServerMessage` gains `Item item_update = 4`: an item whose assessments
  changed, with all of them.

Then `buf generate` with the local plugins. Never hand-edit
`internal/proto/`.

## Validation (`service/validate.go`)

- **Score:** reject NaN and ±Inf **explicitly**. A check such as
  `s < 0 || s > 1` lets NaN through, because every comparison with NaN is
  false. Then require 0 ≤ score ≤ 1.
- **Note:** valid UTF-8, at most 4096 bytes. A put needs a score or a
  non-empty note.
- **Assessor:** name via `validateName` (unique: a duplicate is
  `AlreadyExists` through `isUniqueViolation`), color via `validateColor`,
  description at most 500 characters with no control characters.
- **References:** an unknown item, assessor or tag is `NotFound`. Removing an
  assessment or assessor that doesn't exist is `NotFound`; nyttig-api maps
  it to 404.
- **Filters:** `min_score` follows the score rules. `min_score` or
  `sort:"score"` without `assessor_id` is `InvalidArgument`. In a
  `ViewFilter`, an unknown assessor is `NotFound`, like an unknown source or
  tag.

## Saved views

The saved views plan has one rule: a view holds the feed's whole filter and
resolves to a plain filter in the client, so the Hub and `ListItems` know
nothing about views. Assessments keep that rule. The new filter fields
become part of a view like any other field, and **a view saves its sort,
`score` included**. Opening the view restores the order. Changing it marks
the tab `*` (through `sameFilter`), and `:save` writes it back.

**Schema (in migration 8):**

- `saved_views` gains:
  - `assessor_id INTEGER REFERENCES assessors(id) ON DELETE SET NULL`
  - `min_score REAL`
  - `unassessed_by INTEGER REFERENCES assessors(id) ON DELETE SET NULL`
  - indexes on both foreign keys, for the same reason as
    `idx_saved_views_tag_id`
- Its `CHECK (sort IN ('newest','oldest'))` must also allow `'score'`.
  SQLite can't change a CHECK in place, so the migration rebuilds the table:
  1. create `saved_views_new` with the new columns and CHECK
  2. copy every row
  3. drop the old table
  4. rename the new one
  5. recreate the indexes

  Follow the order in SQLite's ALTER TABLE documentation for this (foreign
  keys off around it, as `golang-migrate` runs it). Test that ids, names,
  positions and favorites survive.

**Deleting an assessor keeps the view and drops the field,** the same
decision as for sources and tags. `ON DELETE SET NULL` alone would leave
`min_score` or `sort = 'score'` without an assessor. So a `BEFORE DELETE ON
assessors` trigger also clears `min_score` and turns `sort = 'score'` into
`'newest'` on the views whose `assessor_id` is the deleted assessor. The
`unassessed_by` column is cleared by its `ON DELETE SET NULL`.

**Clients:**

- CLI:
  - `add-view` and `update-view` take `-assessor NAME`, `-min-score X`,
    `-unassessed-by NAME`, `-sort score` and `-no-assessor`
  - `list-views` prints the filter with the new query syntax
  - `search -view` carries the fields over
- nyttig-api:
  - the view filter object `{q, source, tag, sort, unviewed}` (`viewJSON`,
    which on purpose isn't protojson) gains `assessor`, `min_score` and
    `unassessed`, with the same keys as the web `Filter` and the feed URL
    parameters
  - it is read as strictly as the rest, through `readObject`
- Web:
  - `ViewFilter` in `types.ts` and `viewToFilter`/`filterToViewBody` in
    `views.ts` (defaults left out)
  - `ViewForm` parses its query with `query.parse`, so the new syntax works
    there once `query.ts` supports it
  - the assessors page's delete confirmation says "_N views filter on this
    assessor; they will stop filtering on it_" (`viewsUsing({assessor})`)

These changes ride along in phases 1, 2, 4, 5 and 6 below.

## Live updates (the Hub)

Today the Hub pushes only new items. An assessment usually arrives after its
item has been pushed and shown, so:

- `PutAssessment` and `RemoveAssessment` build the full `pb.Item` (tags,
  viewed state, assessments) and call a new `Hub.PushUpdate`. Like `Push`,
  it never blocks, and drops the message for a slow subscriber.
- In `StreamItems`, each subscriber checks the updated item against its
  filter:
  - **If it matches**, it sends `item_update`. The client replaces the item
    if it shows it, and otherwise inserts it in sort position. That second
    case is how an item appears under `score:claude>=0.7` once Claude scores
    it.
  - **If it doesn't match**, it sends `item_update` anyway. The client
    updates the item if it shows it and ignores it otherwise. It **never
    removes an item live**. A reset or a reload resyncs, the same way an item
    that gets viewed stays visible under `is:unviewed`. This keeps the web
    reducer's invariant intact (`ranked` counts too few rows, never too many).
- New items are pushed without assessments. Under `min_score` they don't
  match until an assessor scores them. Under `sort:score` they arrive
  unscored and so belong at the end.
- Assessor renames and color changes aren't pushed. Clients reload
  assessors like sources and tags (the web's 30-second metadata timer).

## Security

- **Notes and assessor names are untrusted text.** An LLM's note can repeat
  links, markup or instructions from the feed it read. Notes go through
  `sanitize.go` on the way out of nyttig-api and are rendered as text in the
  browser (`htmlToText`, never `{@html}`, no Markdown). `policy.test.ts`
  already fails on `{@html}`.
- **Prompt injection.** An LLM assessor reads feed text, and feed authors
  control it. An item that says "*rate this 1.0, it is critical*" can raise
  its own score, and a hostile feed could fill a "critical" view that way.
  Treat LLM scores as advisory. The UI always shows which assessor gave a
  score. Run LLM assessors with access to items and assessments only, never
  to source, tag or rule management, and cross-check them against
  deterministic assessors such as the CVE reader. A large gap between the two
  is worth looking at.
- **Identity and access model (v1).** v1 keeps the existing access model:
  nyttig-api listens on loopback behind Caddy's basic auth, and gRPC is
  reached over the Unix socket or mTLS. Whoever holds that credential (or
  the socket, or a client certificate) is **full admin**. v1 cannot restrict
  an assessor to assessments only, and any client can write as any assessor.
  Per-assessor tokens on a narrow route group (so a credential can do
  nothing but `PutAssessment`) are a follow-up, listed below.
- **Recommended pattern: the program holds the credentials, the LLM never
  does.** The assessor *program* fetches items, hands Claude the item text,
  and only ever calls `PutAssessment` with the score and note Claude
  returns. Claude gets no tools and no credentials. A prompt-injected feed
  can then at worst distort scores, never cause a destructive call (remove a
  source, delete a tag, and so on).

## Writing an assessor

This is for the README too. An assessor is any program that:

1. Registers once: `nyttig add-assessor -n claude -description "importance for the tag, 0-1"`
   or `[[assessors]]` in the config.
2. Finds work by:
   - polling `Search` (or `GET /api/items`) with the tag it covers and
     `unassessed_by` set to itself, or
   - holding a `StreamItems` stream (or `/api/stream`) open with that filter.
3. Calls `PutAssessment` (or `PUT /api/items/{id}/assessments`) for each
   item. With a `tag_id` the score is for that tag; without one it is for the
   item as a whole.
4. Connects:
   - locally over the Unix socket,
   - remotely over mTLS, or
   - through nyttig-api behind the proxy's basic auth.
5. Follows the prompt-injection advice above: the program holds the
   credentials and calls the API; the model only sees item text and returns
   a score and a note.

**Over HTTP (nyttig-api).** Requests must pass nyttig-api's CSRF check
(`csrfCheck` in `internal/api/security.go`). Every state-changing request
(PUT, POST, PATCH, DELETE) needs `Content-Type: application/json` and an
`Origin` header equal to nyttig-api's `--origin`, in addition to the
basic-auth credentials Caddy asks for. Without them the answer is 415 (wrong
content type) or 403 (wrong origin). The assessor and tag are IDs as strings,
like every ID in this API (`GET /api/assessors`, `GET /api/tags`). For example:

```bash
curl -u assessor:PASSWORD -X PUT https://news.example.com/api/items/123/assessments \
  -H 'Content-Type: application/json' \
  -H 'Origin: https://news.example.com' \
  -d '{"assessor": "1", "tag": "5", "score": 0.9, "note": "critical in Cisco IOS"}'
```

Re-assessing is just another `PutAssessment`.

## Phases (one PR each; conventional titles)

1. **`feat(db): assessments and assessors`.**
   - Migration 8 (see the first deviation) in both directories: the two tables, the `saved_views`
     rebuild and the assessor trigger.
   - `db/assessments.go`.
   - `ItemFilter` gains `AssessorID`, `MinScore *float64` and
     `UnassessedBy`, with the scope SQL and the score sort in `ListItems`,
     reusing `SubtreeSQL`.
   - Items carry their assessments (`loadAssessmentsByItemIDs`).
   - `saved_views.go` reads and writes the new columns.
   - Tests:
     - the upsert key, including the NULL tag
     - the expression in the conflict target
     - the CHECKs
     - the cascades
     - scope, `min_score`, `sort:score` and `unassessed_by` with the tag tree
     - the `saved_views` rebuild keeping its rows
     - the assessor trigger
2. **`feat(service): assessments RPCs`.**
   - The proto, `buf generate`, the handlers and the validation.
   - `ItemToProto` with assessments.
   - `Search` and `ViewFilter` take the new fields.
   - The `internal/client` wrapper.
   - Tests:
     - the validation table, including NaN and ±Inf
     - `NotFound` and `AlreadyExists`
     - a wire round-trip like `TestSourceColorSurvivesWire`, including a
       score of 0 against no score
3. **`feat(service): live assessment updates`.**
   - `item_update`, `Hub.PushUpdate`, `StreamFilter` fields, and the scope
     check in `itemMatchesFilter`.
   - The extended agreement test and Hub tests:
     - an update for a matching item
     - an item that starts matching
     - an update for a non-matching item
4. **`feat(cli): assessors and assessments`.**
   - `[[assessors]]` seeding (`name`, `description`, `color`) in
     `internal/config`, `sample_config.toml` and the README.
   - Subcommands: `add-assessor`, `list-assessors`, `update-assessor`,
     `remove-assessor`, `assess <item-id> -assessor NAME [-tag NAME] [-score X] [-note TEXT]`
     and `unassess`.
   - `search -assessor`, `-min-score`, `-unassessed-by` and `-sort score`.
   - The view flags from [Saved views](#saved-views).
   - `search` prints scores in its output.
5. **`feat(api): assessments endpoints`.**
   - Assessor routes: `GET/POST /api/assessors` and
     `PATCH/DELETE /api/assessors/{id}`, using the `jsonBody` presence
     pattern in `manage.go`.
   - Assessment routes: `PUT /api/items/{id}/assessments` (body
     `{assessor, tag?, score?, note?}`) and
     `DELETE /api/items/{id}/assessments?assessor=&tag=`.
   - `/api/items` and `/api/stream` take `assessor`, `min_score`,
     `unassessed` and `sort=score`.
   - An SSE `event: update`.
   - The view filter keys.
   - Notes sanitized in `sanitize.go`.
   - Handler tests against `fake_test.go`.
6. **`feat(web): assessment scores, filters and assessors page`.**
   - `types.ts`: `Assessor`, `Assessment`, `Item.assessments`, and
     `Filter.assessor`/`minScore`/`unassessed`.
   - `metadata.svelte.ts`: `assessors`.
   - `filter.ts`: URL parameters and `sameFilter`.
   - `query.ts`:
     - `score:<assessor>` and `score:<assessor>>=0.7` (assessor names
       resolved like tags, quoted when they contain spaces)
     - `sort:score`, which requires a `score:` term and is an error otherwise
   - `command.ts`: matching `:` commands.
   - `reducer.ts`: `update`. Check how live items are placed under
     `sort:oldest`, and do the same for `sort:score`.
   - `FeedRow`: one score chip per assessor, showing its highest score in its
     color, with the selected assessor first. `ItemDetail` lists every
     assessment: assessor, tag, score, note and age.
   - `routes/assessors` on `ManageView`, with an `AssessorForm`, added to
     `PAGES`.
   - Keys. `1`–`9`, `0` and `v` belong to saved views, and
     `s`/`t`/`S`/`T`/`o`, `r`/`R`, `F` and `D` are taken. Use `a` for the
     assessor picker and `A` to clear it, by analogy with `t`/`T`. They
     appear in `?` through the binding tables.
   - Vitest for `query`, `filter`, `views`, `command`, `keymap`, `reducer`
     and `forms`.
   - Playwright (both viewports; names include the project, everything
     cleaned up):
     - assess an item through the API and see the chip appear live
     - `score:` filter and `sort:score`
     - save a `sort:score` view, press `0`, press `1`, and check the order
       is by score again
     - delete the assessor and check the view survives without the score
       filter
7. **`feat(tui): assessment scores and filters`.**
   - Score chips (or a column) in `table.go`, notes in the detail view, the
     assessor and minimum score in the filter bar (`filter.go`), and
     `item_update` in `updates.go` and `model.go`.
8. **`feat: rate items yourself`.**
   - A built-in assessor named `me`, created on first use.
   - Web: a `:rate <score> [note]` command, plus `=` to prefill it.
     **Not digits**: saved views own those.
   - TUI: the same.
   - Your scores give a ground truth to compare Claude's and the CVE
     reader's against.

9. **`feat(api): fetch items by saved view`.**
   - `GET /api/items?view=<id or name>` and `GET /api/stream?view=...`:
     nyttig-api resolves the view (an ID if a view has it, else the name in
     any case; unknown is 404). The view is the base filter, including
     `since` (turned into `after = now - window`, once per request or stream
     snapshot, with `internal/since`) and the assessment fields.
   - One resolver for both clients: `client.FindView`,
     `client.ViewSearchRequest(view, Overrides, now)` and
     `client.StreamFilterOf`; `nyttig search -view` and nyttig-api use them.
   - **A shared reading view plus parameters**, not a view per assessor: the
     owner's `cve` view (`tag:CVE since:7d`) is also what a daily Claude
     Routine polls. `?view=cve` is everything in the window (re-assess; `PUT`
     replaces), `?view=cve&unassessed=<id>` only new items. A parameter that is
     present replaces the view's field, and present but empty (or `0` /
     `false`) clears it: `unviewed=0`, `min_score=`, `assessor=` (minimum and
     score sort go with it), `unassessed=`, `after=`, `q=`, `tag=`, `source=`,
     `sort=newest`. The CLI: `-unviewed=false`, `-assessor ''`,
     `-unassessed-by ''`, `-tag ''`, `-since ''`, negative `-min-score`.
   - README warns that a view's reading settings apply to the assessor too
     (`is:unviewed` hides what you read; `score:claude>=0.7` hides unscored
     items) and shows the clearing parameters.
   - Tests: handler tests with the fake client (by name, by ID, unknown 404,
     override and clearing precedence, `since` to `after` against a fixed
     clock, the stream), resolver tests in `internal/client`, and
     `e2e/assessor-view.spec.ts` (a view `tag since:7d unassessed:<a>`, assess
     one, it drops out, edit the view, parameters override).

Each phase updates this plan's `Status:` line, the README and `AGENTS.md`
(layout, and an architecture note on assessments next to the tag tree
note), and records deviations below.

## Known gaps (accepted)

- A bookmarked feed or view URL from before this feature has no score
  filter. This is the same staleness the saved views plan accepted.
- An assessment update dropped for a slow subscriber leaves that client
  stale until its next reset. This is the same trade-off `Push` makes.
- An item that stops matching (its score was lowered) stays on screen until
  the next reset.

## Out of scope / follow-ups

- Combined or merged scores (possible later as an assessor of their own).
- Assessment history.
- A structured `data` field (e.g. `{cve, cvss, vendor}`).
- A worker in the daemon that calls the Claude API, and an MCP server for
  assessors.
- Per-assessor credentials: an mTLS certificate or token bound to an
  assessor, on a narrow route group that can only write that assessor's
  assessments. Until then every credential is full admin (see Security).
- Rules that turn a score into a tag ("Claude ≥ 0.8 for `CVE` → tag
  `CVE/critical`"), which would reuse every tag feature.
- Batch `PutAssessments` for assessors that write many at a time.

## Verification

Each phase runs the AGENTS.md checks before pushing:

- `gofmt -l`, `go mod tidy -diff`, `go vet ./...`
- golangci-lint `--new-from-rev=origin/main` with a self-installed binary
- `go test -race -shuffle=on -tags sqlite_fts5 ./...`
- `buf generate` leaves no diff
- in `web/`: `pnpm check`, `pnpm test`, `pnpm build`, and
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium pnpm test:e2e`

Check by hand: run nyttigd with `sample_config.toml`, then:

1. `nyttig add-assessor -n claude`
2. `nyttig assess <id> -assessor claude -tag CVE -score 0.9 -note "..."`
3. Check the chip appears live in the web app and the TUI.
4. Check that `tag:CVE score:claude>=0.7 sort:score` lists it first, and
   that a saved view with that filter keeps the order.

govulncheck can't reach vuln.go.dev from the sandbox, so leave it to CI.

## Deviations from this plan

- **Phase 9, no `since` parameter.** `since` stays out of the HTTP API (the
  date window plan's rule). A view's window is converted in nyttig-api; to
  change it per request use `after=<unix seconds>` or `after=` to drop it.
  Paged reads of a windowed view should pass `after` so the window cannot slide
  between pages.
- **Merge with the date window (#25).** `main` took migration 7
  (`saved_view_since`) and proto fields while this branch was open, so the
  branch's numbers moved (nothing was released):
  - the migration is `000008_assessments`; its `saved_views` rebuild carries
    `since` (`TestMigration8_RebuildKeepsSavedViews` seeds views with a
    window)
  - `ViewFilter`: `since = 6` (main), `assessor_id = 7`, `min_score = 8`,
    `unassessed_by = 9`
  - `SearchRequest`: `after = 9` (main), `assessor_id = 10`, `min_score = 11`,
    `unassessed_by = 12`
  - `StreamFilter`: `after = 7` (main), `assessor_id = 8`, `min_score = 9`,
    `unassessed_by = 10`
  - `Item.assessments = 13`, `ServerMessage.item_update = 4` and
    `update_matches = 5` did not clash.
  - The Hub checks the window before the assessment filters, so an
    `item_update` for an item outside the window has `update_matches = false`
    (`TestAssessmentFilters_WithWindowAgree`).
  - Query syntax order is `since:` then `score:`, `unassessed:`, `sort:`
    (`since:1d score:claude>=0.7 sort:score`).
- **Phase 8, "created on first use" is client-side.** The daemon has no
  special assessor: `client.EnsureMe` (Go: CLI, TUI) and `ensureMe` in
  `web/src/lib/rate.ts` look `me` up by name and add it when it is missing,
  handling the race with another client. Its description and color
  (`#4EC9B0`) are constants in both places.
- **Phase 8, CLI.** `nyttig rate <item-id> <score> [note...]` is added
  (not in the plan) since the CLI already had `assess` and this is the same
  one-liner for `me`.
- **Phase 8, TUI.** The TUI has no command line, so `=` opens a one-line
  prompt (`rate: 0.8 note`) on the line above the status bar. The web `=`
  prefills `:rate `; the phone has no key for it, but `:rate` works wherever
  the command line does. Ratings are for the whole item.
- **Phase 7, no detail view.** The TUI has no detail view for the plan's
  "notes in the detail view", so `i` toggles a line above the status bar with
  the selected item's assessments and notes. The table loses a row while it
  is shown.
- **Phase 7, filter bar.** `a` cycles the assessor, `m` the minimum score
  (none, 0.5, 0.7, 0.9) and `o` gains `score` while an assessor is selected.
  There is no "not assessed by" in the TUI.
- **Phase 7, assessors load once.** The TUI reads the assessors at startup
  with the sources and tags (and tolerates a daemon without the RPC); it does
  not reload them on a timer.
- **Phase 6, the mobile filter sheet.** It gets `score` and `unassessed`
  selects (when assessors exist) and a `score` sort option once an assessor is
  chosen; a minimum score is typed in the query bar. The desktop filter bar
  has a `score:` chip (click or `a` picks the assessor) and shows
  `unassessed:` when set.
- **Phase 6, notes as text.** `htmlToText` reads markup in a note as text
  (`<b>x</b>` shows as `x`), the same as for feed descriptions; the markup
  is never rendered or run.
- **Phase 6, commands.** Besides `:assessors`, `:sort score`, `:score
  <assessor>|all [min]` and `:unassessed <assessor>|all`, the short forms
  `:u` and `:un` are now fixed to `:unviewed` (`unassessed` made them
  ambiguous).
- **Phase 6, update and ordering.** An `update` replaces an item in place and
  never moves it, even under `sort:score` where its score changed. The scope
  of the score sort comes from the tag tree the browser has loaded
  (`stream.setScore`).
- **Phase 5, IDs in request bodies.** `PUT /api/items/{id}/assessments` takes
  `assessor` and `tag` as ID strings (the plan's `{assessor, tag?, score?,
  note?}` did not say), like every other ID in nyttig-api and the view
  filter keys. The curl example in "Writing an assessor" uses IDs.
- **Phase 5, SSE update event.** `event: update` carries `{"matches": bool,
  "item": <item>}` so the web client gets the daemon's `update_matches`
  flag (see the phase 3 deviation).
- **Phase 5, text sanitizing.** `safeText` in `sanitize.go` strips control
  characters (except newline and tab), bidirectional overrides and invalid
  UTF-8 from notes, assessor names and descriptions; markup is left as text
  for the browser's text-only rendering.
- **Phase 4, query syntax.** The CLI writes a view's `unassessed_by` as
  `unassessed:<assessor>` (and `score:<assessor>[>=N]`, `sort:score`); phase 6's
  `query.ts` must read and write the same. The plan listed only `score:` and
  `sort:score`, and a view needs a way to show the unassessed filter.
- **Phase 4, CLI details.** `assess` and `unassess` take the item ID as the
  first argument (or `-item`) because the flag package stops at the first
  non-flag. A numeric assessor reference is an ID when an assessor has it,
  else a name. `update-view` also has `-no-min-score` and
  `-unassessed-by ''` (clear), and `-no-assessor` clears the minimum score and
  turns a `score` sort into `newest`, because the daemon rejects them
  without an assessor. `search` adds a SCORES column only when a listed item
  has a score.
- **Phase 4, sample config.** `sample_config.toml` declares `claude` and
  `cvss` assessors.
- **Phase 3, `ServerMessage.update_matches`.** The plan has the server send
  `item_update` both when the updated item matches the stream's filter and
  when it does not, and has the client insert it only in the first case. The
  client can't evaluate a filter itself (FTS, the tag tree), so the message
  needs a flag: `ServerMessage` gains `bool update_matches = 5`, set only
  with `item_update`. Clients replace an item they show, insert one they
  don't show only when it is true, and never remove.
- **Phase 3, TUI.** `ListenStream` gets an `ItemUpdateMsg` and the model
  keeps reading on it, so a pushed update doesn't stall the TUI's stream
  before phase 7 renders assessments.
- **Phase 3, tag_exact.** `StreamFilter` has no `tag_exact`, so the Hub always
  walks the subtree. The agreement test calls `assessmentsMatch` with
  `exact` set to cover `ItemFilter.TagExact`.
- **Phase 2, StreamItems.** The initial batch (`sendFilteredItems`) already
  honours the new filter fields and validates them in phase 2, so the
  stream's first batch is correct before phase 3 teaches the live path.
