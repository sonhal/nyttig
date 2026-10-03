# Docker plan

Status: **phases 1–3 implemented, not yet merged.**

Nyttig can be deployed as three containers, `nyttigd`, `nyttig-api` and
`nyttig-web`, next to the systemd setup in `deploy/README.md` (which stays
the reference VPS guide). The images are built from one `Dockerfile` at the
repository root and run with `deploy/docker/compose.yaml`; CI builds them on
every run and publishes them to GHCR for `v*` tags.

## Decisions

| Topic | Decision |
|---|---|
| nyttigd ⇄ nyttig-api | **Plaintext gRPC over TCP on an internal compose network** (`nyttigd --socket :9000`). Only nyttigd and nyttig-api join that network (`daemon`, `internal: true`); the port is never published. No certificates are needed for the web stack |
| Reverse proxy | **Bring your own.** Compose publishes nyttig-api on `127.0.0.1:7070` and the web app on `127.0.0.1:7071`, the ports `deploy/Caddyfile` already routes to, so a Caddy (or nginx, Traefik) on the host works unchanged. TLS and basic auth stay the proxy's job |
| Images | **One multi-target `Dockerfile`**: `--target nyttigd`, `nyttig-api`, `nyttig-web`. CI builds all three on every run and pushes `ghcr.io/sonhal/nyttigd`, `nyttig-api` and `nyttig-web` for `v*` tags, with a build provenance attestation |
| Architectures | **linux/amd64 only**, like the release tarball. nyttigd is cgo; arm64 is a follow-up |
| Tags | `<version>` without the `v` (`0.5.0`), `<major>.<minor>`, and `latest` for stable releases (`docker/metadata-action`'s semver rules; a pre-release gets only its full version) |

### Further decisions

| Topic | Decision |
|---|---|
| nyttigd's SQLite | `-tags libsqlite3` and Debian trixie's `libsqlite3-0`, the same build as the release tarball, so one build configuration is tested. In a container the library is patched by **rebuilding the image** (a new release), not by apt on the host |
| nyttigd base image | `debian:trixie-slim` with `libsqlite3-0`, `ca-certificates` (feeds over HTTPS) and `sqlite3` (online backups with `.backup`, as `nyttigd-backup.service` does). Runs as uid/gid 10001 `nyttig`; the database lives in the volume at `/var/lib/nyttig` |
| `nyttig` in the daemon image | Yes, built with `CGO_ENABLED=0`. It is the healthcheck (`nyttig list-sources`) and the way to use the TUI and CLI: `docker compose exec nyttigd nyttig --socket 127.0.0.1:9000` |
| nyttig-api base image | `gcr.io/distroless/static-debian13:nonroot`; the binary is static (`CGO_ENABLED=0`, it never touches SQLite). The entrypoint passes `--listen :7070 --allow-public-listen`: loopback inside a container is unreachable, and the published port is bound to the host's loopback by compose instead |
| nyttig-web base image | Built with `node:22-trixie-slim` and pnpm through corepack (the `packageManager` pin); runs on `gcr.io/distroless/nodejs22-debian13:nonroot` with `build/` and `package.json` only (the build has no runtime dependencies). Listens on 3000 |
| SSRF | `deploy/docker/config.toml` sets `block_private_addresses = true`. It also keeps feed URLs off the compose networks and the host |
| Config | Compose mounts `deploy/docker/config.toml` read-only at `/etc/nyttig/config.toml` and passes `--config`. Its flags (`--socket`, `--db-path`) override the file's `socket` and `db_path` |
| Hardening | Every service: `read_only: true`, `cap_drop: [ALL]`, `no-new-privileges`, a non-root user, `tmpfs: /tmp`, and capped `json-file` logs (10 MB × 3). nyttig-web is on its own network and cannot reach nyttigd |
| `ORIGIN` | One variable, `NYTTIG_ORIGIN` (in `.env`), feeds both nyttig-api's `--origin` and nyttig-web's `ORIGIN`; compose refuses to start without it |
| Version stamp | Build argument `VERSION`, written into `internal/version.Version` like the Build job. The build context has no `.git`, so an image built without it reports `dev` |
| Healthchecks | nyttigd: `nyttig list-sources`. nyttig-web: a `fetch` of `/manifest.webmanifest` with the image's Node. nyttig-api has none: its distroless image has no client, and its Host check would refuse a request to `127.0.0.1` anyway; it reports a lost daemon on its own and reconnects |

### Trade-offs of plaintext TCP

- The gRPC API is unauthenticated and has full write access. Containers
  outside the `daemon` and `feeds` networks can't reach it, but **root on the
  host** (and anything that can join those networks) can, through the
  container's IP. The systemd setup's socket is limited to the `nyttig`
  group; here the boundary is the host itself.
- `nyttigd` refuses `[tls] listen` with a TCP `socket` (`planListeners`), so
  the remote mTLS TUI of `deploy/README.md` isn't available in this setup.
  Use `docker compose exec`, or publish 9000 on the host's loopback and
  tunnel it over SSH (commented out in `compose.yaml`).

## Files

```
Dockerfile                      go-build, web-build and the three runtime targets
.dockerignore                   allowlist: go.mod, go.sum, cmd/, internal/, web/ (no node_modules or build output)
deploy/docker/compose.yaml      the three services, four networks, one volume
deploy/docker/config.toml       nyttigd's config for the containers
deploy/docker/.env.example      NYTTIG_ORIGIN, NYTTIG_VERSION
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

The PR title is `feat(deploy): ...` so the merge cuts a minor release.

## Out of scope / follow-ups

- **mTLS TUI next to the plaintext TCP listener.** Let `planListeners` accept
  a TCP `socket` together with `[tls] listen` behind an explicit opt-in
  (e.g. `allow_plaintext_tcp = true`), so the compose setup can serve the
  remote TUI on :9090 as well.
- **arm64 images** (cross-compiling cgo with `gcc-aarch64-linux-gnu`, or
  native arm64 runners).
- **Scheduled image rebuilds** to pick up base image patches between
  releases, and Dependabot's `docker` ecosystem for the base image tags.
- A backup sidecar or timer; for now the README shows a `sqlite3 .backup`
  run from the host's cron.
- The first push creates the GHCR packages as **private**; the owner makes
  them public once in the package settings.

## Deviations

None yet.
