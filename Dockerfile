# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.27-alpine AS build
WORKDIR /src

# Cache module downloads separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build metadata baked into internal/version so a running container can answer
# "which build is this?" (GET /api/v1/admin/system, GET /healthz, NodeInfo).
# The package path and the three variable names MUST stay identical to
# Makefile:13-14 — -ldflags -X matches on the fully-qualified symbol, so a typo
# silently no-ops instead of failing the build. Note BUILD_DATE maps onto
# version.Date (the arg is named for what it is; the symbol is what the linker
# needs). Defaults are EMPTY on purpose: an empty arg is skipped below, so a
# plain `docker build` / `make up` / `make dev` keeps the Go-side defaults in
# internal/version/version.go exactly as before. CI (publish-container.yml)
# supplies the release tag, short SHA and release timestamp.
ARG VERSION=""
ARG COMMIT=""
ARG BUILD_DATE=""

# Build a static binary so it runs on a minimal final image.
RUN set -eu; \
    pkg="github.com/vidra/vidra-core/internal/version"; \
    ldflags="-s -w"; \
    if [ -n "$VERSION" ]; then ldflags="$ldflags -X $pkg.Version=$VERSION"; fi; \
    if [ -n "$COMMIT" ]; then ldflags="$ldflags -X $pkg.Commit=$COMMIT"; fi; \
    if [ -n "$BUILD_DATE" ]; then ldflags="$ldflags -X $pkg.Date=$BUILD_DATE"; fi; \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="$ldflags" -o /out/api ./cmd/api

# ---- runtime stage ----
FROM alpine:3.24 AS runtime
# `apk upgrade` FIRST: the base image lags the package repository. Official
# alpine:3.24 is rebuilt for Alpine point releases, not for each package fix,
# and `apk add` never upgrades a package the base already carries — so a plain
# rebuild re-ships the base's copy. v0.6.4 shipped libssl3/libcrypto3 3.5.7-r0
# (ten OpenSSL CVEs, CVSS up to 9.8) while 3.5.8-r0 was already in v3.24 main.
# Here that library is live: ffmpeg, wget and python3/yt-dlp use it for
# outbound TLS to import URLs.
# Trade-off, stated honestly: the image now takes whatever v3.24 main serves
# at build time, so two builds of one commit can differ in patch-level
# packages. A cached layer for this RUN would silently re-ship an OLDER package
# set — neither the base digest nor this line changes when a fix lands — so
# publish-container.yml rebuilds this stage with no-cache-filters. Nothing scans
# the pushed digest yet; release qualification has to.
# ffmpeg provides ffprobe, used to extract media metadata on upload.
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates wget ffmpeg && adduser -D -u 10001 vidra

# Optional yt-dlp platform-URL import (W2.C1, UPLOAD-09), OFF by default. Build
# with --build-arg YTDLP_VERSION=<pinned release> to bake in a PINNED yt-dlp
# (e.g. 2025.06.30); an empty value (the default) skips it so the base image
# stays lean. The version is PINNED at build time — the runtime never
# self-updates (the app also forbids --update). The app opt-in is separate
# (YTDLP_IMPORT_ENABLED=true). yt-dlp is a python zipapp, so it needs python3.
#
# YTDLP_SHA256 is REQUIRED whenever YTDLP_VERSION is set: the build fails if it
# is missing or does not match. A version pin names a release, not bytes — a
# tampered or re-uploaded GitHub asset (or anything in the download path)
# would otherwise be baked into a signed, attested release image and executed
# by the importer with no check at all. Take the value from the `yt-dlp` line
# of that release's SHA2-256SUMS asset (signed by SHA2-256SUMS.sig), and bump
# both args together.
ARG YTDLP_VERSION=""
ARG YTDLP_SHA256=""
RUN if [ -n "$YTDLP_VERSION" ]; then \
        if [ -z "$YTDLP_SHA256" ]; then \
            echo "YTDLP_VERSION=${YTDLP_VERSION} needs --build-arg YTDLP_SHA256 (see SHA2-256SUMS of that release)" >&2; \
            exit 1; \
        fi && \
        apk add --no-cache python3 && \
        wget -O /usr/local/bin/yt-dlp \
            "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" && \
        echo "${YTDLP_SHA256}  /usr/local/bin/yt-dlp" | sha256sum -c - && \
        chmod 0755 /usr/local/bin/yt-dlp && \
        /usr/local/bin/yt-dlp --version ; \
    elif [ -n "$YTDLP_SHA256" ]; then \
        echo "YTDLP_SHA256 is set but YTDLP_VERSION is not: refusing to build an image without the yt-dlp that was pinned" >&2; \
        exit 1; \
    fi

USER vidra
WORKDIR /app
COPY --from=build /out/api /app/api

EXPOSE 8080
# Liveness check used by Compose/orchestrators.
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/api"]
