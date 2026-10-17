# Score heat plan

Status: **planned** (phases 1 and 2 in progress on `feat/score-heat`).

When an assessor is selected, and above all when the feed is sorted by score,
it is hard to see at a glance which rows scored high: the only signal is the
`[claude 0.9]` chip among the tag chips. Rows get a **score heat**: a warm
highlight whose strength follows the selected assessor's score, so the top of
a score-sorted feed visibly glows and fades out as scores drop.

The `●` in the first column is the unviewed marker and stays as it is.

## Decisions

| Topic | Decision |
|---|---|
| Which score | The **selected assessor's** highest in-scope score, the same number the `score` sort and the minimum score use (`scoreOf`, with the filter tag's subtree as scope: `tagScope` in the web app, `subtree` in the TUI). Other assessors' chips never add heat |
| When | Whenever an assessor is selected, **in any sort order**. No assessor selected, or no score from it: no heat, rows look exactly as today |
| Heat | `heat = clamp((score − 0.5) / 0.5, 0, 1)`. A score of 0.5 or less gives no heat, so ordinary items stay quiet; 0.75 is half, 1 is full. One function per client (`heatOf`), unit-tested on the same cases: 0 → 0, 0.5 → 0, 0.6 → 0.2, 0.75 → 0.5, 1 → 1, and out-of-range input clamped |
| Color | One hue for all assessors, amber **`#E8A33D`**, not the assessor's own color: heat means "high score", whoever scored it, and assessor colors may be anything (blue, grey) |
| Viewed rows | Heat is **not** dimmed with read rows: a read row that scored high still shows it. Only the row's text is dimmed, as today |
| Selected row | The selection highlight stays visible underneath; heat is layered on it, never replaces it |

## Phase 1: web (`feat(web): ...`)

- `web/src/lib/scores.ts`: `export function heatOf(score: number | null): number`
  (null → 0). Vitest cases as listed above in the existing scores test file.
- `web/src/routes/+page.svelte`: compute the heat scope whenever
  `filter.assessor` is set, independent of the sort (today `scoreScope()`
  returns null unless the sort is `score`; keep that function's meaning for
  the stream and add a separate one for heat). Pass `heat={…}` to `FeedRow`.
- `web/src/lib/FeedRow.svelte`: new prop `heat: number` (default 0). When
  above 0 the row gets `class:hot` and `style:--heat={heat}`, and:
  - a left-to-right gradient as `background-image` (so the `--sel` /
    hover `background-color` still shows beneath it):
    `linear-gradient(90deg, rgb(232 163 61 / calc(0.08 + var(--heat) * 0.27)), transparent 75%)`;
  - a 3px left bar: `box-shadow: inset 3px 0 0 rgb(232 163 61 / calc(0.35 + var(--heat) * 0.65))`.
  - Define the color once as a token in `app.css` (`--heat-rgb: 232 163 61`
    or similar, matching the file's comment style).
  - Works on mobile (44px two-line rows) unchanged.
- Help (`Help.svelte`) and README: one sentence on what the glow means.
- Tests: Vitest for `heatOf`; a component or e2e check (follow the existing
  pattern for score chips) that a scored row of the selected assessor gets
  `hot` and one without a score does not.

## Phase 2: TUI (`feat(tui): ...`)

Terminals have no alpha and the TUI does not know the terminal background,
and Lipgloss v1 backgrounds do not survive the inner color resets of a row's
chips, so the TUI shows heat in the gutter and the title instead of a row
background:

- `internal/tui/assess.go`: `heatOf(score float64, ok bool) float64`, same
  rule and test cases as the web.
- `Table` knows the tag scope as well as the selected assessor (extend
  `SetAssessors`, or add a setter, and set it wherever the filter changes:
  `model.go` already computes `subtree(m.tags, m.filter.CurrentTagID())`).
- The gutter grows from 2 to 3 cells: **heat bar**, unviewed dot, space.
  The bar is `▌` in amber at one of four brightness steps by heat
  (e.g. heat ≤ 0.25, ≤ 0.5, ≤ 0.75, > 0.75, from a dark amber up to
  `#E8A33D`/brighter), or a space when there is no heat. Update `prefixLen`
  and the layout comment so columns stay aligned and rows keep the exact
  width.
- The title is **bold** when heat is above 0.75 (score above 0.875).
- Must still render correctly on the selected row (`rowSelected`).
- README: the row example and the sentence about `●`; the key table entry
  for `a` mentions the heat.
- Tests in `table_test.go`: bar present/absent and stepped by score, no bar
  without a selected assessor, row width unchanged, scope respected (an
  out-of-scope tag assessment gives no heat).

## Deviations

None yet.
