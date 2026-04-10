# syntax=docker/dockerfile:1.7

# Build stage
FROM golang:1.25-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Force Go modules mode and set module cache
ENV GO111MODULE=on
ENV GOPROXY=https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.org

# Copy go mod files
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

# Copy only the build inputs needed for the server binary so unrelated repo
# changes do not invalidate the Docker build cache.
COPY migrationfs.go ./
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY nginx ./nginx
COPY pkg ./pkg

# Verify modules and build
ARG VERSION=dev
ARG BUILD_TIME
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    : "${BUILD_TIME:=$(date -u +%Y%m%d.%H%M%S)}" && \
    # Ensure we're in modules mode and verify the module \
    export GO111MODULE=on && \
    go env GO111MODULE && \
    go env GOMOD && \
    go mod verify && \
    # Build with explicit modules mode \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o server ./cmd/server

# Runtime stage
FROM alpine:3.18

# Install runtime dependencies including tools for backup/restore
RUN apk --no-cache add ca-certificates curl ffmpeg postgresql-client redis wget

WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/server .

# Copy entrypoint script
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Create necessary directories
RUN mkdir -p storage/avatars storage/cache storage/captions storage/logs storage/previews storage/streaming-playlists/hls storage/thumbnails storage/torrents storage/web-videos storage/storyboards processed

# Create non-root user
RUN adduser -D -s /bin/sh vidra
RUN chown -R vidra:vidra /app
USER vidra

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
