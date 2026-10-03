# Assessor access plan: per-assessor tokens through Caddy

Status: **planned**, no phases implemented. Follows up the "Per-assessor
credentials" item in `docs/assessments-plan.md`.

Today an assessor program that talks to nyttig over HTTP holds the same
credential as the owner: Caddy's one `basic_auth` user, behind which
nyttig-api trusts every request. That credential is **full admin** (sources,
tags, rules, views), and `PUT /api/items/{id}/assessments` takes the
assessor from the body, so any caller can write or delete **any** assessor's
assessments. The program must also fake an `Origin` header to get past
`csrfCheck`.

This plan gives each assessor its own token, checked by Caddy, that can read
the feed and write **only its own** assessments, and nothing else.

```
assessor program ──HTTPS, Basic claude:<token>──▶ Caddy
                                                   │ handle /assessor/api/*
                                                   │   basic_auth (one user per assessor)
                                                   │   uri strip_prefix /assessor
                                                   │   header_up X-Nyttig-Assessor {http.auth.user.id}
                                                   ▼
                                     nyttig-api, assessor listener 127.0.0.1:7072
                                       identity → assessor ID (ListAssessors)
                                       read routes + own assessments only
                                                   │ gRPC, Unix socket
                                                   ▼
                                                nyttigd (unchanged)

browser ──HTTPS, Basic owner──▶ Caddy ── /api/* ──▶ nyttig-api 127.0.0.1:7070 (unchanged)
```

## Decisions

| Topic | Decision |
|---|---|
| Token mechanism | **Stock Caddy `basic_auth`**, one user per assessor. The username is the assessor's name; the password is a random token (32 bytes from `openssl rand`), stored in the Caddyfile only as a `caddy hash-password` hash. No plugins, no custom Caddy build. Caddy issues nothing: the owner generates the token and Caddy checks it |
| Lifetime | **No expiry.** Revoking means removing the user from the Caddyfile and running `systemctl reload caddy`. Rotating means replacing the hash and reloading |
| Routing | **Path prefix on the same host**: `https://<host>/assessor/api/*`. Caddy strips `/assessor`, so nyttig-api sees the usual `/api/...` paths. No new DNS record or certificate |
| Identity hand-off | Caddy sets `X-Nyttig-Assessor: {http.auth.user.id}` on the proxied request and **removes any copy the client sent**, for every route on the site |
| Where it is enforced | **nyttig-api, on a second listener** (`--assessor-listen`, e.g. `127.0.0.1:7072`). Only the allowed routes exist there; everything else is 404. The main listener (7070) is unchanged and ignores the header |
| Read scope | **The whole feed**: `GET /api/health`, `/api/items` (including `?view=`), `/api/stream`, `/api/tags`, `/api/assessors`, `/api/views` |
| Write scope | **Its own assessments only**: `PUT` and `DELETE /api/items/{id}/assessments`. No source/tag/rule/view management, `refresh`, `viewed`, and no assessor create/update/delete |
| Assessor binding | nyttig-api resolves the header to an assessor ID on **every request** (one `ListAssessors` over the Unix socket). The `assessor` field in a PUT body, or `?assessor=` on a DELETE, becomes **optional**: when absent the identity's ID is used; when present it must equal it, or the answer is 403 and the daemon is never called |
| CSRF on the assessor listener | **No `Origin` / `Sec-Fetch-Site` check**: a token sent by a script is not an ambient browser credential. `Content-Type: application/json` is still required for mutations (a cross-site form can't send it without a CORS preflight, and nyttig-api answers no preflight), and the Host check stays |
| gRPC | **Unchanged.** The Unix socket and mTLS stay full admin. Remote assessors use the HTTP route; local ones may use the socket as today |
| Names a token can be issued for | Basic-auth usernames can't contain `:`, and the Caddyfile and HTTP headers are awkward with spaces and non-ASCII. A token can only be issued for an assessor whose name is printable ASCII without `:` or whitespace (`claude`, `cvss-reader`); rename the assessor otherwise. nyttig-api rejects a header value outside that set with 403. Matching is exact and case-sensitive, like `assessors.name`'s `UNIQUE` |
| The `me` assessor | Unchanged: the owner rates from the web app over the main listener. Don't issue a token for `me` |

### Why a second listener rather than a mode on the first

- **Scope by construction.** A token can't call `POST /api/sources` on
  7072 because that route is never registered there. A middleware with a
  deny list on the shared mux would fail open when someone adds a route and
  forgets the list.
- **The two CSRF models stay apart.** The browser listener keeps its
  `Origin` check; the script listener doesn't need one, and neither has to
  guess which kind of caller it is serving.
- **A missing header fails closed.** On 7072 a request without
  `X-Nyttig-Assessor` is refused (403, logged at warn, since it means the
  proxy is misconfigured). On 7070 the header means nothing.

### Trust in the identity header

nyttig-api believes `X-Nyttig-Assessor` because only Caddy should be able to
reach 7072 on loopback. Any **local** process could send the header too, but
a local process can already reach 7070, which has no authentication at all
and is full admin. The header therefore adds no exposure the host doesn't
already have. Hardening for later (out of scope): serve nyttig-api's
listeners on Unix sockets that only Caddy's group can open.

The header must never be trusted on a request that came from outside: Caddy
removes it at the top of the site (`request_header -X-Nyttig-Assessor`), and
`header_up` sets it again only inside the assessor route, after
`basic_auth` has passed.

### Why the token is safe without rate limiting

Caddy has no rate limiting, which is why the owner's password must be long
(see the Caddyfile). A 256-bit random token can't be guessed online at any
rate, so the token's strength doesn't depend on rate limiting. bcrypt adds
nothing for such a token but costs CPU on each check. Caddy's `basic_auth`
caches successful checks; phase 2 verifies that with the Caddy version
`deploy/README.md` installs and records the result here.

## Caddyfile (`deploy/Caddyfile`)

```caddy
nyttig.example.com {
	# Nobody outside may assert an assessor identity.
	request_header -X-Nyttig-Assessor

	# Only /assessor/api/* leaves the owner's basic auth; anything else under
	# /assessor/ stays behind it (it would otherwise reach the app unauthenticated).
	@private not path /manifest.webmanifest /apple-touch-icon.png /icon-192.png /icon-512.png /assessor/api/*
	basic_auth @private {
		sondre $2a$14$...
	}

	header { ... }   # unchanged

	@notstream not path /api/stream /assessor/api/stream
	encode @notstream zstd gzip

	# Assessor programs: one user per assessor; the username is the assessor's
	# name in nyttig. Make a token with scripts/assessor-token.sh.
	handle /assessor/api/* {
		basic_auth {
			claude      $2a$14$...
			cvss-reader $2a$14$...
		}
		uri strip_prefix /assessor
		reverse_proxy 127.0.0.1:7072 {
			header_up X-Nyttig-Assessor {http.auth.user.id}
			flush_interval -1
		}
	}

	handle /api/* { ... }   # unchanged
	handle { ... }          # unchanged
}
```

`handle /assessor/api/*` and `handle /api/*` don't overlap, so their order
doesn't matter. Validate with `caddy validate --adapter caddyfile` before
reloading.

## nyttig-api (`internal/api`, `cmd/nyttig-api`)

- **`api.NewAssessor(cfg Config) (http.Handler, error)`** in a new
  `internal/api/assessor.go`. It shares `handlers` and `streamHandler` with
  `New`, and registers only the routes in the read and write scope above.
  Middleware, outermost first: `securityHeaders`, `hostCheck`,
  `assessorIdentity`, `jsonOnly` (the content-type half of `csrfCheck`,
  split out so both handlers use the same code).
- **`assessorIdentity`** reads `X-Nyttig-Assessor`, checks the character
  set, resolves the name with `ListAssessors` (exact match), and stores the
  assessor's ID and name in the request context. Missing header: 403
  `no assessor identity`. Bad value or unknown name: 403 `unknown assessor`.
  The daemon unreachable: 502, as other handlers map it. The answer never
  echoes the header back.
- **`putAssessment` / `removeAssessment`** get the binding through a helper,
  e.g. `boundAssessor(r, fromRequest int64) (int64, error)`: on the main
  listener (no identity in the context) it returns `fromRequest`, and keeps
  today's "assessor is required"; on the assessor listener it fills in or
  checks. A mismatch is 403 `assessor does not match the token`.
- **Audit log:** each assessment write on the assessor listener logs
  `assessor`, `item_id`, `tag_id` and the method at info. Never the note,
  which is untrusted text.
- **`--assessor-listen`** (default empty = off) on `cmd/nyttig-api`, under
  the same `CheckListenAddr` guard and `--allow-public-listen` as
  `--listen`. When set, `run` serves both listeners with the same
  `http.Server` settings and shuts both down on a signal. The same
  `--origin` applies (same host, so `hostCheck` holds).
- An `assessor` field in the PUT body on the main listener keeps today's
  meaning (the owner may write as anyone). This is pinned by a test so a
  later change doesn't break the web app's `me` ratings.

## Deploy (`deploy/`)

- `nyttig-api.service`: add `--assessor-listen 127.0.0.1:7072` to the
  example `ExecStart` and its comment. `IPAddressAllow=localhost` already
  covers it.
- `deploy/README.md`: a section **Assessor tokens** covering how to issue,
  rotate and revoke a token, the name rule, and `caddy validate`.
- **`scripts/assessor-token.sh <assessor-name>`**: checks the name rule,
  generates the token (`openssl rand -base64 32`, URL-safe), hashes it with
  `caddy hash-password --plaintext` when `caddy` is on the PATH, and prints
  the token once together with the Caddyfile line to paste. The script writes
  nothing to disk.

## Writing an assessor (README and `docs/assessments-plan.md`)

The example becomes:

```bash
curl -u "claude:$NYTTIG_TOKEN" -X PUT https://news.example.com/assessor/api/items/123/assessments \
  -H 'Content-Type: application/json' \
  -d '{"tag": "5", "score": 0.9, "note": "critical in Cisco IOS"}'
```

No `Origin` header, and no `assessor` field, since the token says who you
are. Finding work: `GET /assessor/api/items?tag=5&unassessed=<own id>` (the
ID from `GET /assessor/api/assessors`) or a saved view. The prompt-injection
advice still holds and becomes stronger: the program holds a credential that
can at worst distort **its own** scores.

## Phases (one PR each; conventional titles)

1. **`feat(api): assessor listener with per-assessor identity`.**
   `internal/api/assessor.go`, the `jsonOnly` split of `csrfCheck`, the
   binding in `putAssessment` / `removeAssessment`, the audit log. Tests (fake
   client, table-driven):
   - every route registered by `New` that is not in the scope returns 404 on
     `NewAssessor`. The test enumerates `New`'s routes from one shared list,
     so a new route is out of scope until someone adds it on purpose;
   - a missing header, an empty one, one with `:`, a space or non-ASCII, and
     an unknown name each return 403, and the fake sees no write RPC;
   - a PUT without `assessor` writes as the identity; with the same ID it
     succeeds; with another ID it is 403 and makes no RPC. The same for
     DELETE `?assessor=`;
   - mutations without `Origin` succeed with JSON and get 415 without it;
     the Host check still gives 421;
   - an assessor deleted or renamed after a successful request is refused on
     the next one (resolved per request);
   - `GET /api/stream` and `GET /api/items?view=` work on the assessor
     listener;
   - the main handler ignores `X-Nyttig-Assessor` (it can still write as
     any assessor with no header, and the header doesn't restrict it).
2. **`feat(api): --assessor-listen and Caddy assessor tokens`.** The flag
   and serving two listeners in `cmd/nyttig-api` (flag tests next to the
   existing ones), `deploy/Caddyfile`, `nyttig-api.service`,
   `deploy/README.md`, `scripts/assessor-token.sh`. Manual check, recorded
   here: `caddy validate` on the new Caddyfile; with a local Caddy in front
   of a local stack, show that a token can PUT its own assessment, gets 403
   for another assessor's, 404 on `/assessor/api/sources`, 401 with a wrong
   token, and that `X-Nyttig-Assessor` sent by the client is ignored on both
   `/api/*` and `/assessor/api/*`. Also check whether `basic_auth` caches
   successful checks.
3. **`test(web): assessor route in the e2e proxy`.** `web/e2e/proxy.mjs`
   gains the `/assessor/api/*` route: a fixed test token → name map, strip
   the client's header, set it, strip the prefix, port 7072. `stack.mjs`
   passes `--assessor-listen`. One Playwright test: an assessor writes a
   score over the token route, and the score appears live in the feed; a
   write for another assessor is refused.
4. **`docs: assessor tokens`.** README "Writing an assessor", the
   assessments plan's Security section and follow-up (link here),
   `AGENTS.md` (an architecture note on the two listeners and the header
   trust), and this plan's Status line.

Each phase runs the `AGENTS.md` checks before pushing. govulncheck can't run
in the cloud sandbox; say so in the PR.

## Out of scope / follow-ups

- Token expiry, and minting tokens from the CLI with hashes in the daemon
  (`nyttig assessor-token create`). This replaces the Caddyfile users if
  editing the Caddyfile becomes a chore.
- Per-assessor read restrictions (an assessor may only read `tag:CVE`).
- Per-assessor restrictions on gRPC (mTLS certificate CN → assessor in the
  daemon).
- Unix-socket listeners for nyttig-api, so that only Caddy can reach them.
- Rate limiting per token (needs a Caddy plugin or a limit in nyttig-api).

## Deviations

None yet.
