#!/usr/bin/env bash
# test-integration.sh — Run external service integration tests.
#
# Starts all mock Docker services (test-integration profile), runs
# the integration test suite with TEST_INTEGRATION=true, then tears
# down the services regardless of test outcome.
#
# Usage:
#   ./scripts/test-integration.sh                  # Run all integration tests
#   ./scripts/test-integration.sh -run TestS3       # Run a specific test
#   TEST_KEEP_SERVICES=true ./scripts/test-integration.sh  # Keep services running after tests
#
# Requirements:
#   - Docker and Docker Compose v2
#   - Go 1.24+
#   - pg_isready (postgresql-client) for Postgres readiness check
#   - redis-cli for Redis readiness check

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DOCKER_COMPOSE="${DOCKER_COMPOSE:-docker compose}"
KEEP_SERVICES="${TEST_KEEP_SERVICES:-false}"
EXTRA_ARGS="${@:-}"

# Ports for all mock services
MINIO_PORT=19100
ATPROTO_PORT=19200
ACTIVITYPUB_PORT=19300
MAILPIT_SMTP_PORT=19401
IOTA_PORT=19500
POSTGRES_PORT=15432
REDIS_PORT=16379
IPFS_PORT=15001

cd "$PROJECT_ROOT"

# ── Cleanup ───────────────────────────────────────────────────────────────────

cleanup() {
    if [ "$KEEP_SERVICES" = "true" ]; then
        echo ""
        echo "TEST_KEEP_SERVICES=true — leaving mock services running."
        echo "Stop them with: make test-mock-services-down"
        return
    fi
    echo ""
    echo "Stopping mock external services..."
    $DOCKER_COMPOSE --profile test-integration down -v 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT

# ── Start services ────────────────────────────────────────────────────────────

echo "Starting mock external services (test-integration profile)..."
$DOCKER_COMPOSE --profile test-integration up -d --build

# ── Wait helpers ──────────────────────────────────────────────────────────────

wait_http() {
    local name="$1" url="$2" retries="${3:-30}"
    echo -n "  Waiting for $name ($url)..."
    for i in $(seq 1 $retries); do
        if curl -sf "$url" >/dev/null 2>&1; then
            echo " ready"
            return 0
        fi
        sleep 2
    done
    echo " TIMEOUT (continuing anyway)"
    return 0  # Non-fatal: test will fail with a clear error message
}

wait_postgres() {
    local host="${1:-127.0.0.1}" port="${2:-15432}" db="${3:-athena_integration}" user="${4:-integration_user}" retries="${5:-30}"
    echo -n "  Waiting for Postgres on $host:$port..."
    for i in $(seq 1 $retries); do
        if pg_isready -h "$host" -p "$port" -d "$db" -U "$user" >/dev/null 2>&1; then
            echo " ready"
            return 0
        fi
        sleep 2
    done
    echo " TIMEOUT (continuing anyway)"
    return 0
}

wait_redis() {
    local url="${1:-redis://127.0.0.1:16379}" retries="${2:-30}"
    echo -n "  Waiting for Redis ($url)..."
    for i in $(seq 1 $retries); do
        if redis-cli -u "$url" ping >/dev/null 2>&1; then
            echo " ready"
            return 0
        fi
        sleep 2
    done
    echo " TIMEOUT (continuing anyway)"
    return 0
}

# ── Readiness checks ──────────────────────────────────────────────────────────

echo ""
echo "Waiting for services to be ready..."
wait_http "MinIO"        "http://localhost:${MINIO_PORT}/minio/health/live"
wait_http "ATProto PDS"  "http://localhost:${ATPROTO_PORT}/health"
wait_http "ActivityPub"  "http://localhost:${ACTIVITYPUB_PORT}/health"
wait_http "IOTA RPC"     "http://localhost:${IOTA_PORT}/health"
wait_http "Mailpit"      "http://localhost:19400/api/v1/messages"
wait_http "IPFS"         "http://localhost:${IPFS_PORT}/api/v0/id"
wait_postgres "127.0.0.1" "${POSTGRES_PORT}" "athena_integration" "integration_user"
wait_redis "redis://127.0.0.1:${REDIS_PORT}"

# ── Run tests ─────────────────────────────────────────────────────────────────

echo ""
echo "Running integration tests (TEST_INTEGRATION=true)..."
echo ""

TEST_INTEGRATION=true \
    go test -v -count=1 -timeout 120s \
    ./tests/integration/... \
    $EXTRA_ARGS

echo ""
echo "Integration tests complete."
