# Build stage
FROM golang:1.24.6-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# Allow injecting build metadata without requiring a .git directory in context
ARG VERSION=dev
ARG BUILD_TIME
RUN : "${BUILD_TIME:=$(date -u +%Y%m%d.%H%M%S)}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o server ./cmd/server

# Runtime stage
FROM alpine:3.18

RUN apk --no-cache add ca-certificates curl ffmpeg

WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/server .

# Copy SQL initialization files (for reference)
COPY --from=builder /app/init-shared-db.sql .

# Create necessary directories
RUN mkdir -p storage/avatars storage/cache storage/captions storage/logs storage/previews storage/streaming-playlists/hls storage/thumbnails storage/torrents storage/web-videos storage/storyboards processed

# Create non-root user
RUN adduser -D -s /bin/sh athena
RUN chown -R athena:athena /app
USER athena

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

CMD ["./server"]
