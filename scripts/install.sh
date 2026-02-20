#!/bin/sh
set -e

# Athena One-Command Install Script
# Usage: curl -sSL https://raw.githubusercontent.com/yegamble/athena/main/scripts/install.sh | bash
# Or safer: curl -O https://... && less install.sh && bash install.sh
# From an existing clone: INSTALL_DIR=. bash scripts/install.sh
# Options:
#   --no-cache    Force rebuild of Docker images without cache
#   NO_CACHE=true Environment variable alternative to --no-cache

VERSION="${VERSION:-latest}"
NO_CACHE="${NO_CACHE:-false}"
MODE="${1:-docker}"

# Parse flags
for arg in "$@"; do
    case "$arg" in
        --no-cache) NO_CACHE="true" ;;
        docker|native) MODE="$arg" ;;
    esac
done

# If run from inside the repo (e.g., scripts/install.sh), use the repo root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
if [ -z "${INSTALL_DIR:-}" ]; then
    if [ -f "$SCRIPT_DIR/../docker-compose.yml" ]; then
        INSTALL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
    else
        INSTALL_DIR="$HOME/athena"
    fi
fi

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    printf "${GREEN}[INFO]${NC} %s\n" "$1"
}

log_warn() {
    printf "${YELLOW}[WARN]${NC} %s\n" "$1"
}

log_error() {
    printf "${RED}[ERROR]${NC} %s\n" "$1"
}

# Detect OS
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        OS_VERSION=$VERSION_ID
    elif [ "$(uname)" = "Darwin" ]; then
        OS="macos"
        OS_VERSION=$(sw_vers -productVersion)
    else
        log_error "Unsupported operating system"
        exit 1
    fi

    log_info "Detected OS: $OS $OS_VERSION"
}

# Check if Docker is installed
check_docker() {
    if command -v docker >/dev/null 2>&1; then
        log_info "Docker is already installed: $(docker --version)"
        return 0
    else
        return 1
    fi
}

# Install Docker
install_docker() {
    log_info "Installing Docker..."

    case "$OS" in
        ubuntu|debian)
            curl -fsSL https://get.docker.com -o get-docker.sh
            sh get-docker.sh
            rm get-docker.sh
            ;;
        centos|rhel|fedora)
            curl -fsSL https://get.docker.com -o get-docker.sh
            sh get-docker.sh
            rm get-docker.sh
            ;;
        macos)
            log_warn "Please install Docker Desktop from https://www.docker.com/products/docker-desktop"
            log_warn "Then re-run this script"
            exit 1
            ;;
        *)
            log_error "Automatic Docker installation not supported on $OS"
            log_error "Please install Docker manually and re-run this script"
            exit 1
            ;;
    esac

    log_info "Docker installed successfully"
}

# Generate JWT secret
generate_jwt_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 32
    else
        # Fallback to /dev/urandom if openssl not available
        head -c 32 /dev/urandom | base64
    fi
}

# Setup Athena
setup_athena() {
    log_info "Setting up Athena in $INSTALL_DIR..."

    # Create install directory
    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"

    # Clone or update Athena
    if [ -f "docker-compose.yml" ]; then
        log_info "Athena files already present, skipping download"
    elif [ -d ".git" ]; then
        log_info "Existing git repository detected, pulling latest..."
        git pull --ff-only || {
            log_warn "git pull failed — continuing with existing files"
        }
    elif [ "$(ls -A .)" ]; then
        log_error "Directory $INSTALL_DIR is not empty and is not an Athena checkout"
        log_error "Please use an empty directory or set INSTALL_DIR to a different path:"
        log_error "  INSTALL_DIR=/path/to/athena bash install.sh"
        exit 1
    else
        log_info "Downloading Athena..."
        if command -v git >/dev/null 2>&1; then
            git clone https://github.com/yegamble/athena.git .
        else
            log_error "Git not found. Please install git or download Athena manually"
            exit 1
        fi
    fi

    # Create minimal .env for setup mode (wizard handles full configuration)
    if [ ! -f .env ]; then
        log_info "Creating .env for setup mode..."
        cat > .env <<EOF
# Athena Setup Mode
# The setup wizard at http://localhost:8080/setup/welcome will configure these.
SETUP_COMPLETED=false
PORT=8080
REQUIRE_IPFS=false
EOF
        log_info "Setup wizard will run on first access"
    else
        log_info ".env file already exists, skipping creation"
    fi
}

# Start services
start_services() {
    log_info "Starting Athena services with Docker Compose..."

    COMPOSE_FLAGS="-d"
    if [ "$NO_CACHE" = "true" ]; then
        log_info "Building with --no-cache (this may take longer)..."
        COMPOSE_FLAGS="-d --build"
        docker compose build --no-cache || {
            log_error "Docker build failed"
            exit 1
        }
    fi

    if ! docker compose up $COMPOSE_FLAGS; then
        log_error "Failed to start services"
        exit 1
    fi

    log_info "Services started successfully"
}

# Wait for health check
wait_for_health() {
    log_info "Waiting for Athena to be ready..."

    max_attempts=30
    attempt=0

    while [ $attempt -lt $max_attempts ]; do
        if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
            log_info "Athena is ready!"
            return 0
        fi

        attempt=$((attempt + 1))
        printf "."
        sleep 2
    done

    echo ""
    log_warn "Health check timed out, but services may still be starting"
    log_warn "Check logs with: docker compose logs -f"
}

# Print success message
print_success() {
    echo ""
    log_info "============================================"
    log_info "Athena installation complete!"
    log_info "============================================"
    echo ""
    log_info "Access Athena at: http://localhost:8080"
    log_info "Setup wizard: http://localhost:8080/setup"
    echo ""
    log_info "Useful commands:"
    log_info "  View logs: docker compose logs -f"
    log_info "  Stop services: docker compose stop"
    log_info "  Restart services: docker compose restart"
    echo ""
}

# Main installation flow
main() {
    log_info "Starting Athena installation (mode: $MODE)..."

    detect_os

    if [ "$MODE" = "docker" ]; then
        # Docker mode (default)
        if ! check_docker; then
            install_docker
        fi

        setup_athena
        start_services
        wait_for_health
        print_success
    else
        log_error "Native mode not yet implemented"
        log_error "Please use Docker mode (default) for now"
        exit 1
    fi
}

# Run main
main
