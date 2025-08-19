#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$PROJECT_ROOT/.env"
BACKUP_DIR="$PROJECT_ROOT/backups"
LOG_FILE="$PROJECT_ROOT/deploy.log"

# Default values
ENVIRONMENT="production"
SKIP_BACKUP=false
SKIP_TESTS=false
ROLLBACK_ON_FAILURE=true
DOCKER_COMPOSE="docker compose"

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to log messages
log_message() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" | tee -a "$LOG_FILE"
}

# Function to show usage
show_usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Deploy Athena application to production environment.

OPTIONS:
    -e, --environment ENV    Deployment environment (default: production)
    -s, --skip-backup        Skip database backup before deployment
    -t, --skip-tests         Skip running tests before deployment
    -n, --no-rollback        Disable automatic rollback on failure
    -h, --help               Show this help message

EXAMPLES:
    $0                        # Deploy to production with all safety checks
    $0 -e staging            # Deploy to staging environment
    $0 -s -t                 # Deploy without backup and tests
    $0 -n                    # Deploy without automatic rollback

EOF
}

# Function to parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -e|--environment)
                ENVIRONMENT="$2"
                shift 2
                ;;
            -s|--skip-backup)
                SKIP_BACKUP=true
                shift
                ;;
            -t|--skip-tests)
                SKIP_TESTS=true
                shift
                ;;
            -n|--no-rollback)
                ROLLBACK_ON_FAILURE=false
                shift
                ;;
            -h|--help)
                show_usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
}

# Function to check prerequisites
check_prerequisites() {
    print_status "Checking prerequisites..."
    
    # Check if Docker is installed and running
    if ! command -v docker &> /dev/null; then
        print_error "Docker is not installed"
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        print_error "Docker is not running"
        exit 1
    fi
    
    # Check if Docker Compose is available
    if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
        print_error "Docker Compose is not available"
        exit 1
    fi
    
    # Check if .env file exists
    if [[ ! -f "$ENV_FILE" ]]; then
        print_error ".env file not found. Please copy .env.example to .env and configure it."
        exit 1
    fi
    
    # Check if required environment variables are set
    source "$ENV_FILE"
    required_vars=("DATABASE_URL" "REDIS_URL" "JWT_SECRET")
    for var in "${required_vars[@]}"; do
        if [[ -z "${!var:-}" ]]; then
            print_error "Required environment variable $var is not set"
            exit 1
        fi
    done
    
    print_success "Prerequisites check passed"
}

# Function to create backup
create_backup() {
    if [[ "$SKIP_BACKUP" == "true" ]]; then
        print_warning "Skipping database backup"
        return 0
    fi
    
    print_status "Creating database backup..."
    
    # Create backup directory
    mkdir -p "$BACKUP_DIR"
    
    # Generate backup filename with timestamp
    BACKUP_FILE="$BACKUP_DIR/backup_$(date +%Y%m%d_%H%M%S).sql"
    
    # Extract database connection details from DATABASE_URL
    if [[ "$DATABASE_URL" =~ postgres://([^:]+):([^@]+)@([^:]+):([^/]+)/([^?]+) ]]; then
        DB_USER="${BASH_REMATCH[1]}"
        DB_PASS="${BASH_REMATCH[2]}"
        DB_HOST="${BASH_REMATCH[3]}"
        DB_PORT="${BASH_REMATCH[4]}"
        DB_NAME="${BASH_REMATCH[5]}"
        
        # Create backup using pg_dump
        PGPASSWORD="$DB_PASS" pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" > "$BACKUP_FILE"
        
        if [[ $? -eq 0 ]]; then
            print_success "Database backup created: $BACKUP_FILE"
        else
            print_error "Failed to create database backup"
            exit 1
        fi
    else
        print_error "Invalid DATABASE_URL format"
        exit 1
    fi
}

# Function to run tests
run_tests() {
    if [[ "$SKIP_TESTS" == "true" ]]; then
        print_warning "Skipping tests"
        return 0
    fi
    
    print_status "Running tests..."
    
    cd "$PROJECT_ROOT"
    
    # Run unit tests
    if ! make test; then
        print_error "Unit tests failed"
        exit 1
    fi
    
    # Run integration tests if available
    if make test-integration &> /dev/null; then
        if ! make test-integration; then
            print_error "Integration tests failed"
            exit 1
        fi
    fi
    
    print_success "All tests passed"
}

# Function to build and deploy
deploy() {
    print_status "Starting deployment to $ENVIRONMENT environment..."
    
    cd "$PROJECT_ROOT"
    
    # Stop existing services
    print_status "Stopping existing services..."
    $DOCKER_COMPOSE -f docker-compose.prod.yml down --remove-orphans
    
    # Build new images
    print_status "Building Docker images..."
    $DOCKER_COMPOSE -f docker-compose.prod.yml build --no-cache
    
    # Start services
    print_status "Starting services..."
    $DOCKER_COMPOSE -f docker-compose.prod.yml up -d
    
    # Wait for services to be healthy
    print_status "Waiting for services to be healthy..."
    timeout=300
    elapsed=0
    
    while [[ $elapsed -lt $timeout ]]; do
        if $DOCKER_COMPOSE -f docker-compose.prod.yml ps | grep -q "healthy"; then
            print_success "Services are healthy"
            break
        fi
        
        sleep 10
        elapsed=$((elapsed + 10))
        print_status "Waiting for services... ($elapsed/$timeout seconds)"
    done
    
    if [[ $elapsed -ge $timeout ]]; then
        print_error "Services failed to become healthy within $timeout seconds"
        if [[ "$ROLLBACK_ON_FAILURE" == "true" ]]; then
            rollback
        fi
        exit 1
    fi
    
    # Run database migrations
    print_status "Running database migrations..."
    if ! make migrate-up; then
        print_error "Database migrations failed"
        if [[ "$ROLLBACK_ON_FAILURE" == "true" ]]; then
            rollback
        fi
        exit 1
    fi
    
    # Health check
    print_status "Performing health check..."
    sleep 30
    
    if ! curl -f http://localhost:8080/health &> /dev/null; then
        print_error "Health check failed"
        if [[ "$ROLLBACK_ON_FAILURE" == "true" ]]; then
            rollback
        fi
        exit 1
    fi
    
    print_success "Deployment completed successfully"
}

# Function to rollback
rollback() {
    print_warning "Rolling back deployment..."
    
    cd "$PROJECT_ROOT"
    
    # Stop services
    $DOCKER_COMPOSE -f docker-compose.prod.yml down
    
    # Restore from backup if available
    if [[ -f "$BACKUP_FILE" ]]; then
        print_status "Restoring database from backup..."
        # Implementation depends on your backup strategy
        print_warning "Database rollback requires manual intervention"
    fi
    
    print_warning "Rollback completed. Please check the system manually."
}

# Function to cleanup
cleanup() {
    print_status "Cleaning up..."
    
    # Remove old backups (keep last 5)
    if [[ -d "$BACKUP_DIR" ]]; then
        cd "$BACKUP_DIR"
        ls -t | tail -n +6 | xargs -r rm -f
    fi
    
    # Clean up Docker images
    docker image prune -f
    
    print_success "Cleanup completed"
}

# Main execution
main() {
    log_message "Starting deployment script"
    
    # Parse command line arguments
    parse_args "$@"
    
    # Check prerequisites
    check_prerequisites
    
    # Create backup
    create_backup
    
    # Run tests
    run_tests
    
    # Deploy
    deploy
    
    # Cleanup
    cleanup
    
    log_message "Deployment script completed successfully"
    print_success "Deployment to $ENVIRONMENT completed successfully!"
}

# Trap to handle script interruption
trap 'print_error "Deployment interrupted"; exit 1' INT TERM

# Run main function with all arguments
main "$@"