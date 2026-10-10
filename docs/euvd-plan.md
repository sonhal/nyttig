# EUVD plan: the EU Vulnerability Database as a source type

Status: **phases 1–3 implemented, not yet merged** (one PR).

ENISA's [EU Vulnerability Database](https://euvd.enisa.europa.eu) (EUVD)
is the European counterpart of NVD and CISA's KEV. It has no RSS or Atom
feed, only a JSON API at `https://euvdservices.enisa.europa.eu/api/`. This
plan adds a `euvd` source type that reads that API, turns each record into
an item, and stores its CVSS and EPSS scores as **assessments**, so the
existing `score:` filters, score sort and saved views work on them.

It is independent of the CISA KEV source type
([source-types-plan.md](source-types-plan.md), phase 2, not started). The
two follow the same pattern (a JSON source type with an optional URL and a
rolling window), and KEV can reuse what this adds when it is built.

## The API (what this relies on)

The sandbox this was written in cannot reach ENISA's hosts, so the shape
below comes from ENISA's API documentation as quoted by third parties
(cku-heise/euvd-api-doc, anchore/vunnel's EUVD provider, the Cortex XSOAR
integration). The parser is lenient where they disagree; check the
assumptions marked **(verify)** against a live response.

- `GET /api/search`: unauthenticated. Query parameters include
  `fromScore`/`toScore` (integers 0–10), `fromEpss`/`toEpss` (0–100),
  `fromDate`/`toDate` (published), `fromUpdatedDate`/`toUpdatedDate`
  (`YYYY-MM-DD`), `exploited`, `vendor`, `product`, `assigner`, `text`,
  `page` (from 0) and `size` (at most 100). The answer is
  `{"items": [...], "total": N}`.
- `/api/lastvulnerabilities`, `/api/exploitedvulnerabilities`,
  `/api/criticalvulnerabilities` return at most 8 records with no paging.
  They are **not supported**: a busy hour would drop records silently.
- A malformed query (a float score, say) is answered with **403**, not 400.
  Some clients' default User-Agents are refused too; nyttig sends its own.
- A record: `id` (`EUVD-2025-11154`), `description`, `datePublished` and
  `dateUpdated` (`"Apr 16, 2025, 7:00:16 AM"`, UTC), `baseScore` (a number;
  **(verify)** that a missing score is absent or outside 0–10, which is
  read as "no score"), `baseScoreVersion` (`"3.1"`), `baseScoreVector`,
  `references` and `aliases` (one string, entries separated by `\n`),
  `assigner`, `epss` (**(verify)**: read as a percentage, 0–100, the scale
  of the `fromEpss` filter), `exploitedSince` (a date when the
  vulnerability is known to be exploited), `enisaIdProduct`
  (`[{product: {name}, product_version}]`) and `enisaIdVendor`
  (`[{vendor: {name}}]`).

## Decisions

| Topic | Decision |
|---|---|
| Type name | `euvd` (`fetcher.TypeEUVD`) |
| What a source reads | **A search query.** The source URL is an `/api/search` URL with the user's filters, e.g. `?exploited=true`, `?fromScore=9`, `?vendor=Microsoft`. Blank means `fetcher.EUVDDefaultURL`, `https://euvdservices.enisa.europa.eu/api/search?exploited=true` (exploited vulnerabilities, the KEV-like default). Any other http(s) URL is allowed (a mirror, a test server); it is validated as a feed URL. The service stores the default in place of a blank URL, so the list shows what is read and a second default source is `AlreadyExists` |
| Name | Optional; blank means `EUVD` |
| Paging and window | The fetcher **sets** `fromUpdatedDate` to 14 days ago (UTC date), `size=100` and `page`, replacing those parameters if the URL has them, and keeps every other parameter. It reads pages until one is empty, shorter than `size`, or the running count reaches `total`, at most **20 pages** (2,000 records). `fromUpdatedDate` rather than `fromDate`, so a record whose score or exploitation changes is read again (see Assessments). The first fetch reads two weeks of history |
| Too many matches | When the cap stops paging before `total`, the items read are inserted and the source's `fetch_error` says `euvd: query matches N records, read the first 2000; narrow the query`, so a too-broad query is visible in every client |
| Item | GUID = the EUVD ID, which must match `^EUVD-\d{4}-\d+$` (other records are skipped, counted in one `slog.Warn` per fetch). Link = `https://euvd.enisa.europa.eu/vulnerability/<id>`, built from the validated ID, never taken from the document. Published = `datePublished`. Author = `assigner`. Title = `<first CVE alias, else EUVD ID>: <first line of the description>`, at most 120 characters (`truncateTitle`) |
| Description | Plain text, one line (`cleanText`): the description, then `EUVD-…, CVE-….` (all IDs), `Vendor: a, b.`, `Product: x 1.2, y.`, `CVSS 9.8 (3.1) CVSS:3.1/….`, `EPSS 0.92%.`, `Exploited since 2026-10-01.`, `References: <up to 10 http(s) URLs>`. Each part only when present. Tag rules match on it (e.g. `Exploited since` or a vendor) |
| Write-once items | As for every source, an item is never rewritten: a later description change is not picked up. Scores are (next row) |
| Scores are assessments | Two assessors, **`euvd-cvss`** (score = `baseScore / 10`, note `CVSS 9.8 (3.1) <vector>`) and **`euvd-epss`** (score = `epss / 100`, note `EPSS 0.92%`). Whole-item assessments (no tag). The fetcher creates each assessor the first time it needs it (`db.EnsureAssessor`, insert-or-ignore then look up by name), with a description; a config `[[assessors]]` entry of the same name is the same assessor. A deleted assessor is created again on the next EUVD fetch, and a renamed one is no longer used (a new one is created): the name is the contract |
| Updating scores | On **every** fetch, for every record read (new items and existing ones), the fetcher writes an assessment only when its score or note differs from the stored one (`db.GetAssessment`), so an unchanged refetch writes nothing. A record without a score leaves an existing assessment alone. Existing items whose assessments changed are returned in `FetchResult.UpdatedItemIDs`, and the daemon pushes them with `Hub.PushUpdate` (as `item_update`, like a client's `PutAssessment`). New items get their assessments before the daemon reloads and pushes them, so the first push already carries the scores |
| Exploitation | In the description (`Exploited since …`), for tag rules; no third assessor. The default query is already exploited-only |
| Refresh | The default interval (3600 s) is fine; the README suggests `refresh_sec = 21600` for a broad query. Pages are read one after another, through the daemon's shared client (`block_private_addresses`, 30 s per request, 32 MiB per page) |
| Accept header | `application/json` |

## Phase 1: fetcher, db and daemon

- `internal/server/fetcher/euvd.go`: `TypeEUVD`, `EUVDDefaultURL`,
  `euvdWindow`, `euvdPageSize`, `euvdMaxPages`, a `now` clock variable for
  tests, `euvdPageURL(raw, page, now)`, `fetchEUVD(client, src)` (paging),
  `parseEUVDPage(body)` (`{items, total}`; a body without `items` is an
  error), `euvdEntry(record)` (item and assessments), `parseEUVDDate`
  (`Jan 2, 2006, 3:04:05 PM`, then `parseDate`'s layouts).
- `fetch.go`: `parsedEntry.Assessments`; `fetchInternal` gets its entries
  from `fetchEUVD` for `euvd` and from the single request otherwise (the
  GET, status check and size cap shared in `getBody`); after inserting,
  `applyAssessments` writes changed assessments and fills
  `FetchResult.UpdatedItemIDs`.
- `db`: `EnsureAssessor(name, description)`, `ItemIDByGUID(sourceID, guid)`.
- `cmd/nyttigd/main.go` `doFetch`: push `UpdatedItemIDs` with
  `hub.PushUpdate`; log their count.
- Tests (`euvd_test.go`): the page URL (blank → default, parameters
  replaced and kept), parsing (dates, aliases, products, vendors, scores,
  exploited, references; invalid IDs skipped; control characters and HTML
  in text), paging against `httptest` (stops on a short page, on `total`,
  at the cap with the fetch error), assessments written once, a changed
  score on a refetch reported in `UpdatedItemIDs`, a non-2xx answer as a
  fetch error. `db` tests for the two helpers.

## Phase 2: service, CLI, proto comment

- `service/validate.go`: `validateFeedType` accepts `euvd`;
  `validateSourceName` and `validateSourceURL` allow blank for `euvd`
  (else `validateFeedURL`).
- `service/service.go`: `AddSource` stores `EUVDDefaultURL` for a blank URL
  and `EUVD` for a blank name; `UpdateSource` stores the default when the
  merged `euvd` source has a blank URL.
- `cmd/nyttig/main.go`: `--type` help; `-u` and `-n` optional for `euvd`.
- `proto/nyttig/v1/nyttig.proto`: the `type` comments; `buf generate`.
- `scheduler.Source.Type` comment.
- Tests: service add/update with blanks, duplicate default, validation
  table rows.

## Phase 3: web app and docs

- `web/src/lib/forms.ts`: `euvd` in the `type` union and the coercion; a
  blank URL and name are valid for `euvd`.
- `web/src/lib/SourceForm.svelte`: the option, the placeholder (the
  default URL) and a hint.
- `forms.test.ts` cases; an e2e test in `manage.spec.ts` against a
  `/euvd/api/search` endpoint on the e2e feed server: add the source, see
  an item with its `euvd-cvss` chip, delete the source.
- README (sources table, an "EUVD sources" section, CLI), AGENTS.md
  (architecture note, the "Adding a source type" list).

PR title: **`feat: EUVD source type with CVSS and EPSS assessments`**.

## Security

- Every text field is untrusted: it goes through `cleanText`, and clients
  keep rendering text only. References are kept only when they parse as
  absolute http(s) URLs, and only as text in the description.
- The item link is built from a validated EUVD ID.
- A custom URL goes through the SSRF-guarded client like any feed URL.
  Paging is bounded (20 pages × 32 MiB cap × 30 s timeout).
- Scores are range-checked (CVSS 0–10, EPSS 0–100) before they become
  assessment scores in 0–1; JSON cannot carry NaN or ±Inf.

## Out of scope / follow-ups

- The CISA KEV type (its own plan).
- The `/api/enisaid` detail endpoint (advisories, per-CVE data): one more
  request per record.
- Removing an assessment when EUVD drops a score; deleting items EUVD
  rejects.
- Per-source options (window, page cap): sources have no options column.

## Verification

The AGENTS.md checks, plus by hand: nyttigd with a source pointing at a
local server serving a recorded `/api/search` answer; `nyttig search
-assessor euvd-cvss -min-score 0.9` lists the critical ones.
