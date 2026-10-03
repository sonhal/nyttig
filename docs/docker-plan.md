# Docker plan

Status: **phases 1–4 implemented, not yet merged.**

Nyttig can be deployed as three containers, `nyttigd`, `nyttig-api` and
`nyttig-web`, next to the systemd setup in `deploy/README.md` (which stays
the reference VPS guide). The images are built from one `Dockerfile` at the
repository root and run with `deploy/docker/compose.yaml`; CI builds them on
every run and publishes them to GHCR for `v*` tags.

## Decisions

| Topic | Decision |
|---|---|
| nyttigd ⇄ nyttig-api | **nyttigd's plaintext Unix socket in a shared volume** (`/run/nyttig/nyttig.sock`, volume `nyttig-socket`: tmpfs, uid 10001, mode 0700). Only nyttigd and nyttig-api mount it, both as uid 10001; no network carries the plaintext API. No certificates are needed for the web stack. (Phase 4; phases 1–3 used plaintext TCP on an internal network, see "Deviations") |
| TUI from outside | **Opt-in mTLS served by nyttigd itself** (`deploy/docker/compose.mtls.yaml`: `--tls-listen :9090` plus `./tls` mounted read-only), published on `NYTTIG_TLS_PUBLISH`, a host address with **no default** (e.g. `[2001:db8::10]:9090`), because published ports bypass the host firewall. This is the socket + `[tls] listen` split `planListeners` already supports, the same as the systemd setup. Terminating mTLS in a proxy (Caddy) was rejected: a second TLS configuration to maintain, the TUI's long-lived bidi stream through a proxy, and a plaintext TCP hop behind it |
| Reverse proxy | **Bring your own.** Compose publishes nyttig-api on `127.0.0.1:7070` and the web app on `127.0.0.1:7071`, the ports `deploy/Caddyfile` already routes to, so a Caddy (or nginx, Traefik) on the host works unchanged. TLS and basic auth stay the proxy's job |
| Images | **One multi-target `Dockerfile`**: `--target nyttigd`, `nyttig-api`, `nyttig-web`. CI builds all three on every run and pushes `ghcr.io/sonhal/nyttigd`, `nyttig-api` and `nyttig-web` for `v*` tags, with a build provenance attestation |
| Architectures | **linux/amd64 only**, like the release tarball. nyttigd is cgo; arm64 is a follow-up |
| Tags | `<version>` without the `v` (`0.5.0`), `<major>.<minor>`, and `latest` for stable releases (`docker/metadata-action`'s semver rules; a pre-release gets only its full version) |

### Further decisions

| Topic | Decision |
|---|---|
| nyttigd's SQLite | `-tags libsqlite3` and Debian trixie's `libsqlite3-0`, the same build as the release tarball, so one build configuration is tested. In a container the library is patched by **rebuilding the image** (a new release), not by apt on the host |
| nyttigd base image | `debian:trixie-slim` with `libsqlite3-0`, `ca-certificates` (feeds over HTTPS) and `sqlite3` (online backups with `.backup`, as `nyttigd-backup.service` does). Runs as uid/gid 10001 `nyttig`; the database lives in the volume at `/var/lib/nyttig` |
| `nyttig` in the daemon image | Yes, built with `CGO_ENABLED=0`. It is the healthcheck (`nyttig list-sources` over the socket) and the way to use the TUI and CLI on the server: `docker compose exec nyttigd nyttig --socket /run/nyttig/nyttig.sock` |
| nyttig-api base image | `gcr.io/distroless/static-debian13:nonroot`, run as **uid 10001** (nyttigd's) so it may use the socket; the binary is static (`CGO_ENABLED=0`, it never touches SQLite). The entrypoint passes `--listen :7070 --allow-public-listen`: loopback inside a container is unreachable, and the published port is bound to the host's loopback by compose instead |
| nyttig-web base image | Built with `node:22-trixie-slim` and pnpm through corepack (the `packageManager` pin); runs on `gcr.io/distroless/nodejs22-debian13:nonroot` with `build/` and `package.json` only (the build has no runtime dependencies). Listens on 3000 |
| SSRF | `deploy/docker/config.toml` sets `block_private_addresses = true`. It also keeps feed URLs off the compose networks and the host |
| Config | Compose mounts `deploy/docker/config.toml` read-only at `/etc/nyttig/config.toml` and passes `--config`. Its flags (`--socket`, `--db-path`) override the file's `socket` and `db_path` |
| Hardening | Every service: `read_only: true`, `cap_drop: [ALL]`, `no-new-privileges`, a non-root user, `tmpfs: /tmp`, and capped `json-file` logs (10 MB × 3). nyttig-web is on its own network and cannot reach nyttigd |
| `ORIGIN` | One variable, `NYTTIG_ORIGIN` (in `.env`), feeds both nyttig-api's `--origin` and nyttig-web's `ORIGIN`; compose refuses to start without it |
| Version stamp | Build argument `VERSION`, written into `internal/version.Version` like the Build job. The build context has no `.git`, so an image built without it reports `dev` |
| Healthchecks | nyttigd: `nyttig list-sources`. nyttig-web: a `fetch` of `/manifest.webmanifest` with the image's Node. nyttig-api has none: its distroless image has no client, and its Host check would refuse a request to `127.0.0.1` anyway; it reports a lost daemon on its own and reconnects |

### What can reach the plaintext API

- Only processes that can open the socket: nyttigd, nyttig-api, and root on
  the host (root can read any container's files anyway). No container
  network carries it, and the web container doesn't mount the volume.
- The only network entry point is the optional mTLS port, where nyttigd
  requires a client certificate signed by the configured CA
  (`RequireAndVerifyClientCert`).

## Files

```
Dockerfile                      go-build, web-build and the three runtime targets
.dockerignore                   allowlist: go.mod, go.sum, cmd/, internal/, web/ (no node_modules or build output)
deploy/docker/compose.yaml      the three services, three networks, two volumes (data, socket)
deploy/docker/compose.mtls.yaml opt-in override: nyttigd's mTLS port for the TUI
deploy/docker/config.toml       nyttigd's config for the containers
deploy/docker/.env.example      NYTTIG_ORIGIN, NYTTIG_VERSION, and the mTLS opt-in
scripts/gen-certs.sh            IPv6 and several names in the server certificate
deploy/docker/README.md         the guide: run, proxy, TUI, backups, upgrades
.github/workflows/ci.yml        Images job (build), Publish images job (v* tags)
```

## Phases (one commit each; conventional subjects)

1. **`build: add Dockerfile for nyttigd, nyttig-api and nyttig-web`** —
   the `Dockerfile` and `.dockerignore`. Check: each target builds;
   `nyttigd -version`, `ldd nyttigd` shows `libsqlite3`; the containers start.
2. **`feat(deploy): docker compose setup`** — `deploy/docker/`, plus the
   README, `deploy/README.md` (a pointer) and `AGENTS.md` (layout, CI
   table, sandbox notes). Check: `docker compose up` with a local feed
   server; a source added through `/api/sources` is fetched and its items
   come back from `/api/items`; the app answers on 7071; the TUI runs via
   `exec`.
3. **`ci: build images and publish them to GHCR on release tags`** — an
   `images` matrix job (`needs: [lint, test, web]`, build only, GHA cache)
   and a `publish-images` job on `v*` tags after every other job, which
   rebuilds from the cache, pushes with `docker/metadata-action` tags, and
   attests each image (`push-to-registry`). `tag` and `release` also need
   `images`, so a broken Dockerfile blocks a release.
4. **`feat(deploy): shared socket for nyttig-api, opt-in mTLS port for the TUI`**
   — nyttigd serves its plaintext socket in the `nyttig-socket` volume
   instead of TCP `:9000`; nyttig-api mounts it and runs as uid 10001; the
   `daemon` network goes. `compose.mtls.yaml` adds the mTLS listener on
   `NYTTIG_TLS_PUBLISH`. `gen-certs.sh` writes IPv6 addresses as IP SANs
   and takes several names. CI's nyttigd smoke test starts both listeners,
   connects with a client certificate and checks that one from another CA
   is refused. Check: the same compose run as phase 2 over the socket, the
   TUI client from the host over mTLS, nyttig-api recovering after a
   nyttigd restart, the base file alone publishing no 9090.

The PR title is `feat(deploy): ...` so the merge cuts a minor release.

## Out of scope / follow-ups

- **A CI job for the compose stack** (both files, a feed container, the TUI
  client over mTLS), beyond the per-image smoke tests.
- **`NYTTIG_SOCKET` for the client**, so `docker compose exec nyttigd nyttig`
  works without `--socket`.
- **arm64 images** (cross-compiling cgo with `gcc-aarch64-linux-gnu`, or
  native arm64 runners).
- **Scheduled image rebuilds** to pick up base image patches between
  releases, and Dependabot's `docker` ecosystem for the base image tags.
- A backup sidecar or timer; for now the README shows a `sqlite3 .backup`
  run from the host's cron.
- The first push creates the GHCR packages as **private**; the owner makes
  them public once in the package settings.

## Deviations

- The Images job also **smoke-tests** each image (read-only, no
  capabilities, as compose runs them): `-version` for the Go binaries, and
  nyttigd and nyttig-web must reach `healthy`.
- `useradd` without `--system`: uid 10001 is outside Debian's system range,
  and `--system` only adds a warning.
- **Phase 4 replaced the daemon link.** Phases 1–3 connected nyttig-api over
  plaintext TCP on an internal network, which left no way to serve the
  mTLS TUI (`planListeners` refuses a TCP `socket` next to `[tls] listen`)
  and let anything on that network use the API. The socket volume fixes
  both without a daemon change.

## Testing notes

The cloud sandbox can't reach `deb.debian.org`, so the `apt-get` steps were
checked only by CI. Locally the images were built from a scratchpad copy of
the `Dockerfile` (see `AGENTS.md`, "Working in the Claude cloud sandbox") and
the compose stack was run end to end: both healthchecks pass, the CLI works
through `docker compose exec`, a source's items come back through nyttig-api
from the host, a wrong `Host` gets 421, nyttig-web can't resolve `nyttigd`,
and with the bundled config a feed at `169.254.169.254` is refused while
Docker's DNS (127.0.0.11) keeps working.

Phase 4, the same way: nyttig-api serves items over the socket volume and
recovers after `docker compose restart nyttigd`; the nyttig-web container
has no `/run/nyttig`; with `compose.mtls.yaml` a `nyttig` built on the host
lists sources over mTLS on the published port, and CI's smoke script (run
locally) gets `unknown certificate authority` for a client certificate from
another CA. The sandbox has no IPv6, so the mTLS port was published on
`127.0.0.1:9090`; `docker compose config` shows `[::1]:9090` parsed as
`host_ip: ::1`.
