#!/bin/bash

# Test Environment Setup Script
# This script configures the test environment with proper DNS resolution
# and port isolation to prevent conflicts during testing

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "================================================"
echo "Athena Test Environment Setup"
echo "================================================"

# Function to check if a port is in use
check_port() {
    local port=$1
    local name=$2
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
        echo -e "${RED}✗${NC} Port $port ($name) is in use"
        return 1
    else
        echo -e "${GREEN}✓${NC} Port $port ($name) is available"
        return 0
    fi
}

# Function to free a port
free_port() {
    local port=$1
    echo "  Attempting to free port $port..."

    # Try to stop docker containers using the port
    docker stop $(docker ps --filter "publish=$port" -q) 2>/dev/null || true

    # Check if port is still in use
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
        local pid=$(lsof -Pi :$port -sTCP:LISTEN -t)
        echo -e "  ${YELLOW}Warning:${NC} Process $pid is still using port $port"
        echo "  You may need to manually stop this process"
        return 1
    fi

    echo -e "  ${GREEN}✓${NC} Port $port freed successfully"
    return 0
}

# Step 1: Check DNS Resolution
echo ""
echo "1. Checking DNS Resolution..."
echo "--------------------------------"

# Test DNS resolution for common torrent DHT nodes
test_dns() {
    local host=$1
    if host $host >/dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} DNS resolution for $host: OK"
        return 0
    else
        echo -e "${YELLOW}⚠${NC} DNS resolution for $host: FAILED"
        return 1
    fi
}

DNS_OK=true
test_dns "router.bittorrent.com" || DNS_OK=false
test_dns "dht.transmissionbt.com" || DNS_OK=false
test_dns "google.com" || DNS_OK=false

if [ "$DNS_OK" = false ]; then
    echo -e "${YELLOW}⚠ DNS resolution issues detected${NC}"
    echo "  Consider configuring DNS in your test environment:"
    echo "  - Add DNS servers to /etc/resolv.conf"
    echo "  - Configure Docker with --dns 8.8.8.8"
    echo "  - Use MockedDHTConfig() in torrent tests"
fi

# Step 2: Check and Free Test Ports
echo ""
echo "2. Checking Test Ports..."
echo "--------------------------------"

PORTS_OK=true
check_port 5433 "Postgres Test" || {
    PORTS_OK=false
    free_port 5433
}

check_port 6380 "Redis Test" || {
    PORTS_OK=false
    free_port 6380
}

check_port 15001 "IPFS Test" || {
    PORTS_OK=false
    free_port 15001
}

check_port 18080 "App Test" || {
    PORTS_OK=false
    free_port 18080
}

check_port 3310 "ClamAV Test" || {
    PORTS_OK=false
    free_port 3310
}

# Step 3: Clean up existing test containers
echo ""
echo "3. Cleaning Up Existing Test Containers..."
echo "--------------------------------"

# Stop and remove test containers
docker stop $(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
docker stop $(docker ps -aq --filter "name=athena_test") 2>/dev/null || true
docker rm -f $(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
docker rm -f $(docker ps -aq --filter "name=athena_test") 2>/dev/null || true

# Remove test networks
docker network rm athena-test_test-network 2>/dev/null || true
docker network rm athena_test_default 2>/dev/null || true

echo -e "${GREEN}✓${NC} Test containers cleaned up"

# Step 4: Docker Environment Check
echo ""
echo "4. Docker Environment Check..."
echo "--------------------------------"

if docker info >/dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} Docker is running"

    # Check Docker Compose
    if docker compose version >/dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} Docker Compose v2 is available"
        DOCKER_COMPOSE="docker compose"
    elif docker-compose version >/dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} Docker Compose v1 is available"
        DOCKER_COMPOSE="docker-compose"
    else
        echo -e "${RED}✗${NC} Docker Compose is not installed"
        exit 1
    fi
else
    echo -e "${RED}✗${NC} Docker is not running"
    exit 1
fi

# Step 5: Create test directories
echo ""
echo "5. Creating Test Directories..."
echo "--------------------------------"

mkdir -p /tmp/test-torrents
mkdir -p /tmp/test-storage
mkdir -p /tmp/quarantine
echo -e "${GREEN}✓${NC} Test directories created"

# Step 6: Environment Variables
echo ""
echo "6. Setting Test Environment Variables..."
echo "--------------------------------"

cat > .env.test <<EOF
# Test Environment Configuration
DATABASE_URL=postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable
TEST_DATABASE_URL=postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable
REDIS_URL=redis://localhost:6380/0
JWT_SECRET=test-jwt-secret
IPFS_API=http://localhost:15001
CLAMAV_ADDRESS=localhost:3310
CLAMAV_TIMEOUT=60
CLAMAV_FALLBACK_MODE=strict
TEST_QUARANTINE_DIR=/tmp/quarantine
STORAGE_DIR=/tmp/test-storage
LOG_LEVEL=debug

# Torrent Test Configuration (DHT disabled for tests)
ENABLE_DHT=false
ENABLE_PEX=false
ENABLE_WEBTORRENT=false
TORRENT_LISTEN_PORT=0
TORRENT_DATA_DIR=/tmp/test-torrents
EOF

echo -e "${GREEN}✓${NC} Test environment variables configured in .env.test"

# Step 7: Summary
echo ""
echo "================================================"
echo "Test Environment Setup Summary"
echo "================================================"

if [ "$PORTS_OK" = true ] && [ "$DNS_OK" = true ]; then
    echo -e "${GREEN}✓ All checks passed!${NC}"
    echo ""
    echo "You can now run tests with:"
    echo "  make test-local       - Run all tests with Docker services"
    echo "  make test-unit        - Run unit tests only"
    echo "  make postman-e2e      - Run Postman E2E tests"
    echo "  make test-cleanup     - Clean up test environment"
    exit 0
else
    echo -e "${YELLOW}⚠ Some checks failed${NC}"
    echo ""
    echo "Recommendations:"
    if [ "$DNS_OK" = false ]; then
        echo "- Configure DNS or use mocked DHT in tests"
    fi
    if [ "$PORTS_OK" = false ]; then
        echo "- Some ports may still be in use"
        echo "- Run 'make test-cleanup' to free ports"
    fi
    echo ""
    echo "You can still proceed with testing, but some tests may fail."
    exit 0
fi