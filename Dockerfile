# syntax=docker/dockerfile:1
#
# Container images for nyttigd, nyttig-api, the web app (nyttig-web) and the
# optional Clef assessor (nyttig-clef), one target each:
#
#   docker build --target nyttigd    -t nyttigd .
#   docker build --target nyttig-api -t nyttig-api .
#   docker build --target nyttig-web -t nyttig-web .
#   docker build --target nyttig-clef -t nyttig-clef .
#
# deploy/docker/compose.yaml builds and runs the first three, and
# compose.clef.yaml adds nyttig-clef; deploy/docker/README.md is the guide. CI publishes them to ghcr.io/sonhal/<target> for v* tags.
#
# --build-arg VERSION=v0.5.0 stamps the version (nyttigd -version, the startup
# logs). The build context has no .git, so without it the binaries say "dev".

ARG GO_IMAGE=golang:1.26-trixie
ARG NODE_IMAGE=node:22-trixie-slim

# ── Go binaries ──────────────────────────────────────────────

FROM ${GO_IMAGE} AS go-build

# Headers for the libsqlite3 build tag: nyttigd links Debian's SQLite (built
# with FTS5), the same build as the release tarball's.
RUN apt-get update \
 && apt-get install -y --no-install-recommends libsqlite3-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# The image's Go may be older than go.mod's toolchain line; the go command
# then downloads that toolchain into the module cache (GOTOOLCHAIN=auto).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

# nyttigd is cgo (SQLite); the clients never touch SQLite and are static.
ARG VERSION=
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -e; \
    ldflags="-s -w -X github.com/sonhal/nyttig/internal/version.Version=${VERSION}"; \
    CGO_ENABLED=1 go build -trimpath -tags libsqlite3 -ldflags="$ldflags" -o /out/ ./cmd/nyttigd; \
    CGO_ENABLED=0 go build -trimpath -ldflags="$ldflags" -o /out/ ./cmd/nyttig ./cmd/nyttig-api ./cmd/nyttig-clef; \
    ldd /out/nyttigd | grep -q libsqlite3; \
    /out/nyttigd -version

# ── Web app ──────────────────────────────────────────────────

FROM ${NODE_IMAGE} AS web-build
# pnpm at the version web/package.json pins ("packageManager").
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
RUN corepack enable
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ── nyttigd ──────────────────────────────────────────────────

FROM debian:trixie-slim AS nyttigd
# libsqlite3-0: the library nyttigd links. ca-certificates: feeds over
# HTTPS. sqlite3: online backups ("sqlite3 ... .backup"), see the guide.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 sqlite3 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 10001 nyttig \
 && useradd --uid 10001 --gid nyttig --no-create-home --home-dir /var/lib/nyttig \
      --shell /usr/sbin/nologin nyttig \
 && install -d -o nyttig -g nyttig -m 0700 /var/lib/nyttig /run/nyttig
# nyttig (the TUI and CLI) is the healthcheck, and the way to manage the
# daemon: docker compose exec nyttigd nyttig --socket /run/nyttig/nyttig.sock
COPY --from=go-build /out/nyttigd /out/nyttig /usr/local/bin/
USER nyttig
WORKDIR /var/lib/nyttig
VOLUME /var/lib/nyttig
# The API is served in plaintext on the Unix socket in /run/nyttig, for
# nyttig-api, which shares that directory as a volume and runs as the same
# uid. With a read-only root, /run/nyttig must be a volume or tmpfs.
# --tls-listen adds mutual TLS on a TCP port (9090 by convention) for the
# TUI; deploy/docker/compose.mtls.yaml turns it on.
EXPOSE 9090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD nyttig list-sources --socket /run/nyttig/nyttig.sock > /dev/null || exit 1
ENTRYPOINT ["nyttigd"]
CMD ["--socket", "/run/nyttig/nyttig.sock", "--db-path", "/var/lib/nyttig/nyttig.db"]

# ── nyttig-api ───────────────────────────────────────────────

FROM gcr.io/distroless/static-debian13:nonroot AS nyttig-api
COPY --from=go-build /out/nyttig-api /usr/local/bin/nyttig-api
# nyttigd's uid: only it may connect to nyttigd's socket (nyttigd's
# /run/nyttig is mode 0700). This image never sees the database volume.
USER 10001:10001
EXPOSE 7070
# A loopback listener is unreachable from outside the container, so it
# listens on every interface; keep the published port on the host's
# loopback, behind the reverse proxy (deploy/docker/compose.yaml does).
# --origin (required) comes from the command.
ENTRYPOINT ["/usr/local/bin/nyttig-api", "--listen", ":7070", "--allow-public-listen"]
CMD ["--socket", "/run/nyttig/nyttig.sock"]

# ── nyttig-clef ──────────────────────────────────────────────

# distroless/static carries the CA certificates that HTTPS to
# api.cloudflare.com needs, and no shell, so there is no healthcheck.
FROM gcr.io/distroless/static-debian13:nonroot AS nyttig-clef
COPY --from=go-build /out/nyttig-clef /usr/local/bin/nyttig-clef
# nyttigd's uid, the only one that may connect to its socket. The token is
# mounted as a file (a compose secret) and named by token_file in the config.
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nyttig-clef"]
CMD ["--config", "/etc/nyttig/clef.toml"]

# ── nyttig-web ───────────────────────────────────────────────

FROM gcr.io/distroless/nodejs22-debian13:nonroot AS nyttig-web
WORKDIR /app
# The build bundles everything; package.json marks it as ES modules.
COPY --from=web-build /web/build ./build
COPY --from=web-build /web/package.json ./
# ORIGIN (required) is the URL the browser uses, the same as nyttig-api's
# --origin.
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/nodejs/bin/node", "-e", "fetch('http://127.0.0.1:3000/manifest.webmanifest').then(r => process.exit(r.ok ? 0 : 1), () => process.exit(1))"]
CMD ["build"]
