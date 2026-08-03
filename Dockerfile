# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26-alpine AS build
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
FROM alpine:3.24
# ffmpeg provides ffprobe, used to extract media metadata on upload.
RUN apk add --no-cache ca-certificates wget ffmpeg && adduser -D -u 10001 vidra

# Optional yt-dlp platform-URL import (W2.C1, UPLOAD-09), OFF by default. Build
# with --build-arg YTDLP_VERSION=<pinned release> to bake in a PINNED yt-dlp
# (e.g. 2025.06.30); an empty value (the default) skips it so the base image
# stays lean. The version is PINNED at build time — the runtime never
# self-updates (the app also forbids --update). The app opt-in is separate
# (YTDLP_IMPORT_ENABLED=true). yt-dlp is a python zipapp, so it needs python3.
ARG YTDLP_VERSION=""
RUN if [ -n "$YTDLP_VERSION" ]; then \
        apk add --no-cache python3 && \
        wget -O /usr/local/bin/yt-dlp \
            "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" && \
        chmod 0755 /usr/local/bin/yt-dlp && \
        /usr/local/bin/yt-dlp --version ; \
    fi

USER vidra
WORKDIR /app
COPY --from=build /out/api /app/api

EXPOSE 8080
# Liveness check used by Compose/orchestrators.
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/api"]
