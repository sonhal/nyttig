# Article archive plan

Status: **phases 1-4 merged on `feat/article-archive`; phase 5 (web) in progress**.

You see an interesting item and press a key: the daemon downloads the
article behind its link and keeps a **self-contained copy** (one HTML file
with its stylesheets, images and fonts inlined) on disk. Any client can
list archives and download them; the CLI saves them to a file. Archiving is
**best effort**: when a page can't be saved, the archive is marked
`failed` with the reason, and you can retry.

## What works and what doesn't

The daemon fetches with plain HTTP, as it does for feeds. That saves
server-rendered pages (most news sites and blogs). It can't get past:

- paywalls (you get the teaser),
- pages that render only with JavaScript (you get an empty shell),
- bot protection (Cloudflare challenges: a 403 or a challenge page),
- cookie and consent walls, which are common on European sites: the
  fetch succeeds but stores the consent page.

The last two can look like success. A page with little visible text gets a
`note` ("little text; may be a consent wall or paywall") so the client can
show a warning, but no heuristic catches everything. A headless browser
would get past JavaScript and some consent walls; it is out of scope (see
Decisions).

## Decisions

| Topic | Decision |
|---|---|
| What is stored | One **self-contained HTML file** per archive, in the style of SingleFile and monolith: stylesheets inlined as `<style>`, images, fonts and CSS `url()`s as `data:` URIs, scripts removed. A `application/pdf` link is stored as the PDF itself. Any other content type is `failed` |
| No headless browser | No Chromium. It is a heavy dependency, adds risk on a VPS and doesn't fit the read-only containers. It can be a later phase behind a flag if plain HTTP saves too little |
| Making the copy inert | Remove `<script>`, `<iframe>`, `<frame>`, `<object>`, `<embed>`, `<base>`, `<meta http-equiv>` (refresh, CSP) and every `on*` attribute; drop `javascript:` URLs. Write our own first child of `<head>`: `<meta charset="utf-8">` and `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline' data:; font-src data:; media-src data:">`, so a copy opened from disk loads nothing from the network and runs nothing. A comment at the top records the original URL and the archive time |
| Links in the copy | `href`s are made absolute against the final URL, so links still lead to the live site |
| Lazy images | `data-src`, `data-srcset`, `data-lazy-src` and `data-original` replace `src` / `srcset` when `src` is missing or a placeholder `data:` URI. `<noscript>` content is parsed as markup (`html.ParseOptionEnableScripting(false)`) and unwrapped, since it often holds the real `<img>` |
| `srcset` and `<picture>` | Pick one candidate (the largest `w` at or under 1600, else the smallest; for `x` descriptors the largest at or under 2x), set it as `src`, drop `srcset`, `sizes` and the `<source>` elements |
| Media | `<video>`, `<audio>` and `<iframe>` embeds are replaced by a link to their source; they are never downloaded |
| Charset | The page is converted to UTF-8 with `golang.org/x/net/html/charset` (already a dependency), as feeds are |
| Asset checks | An image must be `image/*` by header or by `http.DetectContentType`; a stylesheet must be `text/css` or sniff as text. Anything else is dropped and counted as missing. `@import` is followed one level deep |
| Limits | Page HTML ≤ 10 MiB, each asset ≤ 8 MiB, ≤ 300 assets, total (before compression) ≤ `archive_max_bytes` (default 50 MiB), 4 asset fetches in parallel per page, the whole job ≤ 3 minutes. Assets are fetched in priority order (stylesheets, images in document order, then fonts) until the budget runs out |
| HTTP client | The fetcher's shared client (`fetcher.NewHTTPClient`): the 30 s timeout and, with `block_private_addresses`, the private-address block on the resolved IP for **every** request, the page and each asset alike. Don't add a second client |
| Statuses | `pending` → `running` → `done`, `partial` (the page is saved but `missing_assets` > 0) or `failed` (`error` says why: HTTP status, content type, too large, timeout, blocked address). `note` warns when a saved page has under 500 characters of visible text |
| Retries | None automatic. Archiving the item again re-queues it; the old file stays until the new copy succeeds |
| One per item | At most one archive per item (unique `item_id`). Archiving again replaces the copy; there are no revisions |
| Archives outlive items | Removing a source cascades to its items; an archive must survive that, since it is a backup. The `archives` row copies `link`, `title` and `source_name`, and `item_id` is `ON DELETE SET NULL` |
| Storage | **Files on disk**, gzip-compressed: `<archive_dir>/<archive id>.html.gz` (`.pdf.gz` for PDFs). `archive_dir` is a config key and `--archive-dir` flag, defaulting to `archives/` next to `db_path`, which puts it in `/var/lib/nyttig` under systemd and in the `nyttig-data` volume under compose without other changes |
| Writing files | Write to a temporary file in `archive_dir`, fsync, rename, then update the row. On startup the daemon reconciles: `running` rows go back to `pending`; `done` / `partial` rows without a file become `failed` ("file missing"); files matching the naming pattern without a row are deleted. `RemoveArchive` deletes the row, then the file |
| The queue | The `archives` table is the queue, so pending work survives a restart. `archive_workers` (default 2) workers take `pending` rows oldest first; `ArchiveItem` wakes them through a channel |
| Live status | `Item` gets `ArchiveInfo archive = 14` (id, status). The daemon calls `Hub.PushUpdate` when an archive is queued and when it finishes (not while it runs), so the TUI and web rows update over the existing `item_update` path without any new stream code |
| Downloading | `GetArchiveContent` is a **server stream** of chunks of up to 1 MiB of uncompressed bytes: gRPC's default 4 MiB message limit rules out one message |
| Web safety | An archive is untrusted HTML. Shown on the app's origin it would be stored XSS, so nyttig-api serves it only as a download: `Content-Disposition: attachment` (with the file name sanitized), `Content-Type: application/octet-stream`, `Content-Security-Policy: sandbox; default-src 'none'`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`. The web app never shows an archive inline |
| Triggering | Manual only: `b` in the TUI and the web app (free in both), `:archive` in the web app, `nyttig archive` in the CLI. Archiving by rule (a tag, or a score at or above a threshold) can come later as its own plan |
| Backups | `nyttigd-backup.service` copies only the database today. It also copies `archives/` (`cp -a`, keeping 14 days like the database), and `deploy/README.md` says that archives are part of the state to back up |
| Personal copies | Archives are personal copies of other people's articles. The web app stays behind the proxy's auth; nothing in this feature makes archives public |

## Phase 1: the archiver (`feat(fetcher): build self-contained archive pages`)

A new package, `internal/server/archiver`, with no database access:

- `Archive(ctx, client, url string, limits Limits) (*Result, error)`.
  `Result` holds the content type, the final URL, the HTTP status, the
  bytes (HTML or PDF), `MissingAssets`, `Note` and the visible-text length.
- The package's API is fixed here so phase 2 can be built against it in
  parallel. `internal/server/archiver/api.go` contains exactly this (phase 2
  carries an identical copy plus a stub `Archive`, removed when the phases
  are merged):

  ```go
  package archiver

  import "time"

  // Limits bounds one archive job.
  type Limits struct {
  	MaxPageBytes  int64         // the page itself (10 MiB)
  	MaxAssetBytes int64         // each asset (8 MiB)
  	MaxTotalBytes int64         // everything, before compression (50 MiB)
  	MaxAssets     int           // 300
  	Parallel      int           // asset fetches at once (4)
  	Deadline      time.Duration // the whole job (3 minutes)
  }

  // DefaultLimits returns the limits a job gets unless configured otherwise.
  func DefaultLimits() Limits {
  	return Limits{
  		MaxPageBytes:  10 << 20,
  		MaxAssetBytes: 8 << 20,
  		MaxTotalBytes: 50 << 20,
  		MaxAssets:     300,
  		Parallel:      4,
  		Deadline:      3 * time.Minute,
  	}
  }

  // Result is a saved page: self-contained HTML, or a PDF as fetched.
  type Result struct {
  	ContentType   string // "text/html; charset=utf-8" or "application/pdf"
  	FinalURL      string // after redirects
  	HTTPStatus    int
  	Body          []byte
  	MissingAssets int
  	Note          string // set when the page has little visible text
  	TextLength    int    // visible text, in characters
  }
  ```

  and `archive.go` implements
  `func Archive(ctx context.Context, client *http.Client, pageURL string, lim Limits) (*Result, error)`.
  An error means the archive failed; its message becomes the archive's
  `error`, so it must be short and readable ("HTTP 403", "content type
  image/png is not archived", "page larger than 10 MiB").
- Parse with `golang.org/x/net/html`, apply the inert-copy rules,
  inline the assets, render. CSS `url()` and `@import` rewriting gets its
  own small scanner (strings, comments and escapes), not a regex over the
  whole sheet.
- Tests with `httptest` fixtures (no network): relative and absolute URLs,
  `srcset` and `<picture>` selection, lazy images and `<noscript>`,
  stylesheets with `url()` and `@import`, every removed element and
  attribute, `javascript:` links, `meta refresh`, ISO-8859-1 input, an
  oversized asset (→ `MissingAssets`), the total budget, an asset with the
  wrong content type, a PDF passed through, a 404 and a `text/plain` page
  (→ errors), a short page (→ `Note`), the 3-minute deadline (with a short
  one in the test), and a check that the output has no `<script>` and no
  `on*` attribute for a fixture full of them.

## Phase 2: storage, worker and API (`feat(service): archive items on request`)

- Migration 11 (both directories): `archives` (`id`, `item_id` unique and
  `ON DELETE SET NULL`, `link`, `title`, `source_name`, `status`, `error`,
  `note`, `final_url`, `content_type`, `http_status`, `size`,
  `original_size`, `missing_assets`, `requested_at`, `started_at`,
  `finished_at`; dates in the `utcSecond` text shape). An index on
  `(status, requested_at)`.
- `internal/server/db/archives.go`: enqueue (insert, or reset to `pending`),
  claim the next pending row, finish, list (by status, newest first, paged
  by id), get, delete, and the startup reconcile queries. `ListItems` and
  `Search` join `archives` for `Item.archive`.
- Proto: `ArchiveInfo`, `Archive`, and the RPCs `ArchiveItem`,
  `ListArchives`, `GetArchive`, `GetArchiveContent` (server stream),
  `RemoveArchive`; then `buf generate`. Validation in
  `service/validate.go`: an unknown item or archive is `NotFound`; an item
  whose link is not absolute http(s) is `InvalidArgument`.
- `cmd/nyttigd/main.go`: config `archive_dir`, `archive_workers` and
  `archive_max_bytes` (and flags), creating `archive_dir` with mode 0700,
  the reconcile, the workers, and the push on queue and finish. Update
  `sample_config.toml` and the README's tables.
- `deploy/systemd/nyttigd-backup.service` and `deploy/README.md`: back up
  `archives/`.
- `internal/client`: wrappers for the new RPCs, with `GetArchiveContent`
  writing to an `io.Writer`.
- Tests: db (queue order, claim, reconcile, a source delete keeps the
  archive with `item_id` NULL, `Item.archive` in `ListItems`), service
  (validation, statuses end to end with an `httptest` page and a temporary
  `archive_dir`, a content stream over 1 MiB arrives intact, a push on queue
  and on finish).

## Phase 3: CLI (`feat(cli): archive items and download archives`)

- `nyttig archive <item-id> [-wait]`: queues it and prints the archive's ID;
  with `-wait` it polls until it finishes and exits 1 if it failed.
- `nyttig list-archives [-status failed] [-limit n]`: ID, status, size,
  missing assets, title, and the error or note.
- `nyttig get-archive <archive-id> [-o path]`: by default
  `<slug of the title>.html` (or `.pdf`) in the current directory; refuses
  to overwrite an existing file without `-f`; `-o -` writes to stdout.
- `nyttig remove-archive <archive-id>`.
- README: the subcommands.

## Phase 4: TUI (`feat(tui): archive the selected item`)

- `b` archives the selected item (or re-archives one that failed); on an
  item that is already archived it says so in the status bar, and `B`
  re-archives it anyway.
- A status chip on the row (`⤓` saved, `…` pending or running, `✗` failed;
  `partial` shows as saved with a dim marker), updated from `ItemUpdateMsg`.
- The detail line shows the error or note.
- Tests: the key with a fake client, the chip for each status, an update
  that changes it.

## Phase 5: web (`feat(web): archive items and download archives`)

- nyttig-api (`internal/api/archives.go`): `POST /api/items/{id}/archive`,
  `GET /api/archives`, `DELETE /api/archives/{id}`, and
  `GET /api/archives/{id}/file` with the headers under Decisions (a test
  pins each header). Errors, notes and titles go through `safeText`, links
  through `safeLink`. `Item.archive` is passed through in items and SSE
  updates.
- `keymap.ts`: `b` (archive or retry), in the help. `command.ts`:
  `:archive`. `FeedRow`: the status badge; the expanded row shows the
  error or note and a download link.
- `/archives`: a management page in `ManageView` with status filters,
  download, retry and remove, plus its `PageTabs` entry.
- `types.ts`: `ArchiveInfo` and `Archive`.
- Vitest: the badge for each status and the command. e2e: the feed server
  serves `/article/<name>.html` with a stylesheet and an image; archiving
  from the feed shows the badge, the `/archives` page lists it, and the
  download has `Content-Disposition: attachment` and contains a `data:`
  image and no `<script>`.

## Deviations

Phase 1:

- A PDF is limited by `MaxTotalBytes`, not `MaxPageBytes`, which stays the
  limit for HTML.
- `MaxTotalBytes` bounds the output: it counts the page, stylesheets as fetched, and images and
  fonts at their stored (base64) size, once per inlined use. A use that no longer fits is left out and counted in `MissingAssets`.
- Fields of `Limits` that are zero or negative take their defaults.
- A deadline that expires while assets load returns the page with the rest
  counted in `MissingAssets`; it fails the archive only while the page itself
  loads. A canceled parent context fails with "canceled".
- `@import`s inside an imported sheet are dropped and count as missing.
- Besides the listed elements, `<link>` other than a non-alternate
  stylesheet, `<applet>` and comments are removed, `data:text/html` URLs are
  dropped like `javascript:`, and an honoured `<base href>` resolves relative
  URLs before `<base>` is removed.
- `html.Parse` already refuses nesting past 512 elements; that becomes the
  error "page markup is nested too deeply".

Phase 2:

- The file store and the workers are their own package,
  `internal/server/archives` (`WriteFile`, `OpenFile`, `Reconcile`, `Worker`),
  not code in `cmd/nyttigd/main.go`; main only wires it. The worker takes the
  archive function as a field, so tests use a fake.
- Archiving an item whose archive is `pending` or `running` returns it
  unchanged instead of resetting it; only `done`, `partial` and `failed`
  archives are queued again.
- A re-archive that fails ends `failed` but keeps the older copy: the failure
  only records `error` (and a non-zero `http_status`), and `size`,
  `original_size`, `content_type`, `final_url`, `note` and `missing_assets`
  stay those of the copy. `Archive.size > 0` means a copy can be downloaded,
  whatever the status, and `GetArchiveContent` serves it (`FailedPrecondition`
  when `size` is 0). Reconcile checks every archive with `size > 0`; a missing
  file sets `size` to 0 and turns a `done` or `partial` archive `failed`.
- The archive RPCs return `Unavailable` when the service has no archive
  directory (`Service.SetArchives` not called); the daemon always sets it.
- `Service.PushItemUpdate(itemID)` is exported for the worker's finish callback.
- The backup unit copies `archives/` with hard links (`cp -al -T`) instead of
  `cp -a`, since archive files are only ever replaced by rename, and then
  `touch`es the copy: the 14-day `find` goes by modification time, and the
  copy would otherwise keep the source directory's.

Phases 3 and 4:

- `list-archives` also takes `-before <archive-id>` for paging, like
  `list-digests`.
- `get-archive` needs `size > 0` (a copy exists, even after a failed
  re-archive) and removes a partly written file when the download fails.
- The archive chip sits after the date, not in the gutter. The error or note
  is not in `Item.archive`, so the TUI fetches `GetArchive` once per archive
  status for the selected item and shows it on the `i` line.
- `b` on a `pending` or `running` archive says so instead of calling the
  daemon.

Phase 5:

- Added `GET /api/archives/{id}` (metadata): `Item.archive` has only the ID and
  status, and the expanded row needs the error and note.
- The expanded row has an archive button (retry for a failed archive) for
  touch screens, which have no `b` key.
- `b` on an item that is already archived (`done` or `partial`) only says so;
  saving it again is `r` on the `/archives` page. Pending and running say
  "already archiving".
- Management tools `download` (`o`) and `filter` (`f`); `r` is retry.
- The `/archives` page lists the newest 200 and polls every 3 s while any
  archive is pending or running.
- The e2e test covers the failed path (the stub archiver fails): the badge,
  the error in the row, the page and removal. The feed server's
  `/article/<name>.html` and the download check (`data:` image, no
  `<script>`, `Content-Disposition: attachment`) wait for the real archiver.

## Later

- Archiving by rule (a tag, a score at or above a threshold).
- Full-text search of the archived text.
- Retention or a size quota for `archive_dir`.
- A headless browser, behind a flag, for pages that need JavaScript.
