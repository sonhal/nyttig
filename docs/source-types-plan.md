# Source types plan: more feed formats and a CISA KEV source

Status: **plan, not started.** Two phases, one PR each.

nyttig reads RSS 2.0, Atom and Bluesky accounts. This plan adds:

1. **Formats the fetcher should already read** (phase 1): RSS 1.0 (RDF)
   and JSON Feed, both of which fail today with `unrecognized feed format`.
   It also reads `dc:creator` and `<comments>` from RSS 2.0, and documents
   good developer feeds that work with no code at all.
2. **A `kev` source type** (phase 2): CISA's Known Exploited
   Vulnerabilities catalogue, a JSON document rather than a feed, as one
   item per newly added CVE.

The research behind the choices (what was considered and why most of it is
not here) is summarized under [Considered and dropped](#considered-and-dropped).

## Decisions

| Topic | Decision |
|---|---|
| New formats are not new types | RSS 1.0 and JSON Feed are **detected from the document**, like RSS vs Atom today. A source pointing at one is `type = "rss"` (or `atom`; the fetcher ignores the difference). No proto, validation, CLI or web change in phase 1 |
| Detection | The XML root element decides, found with a token scan (`xml.Decoder`), not by string prefix: `rss` → RSS 2.0, `feed` → Atom, `RDF` → RSS 1.0. A body whose first non-space byte is `{` is JSON Feed. A leading UTF-8 BOM is skipped |
| JSON that is not a JSON Feed | An error (`not a JSON Feed: missing version`), not zero items, so a JSON error page from a misconfigured URL shows as a fetch error |
| `dc:creator` | Read in RSS 2.0 and RSS 1.0. It **wins over `<author>`**, which in RSS 2.0 is an e-mail address ("a@b.c (Name)"). Several `dc:creator` elements are joined with ", " (arXiv puts all authors in one, comma-separated) |
| `<comments>` | An RSS 2.0 item's `<comments>` URL (HN, hnrss.org, Lobsters, Slashdot: the discussion thread) is appended to the description as `Comments: <url>`, when it is http(s), differs from `<link>`, and the description does not already contain it. Items have one link; the article stays the link. If `<link>` is empty, the comments URL becomes the link instead |
| Hacker News | **No native type.** HN's own `https://news.ycombinator.com/rss` and hnrss.org (`/newest?points=100`, `/show`, `?q=`) are plain RSS; phase 1's `<comments>` handling is all they need. Points as assessments belong to the assessments track, not here |
| KEV type name | `kev` |
| KEV URL | **Optional.** Blank means CISA's feed, `https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json` (`fetcher.KEVDefaultURL`). Any other http(s) URL is allowed (validated like a feed URL), for a mirror such as `cisagov/kev-data` on GitHub, or a test server. The service stores the default URL in place of a blank one, so the source list shows where it reads from |
| KEV name | Optional; blank means `CISA KEV`, the same pattern as Bluesky's blank name |
| KEV window | **Only entries with `dateAdded` in the last 30 days** are inserted, on every fetch (`kevWindow`, fixed, not a setting). The first fetch adds about a month of CVEs, not the ~1,500-entry history. Dedup by CVE ID keeps refetches idempotent |
| KEV item | GUID = `cveID`. Title = `<cveID>: <vulnerabilityName>`. Link = `https://nvd.nist.gov/vuln/detail/<cveID>`, built only from an ID matching `^CVE-\d{4}-\d{4,}$`; other entries are skipped. Published = `dateAdded` (midnight UTC). Author = empty. Description, plain text: `<vendorProject> <product>. <shortDescription> Required action: <requiredAction> Due: <dueDate>.`, then `Known ransomware use.` when `knownRansomwareCampaignUse` is `Known`, then `Notes: <notes>` when there are notes |
| KEV entries that change | Write-once, like every item: a later edit to an entry (a new due date, ransomware use becoming "Known") is not picked up. Updating stored items is a separate decision for all source types |
| KEV refresh | The default interval (`3600`) is fine; the README suggests `refresh_sec = 21600`, since CISA adds entries a few times a week and the file is about 1.5 MB. Conditional GET (`ETag`) would help every source and is a follow-up |
| Accept header | Grows `application/feed+json, application/rdf+xml` for feeds, `application/json` for `kev` |

## Phase 1: formats (`internal/server/fetcher/fetch.go`, new `jsonfeed.go`)

### Detection

Replace the prefix checks in `parseFeed` with:

```go
// feedKind returns "rss", "atom", "rdf", "json" or "" for a body.
func feedKind(body []byte) string
```

- Skip a UTF-8 BOM and leading whitespace; `{` → `json`.
- Otherwise run an `xml.Decoder` (with `charset.NewReaderLabel`, as
  `decodeXML` does) over the tokens until the first `xml.StartElement`,
  skipping the declaration, comments, a doctype and whitespace. Its local
  name `rss`, `feed` or `RDF` decides. Stop after the first element; don't
  read the document twice to find out what it is.
- An unknown root keeps today's error, `unrecognized feed format`. Drop the
  "try both parsers" fallback: with a real root-element check it can only
  turn a clear error into a confusing one.

### RSS 1.0 (RDF)

```go
type rdfFeed struct {
	XMLName xml.Name  `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# RDF"`
	Items   []rdfItem `xml:"item"` // siblings of <channel>, not inside it
}

type rdfItem struct {
	About       string   `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# about,attr"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Date        string   `xml:"http://purl.org/dc/elements/1.1/ date"`
	Creators    []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
}
```

`item` without a namespace in the tag matches both RSS 1.0's
(`http://purl.org/rss/1.0/`) and RSS 0.90's
(`http://my.netscape.com/rdf/simple/0.9/`) items. GUID = `rdf:about`
(usually the link), date through `parseDate` (it reads `dc:date`'s ISO
8601 already), description through `sanitizeHTML`, text through `cleanText`.

### RSS 2.0 additions

`rssItem` gains `Creators []string` (`dc:creator`) and `Comments string`
(`comments`), applied as in the decisions table.

### JSON Feed 1.0 and 1.1 (`jsonfeed.go`)

Read with `encoding/json` (the body is already capped at 32 MiB):

| JSON Feed field | Item |
|---|---|
| `version` | Must start with `https://jsonfeed.org/version/1`, or the parse fails |
| `items[].id` | GUID. A string in the spec; accept a number too (some 1.0 feeds), via `json.RawMessage` |
| `url`, else `external_url` | Link |
| `title` | Title. Microblog feeds have none: use the first non-empty line of `content_text` (or of the sanitized `content_html`), at most 120 characters, as `blueskyTitle` does. Move that truncation into a shared helper rather than copying it |
| `summary`, else `content_text`, else `content_html` through `sanitizeHTML` | Description |
| `authors[].name` (1.1), else `author.name` (1.0) | Author, joined with ", " |
| `date_published`, else `date_modified` | Published, through `parseDate` |

Every string goes through `cleanText` or `sanitizeHTML`, like the XML
parsers. Items with neither `id` nor `url` are skipped by the existing
`GUID == "" && Link == ""` check.

### Docs

- README: "Fetching" (or the `[[sources]]` table) says which formats are
  read: RSS 0.90/1.0/2.0, Atom 1.0, JSON Feed 1.0/1.1. A new section,
  **Developer feeds that work as-is**, lists URLs: HN
  (`news.ycombinator.com/rss`, hnrss.org with `points`/`comments`/`q`),
  Lobsters (`/rss`, `/t/<tag>.rss`), GitHub (`/<owner>/<repo>/releases.atom`,
  `/tags.atom`), arXiv (`rss.arxiv.org/rss/cs.CR+cs.PL`), PyPI
  (`pypi.org/rss/project/<name>/releases.xml`), crates.io, Mastodon
  (`/@user.rss`, `/tags/<tag>.rss`, instance-local), and newsletters through
  an e-mail-to-Atom bridge (Kill the Newsletter, kill-the-news). Check each
  URL shape against the site while writing it; the sandbox can't reach most
  of them, so say in the PR which were checked.
- AGENTS.md: the fetching note names the formats; the "Adding a source
  type" gotcha says "today `rss`, `atom` and `bluesky`" (it is stale) and
  that a new *format* is detection only, not a type.

### Tests (`fetch_test.go`, `jsonfeed_test.go`)

Inline fixtures, as the existing tests use:

- `feedKind` table: RSS, Atom, RDF (1.0 and 0.90 namespaces), JSON, a BOM
  before each, a comment and a doctype before the root, ISO-8859-1 RDF,
  `<html>` (unknown), empty body.
- RDF: items outside `<channel>`, `rdf:about` as GUID, `dc:date`,
  `dc:creator`, HTML in the description stripped.
- RSS 2.0: `dc:creator` wins over `<author>`; several creators joined;
  `<comments>` appended once, not when equal to the link, not when the
  description already has it, not when it is `javascript:`; used as the
  link when `<link>` is empty.
- JSON Feed: 1.0 (`author`) and 1.1 (`authors`); numeric `id`; no title
  (derived and truncated); `content_html` sanitized; `date_modified`
  fallback; missing or wrong `version` is an error; control characters
  stripped (`TestParseRSS_StripsControlCharacters`'s cases).
- `TestFetch_*` end to end for one RDF and one JSON Feed through
  `FetchWithClient`, so the Accept header and the dispatch are covered.

PR title: **`feat(fetcher): read RSS 1.0 and JSON Feed, dc:creator and comment links`**.

## Phase 2: the `kev` source type

### Fetcher (`internal/server/fetcher/kev.go`)

```go
const TypeKEV = "kev"
const KEVDefaultURL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
const kevWindow = 30 * 24 * time.Hour

var now = time.Now // tests fix the clock

func parseKEV(body []byte) ([]parsedEntry, error)
```

- `buildRequest`: for `kev`, a blank URL means `KEVDefaultURL` (config
  seeding skips the service, as the Bluesky check there notes), and Accept
  is `application/json`.
- `parseBody` dispatches `kev` to `parseKEV`.
- `parseKEV` reads `{"vulnerabilities": [...]}` with the fields `cveID`,
  `vendorProject`, `product`, `vulnerabilityName`, `dateAdded`,
  `shortDescription`, `requiredAction`, `dueDate`,
  `knownRansomwareCampaignUse`, `notes`. A body without a
  `vulnerabilities` array is an error. Entries outside the window, with an
  unparseable `dateAdded` or an invalid CVE ID are skipped (count the
  skipped invalid ones in one `slog.Warn` per fetch, like
  `unrecognized item date`).
- The request goes through the daemon's shared client, so
  `block_private_addresses`, the 30 s timeout and the 32 MiB cap apply as
  for any feed.

### Service (`service/validate.go`, `service/service.go`)

- `validateFeedType` accepts `kev`; its message lists it.
- `validateSourceName`: blank is allowed for `kev` (as for `bluesky`).
- `validateSourceURL`: for `kev`, blank is allowed, anything else goes
  through `validateFeedURL`.
- `AddSource`: for `kev`, a blank URL becomes `KEVDefaultURL` and a blank
  name `CISA KEV`, before the insert (so `sources.url`'s `UNIQUE` sees the
  real URL: a second default KEV source is `AlreadyExists`).
- `UpdateSource`: when the merged source is `kev` with a blank URL, store
  `KEVDefaultURL`; a blank name in a patch on a `kev` source keeps the
  current name, as for Bluesky.

### Clients

The "Adding a source type" places (AGENTS.md):

- `proto/nyttig/v1/nyttig.proto`: the comments on `Source.type` and
  `AddSourceRequest.type` (comment only; `buf generate` still runs and must
  leave the Go output's descriptor comments in step).
- `cmd/nyttig/main.go`: `--type` help on `add-source` and `update-source`;
  `-u` and `-n` are optional for `kev`; the usage line gains
  `nyttig add-source -t kev [-u <url>] [-n <name>]`.
- `web/src/lib/forms.ts`: the `type` union and the coercion in
  `sourceForm` learn `kev`; the URL check allows blank for `kev`; a blank
  name on a `kev` source means "keep" in the patch, as for Bluesky.
- `web/src/lib/SourceForm.svelte`: `<option value="kev">kev</option>`; for
  `kev` the URL placeholder is the default URL and a hint says "leave empty
  for CISA's catalogue; the name is optional".
- `internal/server/scheduler/scheduler.go`: the comment on `Source.Type`.
- README: the `[[sources]]` table, a "CISA KEV sources" subsection next to
  "Bluesky sources" (what an item looks like, the 30-day window,
  write-once, `refresh_sec = 21600`, a tag rule example on
  `description` for vendors you run), and the CLI section.
- AGENTS.md: a short architecture note (one item per newly added CVE,
  window, write-once) and the "Adding a source type" list, which now has
  more than five places: list them all.

### Tests

- `kev_test.go`: a fixture with entries inside and outside the window
  (fixed `now`), an invalid CVE ID, a `Known` ransomware entry, notes,
  control characters and HTML in text fields; the title, link, description
  and date of each; a body without `vulnerabilities` is an error.
- `TestFetch_KEV` through `FetchWithClient` against `httptest`, run twice:
  the second fetch inserts nothing.
- `buildRequest`: blank URL → default, Accept header.
- Service: `AddSource` with blank name and URL stores the defaults; a
  second one is `AlreadyExists`; a non-http URL is `InvalidArgument`;
  `UpdateSource` to blank URL stores the default; the validation table
  gains `kev` rows.
- Web: `forms.test.ts` cases for `kev` (blank URL and name valid, the patch
  keeps the name), and an e2e test in `manage.spec.ts` that adds a `kev`
  source pointing at a fixture the e2e feed server serves
  (`/kev.json`, with `dateAdded` relative to the server's clock so it stays
  inside the window), sees its items in the feed, and deletes the source.

PR title: **`feat: CISA KEV source type`** (fetcher, service, CLI and web).

## Security

- Everything read from a feed or the catalogue is untrusted text: every
  field goes through `cleanText` / `sanitizeHTML`, and clients keep
  rendering text only (`htmlToText`, `safeLink`). The JSON parsers add no
  new rendering path.
- The KEV link is built from a validated CVE ID, not taken from the
  document. JSON Feed and RSS links are stored as RSS links are today; the
  clients' `safeLink` is the guard, unchanged.
- The `<comments>` URL is only used when its scheme is http or https.
- A custom KEV URL is fetched through the SSRF-guarded client like any
  feed URL; a blank one is a fixed public host.
- Size: the existing 32 MiB body cap covers both JSON formats; the KEV file
  is about 1.5 MB.

## Considered and dropped

From the research in the session that produced this plan:

- **Native Hacker News type** (Algolia API): its value was points as
  assessments, which belong to the assessments track. Without that, HN's
  RSS and hnrss.org's filters cover it with no code.
- **Reddit**: unauthenticated `.json` was shut down in 2026, `.rss` is
  throttled hard per IP, and the API needs approved OAuth. Users can add a
  subreddit's `.rss` as an ordinary source with a long interval.
- **NVD CVE API**: very high volume, key-based rate limits, and NVD warns
  that paging can miss records. KEV is the high-signal subset.
- **OSV.dev**: no "modified since" query; incremental sync is a bulk export.
- **GitHub global advisories**: a good follow-up, but it wants an optional
  token, i.e. secrets on sources, which nyttig doesn't have yet.
- **Bluesky custom feeds** (`getFeed`), **WebSub push**, **e-mail (IMAP /
  JMAP)** and **HTML scraping**: separate plans if wanted; RSSHub and
  e-mail-to-Atom bridges cover the last two without code in the daemon.

## Out of scope / follow-ups

- Updating stored items when the source changes them (KEV edits, rising
  points). Today every item is write-once.
- A configurable KEV window (needs per-source options, which sources don't
  have).
- Conditional GET (`ETag` / `If-Modified-Since`) for all sources.
- Several Atom authors, `content:encoded`, enclosures.
- GitHub security advisories as a source type (with a token).

## Verification

Before each push, the AGENTS.md checks: `gofmt -l`, `go mod tidy -diff`,
`go vet ./...`, golangci-lint `--new-from-rev=origin/main` with a
self-installed binary, `go test -race -shuffle=on -tags sqlite_fts5 ./...`,
and for phase 2 `buf generate` with no diff and in `web/` `pnpm check`,
`pnpm test`, `pnpm build`,
`PLAYWRIGHT_CHROMIUM_EXECUTABLE=/opt/pw-browsers/chromium pnpm test:e2e`.
govulncheck can't reach vuln.go.dev from the sandbox; leave it to CI.

By hand, with nyttigd on `sample_config.toml`, serving fixtures from a local
file server (the sandbox blocks most feed hosts):

1. Phase 1: add an RDF fixture and a JSON Feed fixture as `rss` sources;
   both fetch without a fetch error. An hnrss-shaped RSS item shows
   `Comments: <url>` once.
2. Phase 2: `nyttig add-source -t kev -u http://127.0.0.1:<port>/kev.json`
   (with `block_private_addresses` off); recent CVEs appear, old ones
   don't; `nyttig add-source -t kev` stores CISA's URL and the name
   `CISA KEV`.

## Deviations from this plan

None yet.
