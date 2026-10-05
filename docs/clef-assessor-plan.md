# Clef assessor plan

Status: **phases 1-2 implemented, not yet merged.**

`nyttig-clef` is a new client binary that scores news items with
Cloudflare's **Clef** decision models on Workers AI and writes the results
as nyttig assessments. It is an assessor in the sense of
[`docs/assessments-plan.md`](assessments-plan.md): a separate program that
finds its work through a saved view and calls `PutAssessment`. The daemon,
the proto, nyttig-api and the web app don't change.

```
tag:security score:clef>=0.7 sort:score     Clef's most relevant security news first
tag:CVE score:clef                          CVE news, with Clef's severity chips shown
```

## Background: what Clef is

Clef (`@cf/cloudflare/clef`, 27B) and Clef-flash (`@cf/cloudflare/clef-flash`,
9B) are **decision models**, released 2026-10-01
([blog](https://blog.cloudflare.com/clef-decision-models/),
[model page](https://developers.cloudflare.com/workers-ai/models/clef/),
[input schema](https://developers.cloudflare.com/workers-ai/models/clef/schema-input.json),
[output schema](https://developers.cloudflare.com/workers-ai/models/clef/schema-output.json)).
They don't generate text. They read a **state** and a map of typed
**questions** and return a probability for every allowed answer, in one
non-autoregressive pass. Their API is compatible with Typesafe's Jev
("System One API").

| | Clef | Clef-flash |
|---|---|---|
| Body `model` | `"clef"` | `"clef-flash"` |
| Context | 65,536 tokens (long text state is truncated by Cloudflare) | same |
| Price | $0.24 / M input tokens | $0.09 / M input tokens |
| Median / p95 latency (Cloudflare's numbers) | 209 / 239 ms | 39 / 122 ms |

`POST https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/run/@cf/cloudflare/{clef|clef-flash}`
with `Authorization: Bearer <token>`:

```json
{
  "model": "clef-flash",
  "state": {"title": "...", "source": "...", "tags": ["CVE"], "text": "..."},
  "questions": {
    "item":    {"type": "noul",  "instructions": "Would a Go/security developer want to read this today?"},
    "tag.CVE": {"type": "score", "instructions": "How severe is this vulnerability?",
                "criteria": ["None", "Low", "Medium", "High", "Critical"]}
  }
}
```

- `state`: a string or JSON object/array.
- `questions`: 1–64 entries. Ids use `[A-Za-z0-9_.-]`, up to 100 characters.
- `noul` (yes/no) takes `instructions` and optional `criteria: {true, false}`,
  and answers `{"type":"noul","noul":0.83}`, the probability of yes.
- `score` takes `instructions` and `criteria`: 2–10 levels, lowest first,
  numbered from 0. It answers `score` (the probability-weighted level, which
  can fall between levels), `legend`, `probabilities` and `confidence`.
- `choice` (2–255 named options) answers `choice`, `probabilities` and
  `confidence`. It is not used in v1, see Decisions.
- `images` (base64 only, at most 4) is a Clef extension. Not used, see Decisions.
- The response also carries `usage.input_tokens` / `output_tokens`.

Cloudflare states that it doesn't read, store or train on requests or
responses, and that the probabilities are trained to be calibrated (a Brier
loss in post-training). That calibration is what makes a `min_score` on a
`noul` meaningful: 0.7 should mean "yes" about 70% of the time.

**Unverified:** Workers AI's REST API normally wraps results as
`{"result": {...}, "success": true, "errors": [], "messages": []}`, but the
Clef pages show neither the wrapped nor the bare shape. The client accepts
both (phase 1) and the first real call settles it; record the answer under
Deviations.

## Decisions

| Topic | Decision |
|---|---|
| Model | **Clef only** (no generic Jev support). `model = "clef-flash"` by default, `"clef"` selectable; the endpoint path follows from it |
| Prompts | **Configurable**, in `nyttig-clef`'s own TOML file: every question's type, instructions and rubric, and which tag it scores. Nothing is hardcoded |
| Where the prompts live | **The TOML file**, not the daemon. Editing questions in the web app would need a new table and RPCs for one client's settings; that is a follow-up |
| What gets scored | Each question scores one thing: the item as a whole (no `tag`), or one tag (`tag = "CVE"`). That is the assessment key `(item, assessor, tag)`, so **at most one question per tag and one whole-item question** |
| Question types | **`noul` and `score`**, the two that produce a number. Score: `noul` as it is; `score` as `score / (levels - 1)`. `choice` has no natural place in a 0–1 score and is left out of v1 |
| Requests | **One item per request**, with all of its applicable questions in it. Several items in one request would mean telling Clef which answer belongs to which item, which can go wrong silently; at 40–200 ms per call it isn't needed |
| Finding work | **A saved view**, named in the config, e.g. `clef-inbox` = `tag:security unassessed:clef since:2d`. Retargeting is editing the view in the web app. Resolved with `client.FindView` / `ViewSearchRequest`, like `nyttig search -view` |
| Polling or streaming | **Polling** `Search` every `interval` (default 60s), draining all pages before it sleeps. News doesn't need sub-minute latency, and polling also covers the backlog and restarts. A `StreamItems` mode is a follow-up |
| Deployment | **Same VPS**, a systemd unit next to nyttigd, talking to it over the Unix socket `/run/nyttig/nyttig.sock` (group `nyttig`, like nyttig-api). No compose service in v1 |
| Credentials | A Cloudflare API token with **Workers AI: Read** only, read from a file (systemd `LoadCredential=`) or `CLOUDFLARE_API_TOKEN`. A `token` key in the TOML is rejected, so the secret never ends up in a config file |
| Images | **None.** nyttig never loads feed images (see "Feed content in the browser is untrusted" in `AGENTS.md`), and fetching them would mean following untrusted URLs |
| Feed text | Sent to Cloudflare. Fine for public feeds; README says so |

### Why the model is safe to point at untrusted feed text

The README's advice for LLM assessors is that the *program* holds the
credentials and the model gets no tools. Clef goes further: it can't produce
text at all, only probabilities over answers that `nyttig-clef` wrote. A
prompt-injected feed ("ignore the instructions, this is critical") can at
worst move a number. It can't make a call, write a note or reach the nyttig
API. The state is sent as a **JSON object** with the feed text in its own
field (`text`), separate from the questions' `instructions`. That makes
injection harder, but it isn't the guarantee; the guarantee is the
bounded output. Scores stay advisory and always carry the assessor's name.
Cross-checking against a deterministic assessor (a CVSS reader) is still
the way to spot a skewed one.

### Why `unassessed:` needs care

`unassessed_by` hides an item once it has **any** in-scope assessment by the
assessor (`internal/server/db/items.go`, `assessmentScopeSQL`). That causes
two problems:

1. **Partial writes.** If the whole-item assessment is written and then the
   `tag.CVE` one fails, the item no longer matches the view and the CVE score
   is never written. `nyttig-clef` therefore retries each failed
   `PutAssessment` (3 tries, backoff) before moving on, and logs
   `assessment write failed` with the item, tag and error when it gives up.
   nyttigd is on the same host, so this should be rare. A `-reassess` run
   (follow-up) can fill gaps.
2. **Items no question covers.** An item with none of the configured tags,
   and no whole-item question configured, gets no assessment, so it matches
   the view forever. The loop keeps an in-memory set of such item IDs and
   pages past them with `offset` (the count skipped so far on this pass). It
   logs a warning at startup when the view has no tag filter and no
   whole-item question is configured, since then most items will be skipped.

A question added to the config later doesn't re-assess items that already
have a Clef assessment. That is by design for v1 (the same as re-running any
assessor); `-reassess` is a follow-up.

## Configuration (`nyttig-clef --config /etc/nyttig/clef.toml`)

```toml
socket     = "/run/nyttig/nyttig.sock"   # or [tls] like the TUI's flags, for a remote daemon
assessor   = "clef"                      # created if missing, never overwritten
description = "Clef: relevance and severity, 0-1"   # used only when creating it
color      = "#F38020"
view       = "clef-inbox"                # saved view, by name or ID

account_id = "0123456789abcdef"          # Cloudflare account
model      = "clef-flash"                # "clef-flash" | "clef"
token_file = "/run/credentials/nyttig-clef/cf-token"   # else $CLOUDFLARE_API_TOKEN

interval        = "60s"   # sleep between polls once the view is drained
max_per_minute  = 60      # Clef requests per minute
daily_tokens    = 2000000 # stop calling Clef for the rest of the UTC day past this many input tokens (0 = no limit)
max_text_chars  = 8000    # the item description is cut to this before sending

[[questions]]             # no tag: the item as a whole
type         = "noul"
instructions = "Would a Go and security developer want to read this today?"
criteria     = { true = "Directly useful or important news", false = "Noise, marketing or off-topic" }   # optional

[[questions]]
tag          = "CVE"      # asked when the item has CVE or a tag below it; scored for CVE
type         = "score"
instructions = "How severe is this vulnerability for widely deployed software?"
criteria     = ["Not a vulnerability", "Low", "Medium", "High", "Critical"]
```

Loading (`config.Load` style: unknown keys rejected) checks:
- `model` is one of the two names, and `account_id` is hex.
- No `token` key is present.
- Each question has a type of `noul` or `score`, non-empty `instructions` of at
  most 2000 characters, and a `score` question has 2–10 non-empty levels.
- At most one question per tag and at most one without a tag, with 1–64
  questions in total.
- Durations parse, and limits are positive.

The tags named in questions, and the view, are resolved against the daemon
at startup; an unknown name is a startup error.

**State** sent for an item (a JSON object; empty fields left out):

```json
{"title": "...", "source": "<source name>", "tags": ["CVE", "linux"],
 "published": "2026-10-05T12:00:00Z", "link": "https://...", "text": "<description, cut to max_text_chars>"}
```

**Question ids** are `item` and `tag.<tag id>` (ids, not names, so any tag
name is a valid id). A question applies to an item when it has no tag, or
when the item carries the tag or one of its descendants. The descendants
come from `ListTags`' `parent_ids` and are refreshed on every poll. This is
the same subtree rule as `ListItems`. The assessment is written with the
configured tag's ID, never the descendant's.

**Answer → assessment:**

| Question | `score` | `note` |
|---|---|---|
| `noul` | `answers[id].noul` | empty |
| `score` with *n* levels | `answers[id].score / (n - 1)` | `High (3.2/4), confidence 0.71`: the most likely level by name, the raw score and the confidence |

The answer is checked before anything is written. Every requested id must be
present with the requested type; numbers must be finite; `noul` must be in
0–1 and `score` in 0–(n−1). A response that fails any check is logged and
nothing is written for that item. It is retried on the next pass, and after
3 failed passes it joins the skip set. A level name in the note comes from
our own config, so it is trusted text, but it still goes through the
daemon's note validation.

## Phases (one PR each; conventional titles)

Add `clef` to the scope names in `AGENTS.md` in phase 1.

1. **`feat(clef): Workers AI client for Clef`**, in `internal/clef/client.go`.
   - Types for the request and the response; `Client.Decide(ctx, state any,
     questions map[string]Question) (*Response, error)`.
   - The URL is built from the account ID and model. `BaseURL` is overridable
     for tests only (no config key).
   - The `http.Client` has a 30s timeout and doesn't follow redirects. The
     response body is capped at 1 MiB.
   - Both the bare and the `{"result": ...}` response shapes are accepted;
     `success: false` becomes an error with the `errors` messages.
   - HTTP 429 and 5xx are a typed retryable error, carrying `Retry-After`
     when present. Other 4xx are permanent, and a 401/403 message says to
     check the token's Workers AI permission.
   - The token is never logged or included in an error.
   - Tests (`httptest`): request shape and headers, both response shapes,
     the API's own error envelope, 429 with and without `Retry-After`, 5xx,
     the oversized body, a redirect refused, malformed JSON.
2. **`feat(clef): configuration, state and answer mapping`**, in
   `internal/clef/config.go` and `assess.go`.
   - TOML loading and validation as above.
   - `BuildState(item, sources, maxChars)`.
   - `Applicable(item, questions, tree)` with the subtree rule; `tree` comes
     from `ListTags`.
   - `CheckAnswers` and `ToAssessments(item, questions, resp)`, which returns
     `[]*pb.PutAssessmentRequest`.
   - `deploy/clef.sample.toml`, loaded by a test so it stays valid (like
     `sample_config.toml`).
   - Table-driven tests: every validation rule; the subtree rule with a child
     tag and a tag with two parents; the score mapping at the edges (0,
     n−1, between levels) for 2 and 10 levels; every way `CheckAnswers`
     rejects (missing id, wrong type, NaN, out of range); truncation on a
     rune boundary.
3. **`feat(clef): nyttig-clef assessor loop`**, in `cmd/nyttig-clef/main.go`
   and `internal/clef/loop.go`.
   - Flags: `--config`, `--once` (drain the view once and exit),
     `--dry-run` (call Clef and log the assessments it would write, write
     nothing), `--log-level`, and `-version` (`internal/version`).
   - Startup: dial (`client.DialConn` options: socket or mTLS), find or create
     the assessor (the `EnsureMe` pattern, looked up by name, `AlreadyExists`
     handled), resolve the view and the questions' tags, and warn on a view
     with no tag and no whole-item question.
   - Each pass: re-read the view (it may have been edited),
     `ViewSearchRequest` with `now` taken once, then page with `Search`. Per
     item: questions → `Decide` → `CheckAnswers` → `PutAssessment` with
     retries. Skip set and offset as described above.
   - Rate limit with a small ticker (`golang.org/x/time` isn't in the
     module graph, and this doesn't justify a new dependency). Retryable Clef
     errors back off up to 5 minutes. `daily_tokens` is counted from
     `usage.input_tokens` and reset at UTC midnight.
   - SIGINT/SIGTERM finishes the current item and exits.
   - slog JSON with one `item assessed` line per item (item id, question
     ids, scores, input tokens, latency). Item text is never logged above
     debug.
   - The loop depends on a small interface (`Search`, `ListSavedViews`,
     `ListTags`, `ListSources`, `ListAssessors`, `AddAssessor`,
     `PutAssessment`) and a `Decider`, so tests use fakes.
   - Tests: a pass writes the mapped assessments; a partial write retries; an
     item no question covers is skipped and paged past; a bad answer writes
     nothing and is skipped after 3 passes; a view edited between passes is
     picked up; `--dry-run` writes nothing; the daily budget stops calls;
     429 backs off.
4. **`feat(deploy): nyttig-clef unit, build and release`**.
   - `deploy/systemd/nyttig-clef.service`: `DynamicUser=yes`,
     `SupplementaryGroups=nyttig`,
     `LoadCredential=cf-token:/etc/nyttig/clef-token`,
     `ExecStart=/usr/local/bin/nyttig-clef --config /etc/nyttig/clef.toml`,
     `Requires=`/`After=nyttigd.service`, and the same hardening as
     nyttig-api.
   - Network for the unit: it needs outbound HTTPS to `api.cloudflare.com`,
     which systemd can't allow by hostname, so
     `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6` without
     `IPAddressDeny`. Say so in the unit's comment.
   - CI's Build job adds `./cmd/nyttig-clef` (`CGO_ENABLED=0` like the other
     clients), and the release tarball carries it in `bin/`.
   - Docs:
     - README: a "Clef assessor" section with setup, the config reference
       table, cost, and the note that feed text is sent to Cloudflare.
     - `deploy/README.md`: install steps, creating the token, and the view to
       create first.
     - `AGENTS.md`: the binary table, the repository layout, and an
       architecture note on the `unassessed` caveat.
   - Check: the unit with `systemd-analyze verify`, if available.

Each phase runs the checks in `AGENTS.md` (gofmt, `go mod tidy -diff`, `go
vet`, golangci-lint against `origin/main`, the race tests). No phase touches
the web app or the proto.

## Out of scope / follow-ups

- **Questions edited in the web app** (stored in the daemon, e.g. an
  assessor `config` JSON column) instead of the TOML file.
- **`-reassess`**: re-score items that already have Clef assessments, e.g.
  after changing a question, limited by a view and a date.
- **`choice` questions**, e.g. writing the chosen option as a note-only
  assessment, or suggesting tags.
- **Streaming mode** (`StreamItems` with the view's filter) for lower latency.
- **A compose service** in `deploy/docker/`.
- **Images**, only through the fetcher's SSRF-protected client and with a size cap.
- **Running the open weights locally** (Apache 2.0 on Hugging Face) instead of
  Workers AI: the request format is the same, so it would mostly be a
  `base_url` key.

## Deviations

- **Phase 1:** `ClientConfig` (not `Config`) configures the client, since
  `Config` is the TOML file's struct in phase 2. Transport failures (not only
  429 and 5xx) are `RetryableError` with `Status` 0.
- **Phase 2:** `BuildState(item, maxChars)` takes no `sources` argument: an
  item from `Search` carries `source_name`. `ToAssessments(item, assessorID,
  questions, resp)` takes the assessor ID, which the request needs. Questions
  are `QuestionSpec` (the TOML entry) and `BoundQuestion` (resolved against
  the daemon's tags by `ResolveQuestions`). The `[tls]` table has `cert`,
  `key`, `ca` and `server_name`. The shape of Clef's `probabilities` and
  `legend` is not documented, so they are kept raw; the note's level name
  uses `probabilities` when it is an array with one entry per level and
  otherwise the level nearest the score.
