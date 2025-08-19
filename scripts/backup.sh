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
LOG_FILE="$PROJECT_ROOT/backup.log"

# Default values
BACKUP_TYPE="full"
RETENTION_DAYS=30
COMPRESS=true
UPLOAD_TO_S3=false

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

Create backup of Athena application data.

OPTIONS:
    -t, --type TYPE          Backup type: full, db, files (default: full)
    -r, --retention DAYS     Retention period in days (default: 30)
    -c, --compress           Compress backup files (default: true)
    -s, --s3                 Upload backup to S3
    -h, --help               Show this help message

EXAMPLES:
    $0                        # Create full backup with default settings
    $0 -t db                 # Create database backup only
    $0 -t files              # Create files backup only
    $0 -r 7                  # Keep backups for 7 days
    $0 -s                    # Upload backup to S3

EOF
}

# Function to parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -t|--type)
                BACKUP_TYPE="$2"
                shift 2
                ;;
            -r|--retention)
                RETENTION_DAYS="$2"
                shift 2
                ;;
            -c|--compress)
                COMPRESS=true
                shift
                ;;
            -s|--s3)
                UPLOAD_TO_S3=true
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
    
    # Check if .env file exists
    if [[ ! -f "$ENV_FILE" ]]; then
        print_error ".env file not found"
        exit 1
    fi
    
    # Load environment variables
    source "$ENV_FILE"
    
    # Check if required tools are available
    if ! command -v pg_dump &> /dev/null; then
        print_error "pg_dump is not installed"
        exit 1
    fi
    
    if [[ "$UPLOAD_TO_S3" == "true" ]]; then
        if ! command -v aws &> /dev/null; then
            print_error "AWS CLI is not installed"
            exit 1
        fi
        
        if [[ -z "${S3_BUCKET:-}" ]]; then
            print_error "S3_BUCKET environment variable is not set"
            exit 1
        fi
    fi
    
    print_success "Prerequisites check passed"
}

# Function to create backup directory
create_backup_dir() {
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local backup_path="$BACKUP_DIR/backup_${timestamp}"
    
    mkdir -p "$backup_path"
    echo "$backup_path"
}

# Function to backup database
backup_database() {
    local backup_path="$1"
    print_status "Creating database backup..."
    
    # Extract database connection details from DATABASE_URL
    if [[ "$DATABASE_URL" =~ postgres://([^:]+):([^@]+)@([^:]+):([^/]+)/([^?]+) ]]; then
        local db_user="${BASH_REMATCH[1]}"
        local db_pass="${BASH_REMATCH[2]}"
        local db_host="${BASH_REMATCH[3]}"
        local db_port="${BASH_REMATCH[4]}"
        local db_name="${BASH_REMATCH[5]}"
        
        local db_backup_file="$backup_path/database.sql"
        
        # Create database backup
        PGPASSWORD="$db_pass" pg_dump \
            -h "$db_host" \
            -p "$db_port" \
            -U "$db_user" \
            -d "$db_name" \
            --verbose \
            --no-password \
            > "$db_backup_file"
        
        if [[ $? -eq 0 ]]; then
            print_success "Database backup created: $db_backup_file"
            echo "$db_backup_file"
        else
            print_error "Failed to create database backup"
            return 1
        fi
    else
        print_error "Invalid DATABASE_URL format"
        return 1
    fi
}

# Function to backup files
backup_files() {
    local backup_path="$1"
    print_status "Creating files backup..."
    
    # Create files backup directory
    local files_backup_dir="$backup_path/files"
    mkdir -p "$files_backup_dir"
    
    # Backup uploads directory
    if [[ -d "$PROJECT_ROOT/uploads" ]]; then
        print_status "Backing up uploads directory..."
        cp -r "$PROJECT_ROOT/uploads" "$files_backup_dir/"
    fi
    
    # Backup processed directory
    if [[ -d "$PROJECT_ROOT/processed" ]]; then
        print_status "Backing up processed directory..."
        cp -r "$PROJECT_ROOT/processed" "$files_backup_dir/"
    fi
    
    # Backup logs directory
    if [[ -d "$PROJECT_ROOT/logs" ]]; then
        print_status "Backing up logs directory..."
        cp -r "$PROJECT_ROOT/logs" "$files_backup_dir/"
    fi
    
    # Backup configuration files
    print_status "Backing up configuration files..."
    cp "$ENV_FILE" "$files_backup_dir/"
    cp "$PROJECT_ROOT/docker-compose.yml" "$files_backup_dir/"
    cp "$PROJECT_ROOT/docker-compose.prod.yml" "$files_backup_dir/"
    
    print_success "Files backup created: $files_backup_dir"
    echo "$files_backup_dir"
}

# Function to compress backup
compress_backup() {
    local backup_path="$1"
    
    if [[ "$COMPRESS" == "true" ]]; then
        print_status "Compressing backup..."
        
        local backup_name=$(basename "$backup_path")
        local archive_path="$BACKUP_DIR/${backup_name}.tar.gz"
        
        cd "$BACKUP_DIR"
        tar -czf "$archive_path" "$backup_name"
        
        if [[ $? -eq 0 ]]; then
            print_success "Backup compressed: $archive_path"
            rm -rf "$backup_path"
            echo "$archive_path"
        else
            print_error "Failed to compress backup"
            return 1
        fi
    else
        echo "$backup_path"
    fi
}

# Function to upload to S3
upload_to_s3() {
    local backup_file="$1"
    
    if [[ "$UPLOAD_TO_S3" == "true" ]]; then
        print_status "Uploading backup to S3..."
        
        local s3_key="backups/$(basename "$backup_file")"
        
        aws s3 cp "$backup_file" "s3://$S3_BUCKET/$s3_key" \
            --storage-class STANDARD_IA \
            --metadata "backup-type=$BACKUP_TYPE,created=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        
        if [[ $? -eq 0 ]]; then
            print_success "Backup uploaded to S3: s3://$S3_BUCKET/$s3_key"
        else
            print_error "Failed to upload backup to S3"
            return 1
        fi
    fi
}

# Function to cleanup old backups
cleanup_old_backups() {
    print_status "Cleaning up old backups..."
    
    local cutoff_date=$(date -d "$RETENTION_DAYS days ago" +%Y%m%d)
    local deleted_count=0
    
    # Clean up local backups
    for backup in "$BACKUP_DIR"/backup_*; do
        if [[ -f "$backup" || -d "$backup" ]]; then
            local backup_date=$(basename "$backup" | sed 's/backup_\([0-9]\{8\}\)_.*/\1/')
            if [[ "$backup_date" < "$cutoff_date" ]]; then
                rm -rf "$backup"
                deleted_count=$((deleted_count + 1))
            fi
        fi
    done
    
    # Clean up S3 backups if enabled
    if [[ "$UPLOAD_TO_S3" == "true" ]]; then
        print_status "Cleaning up old S3 backups..."
        
        aws s3 ls "s3://$S3_BUCKET/backups/" | while read -r line; do
            local s3_date=$(echo "$line" | awk '{print $1}' | sed 's/backup_\([0-9]\{8\}\)_.*/\1/')
            local s3_key=$(echo "$line" | awk '{print $4}')
            
            if [[ "$s3_date" < "$cutoff_date" ]]; then
                aws s3 rm "s3://$S3_BUCKET/backups/$s3_key"
                print_status "Deleted old S3 backup: $s3_key"
            fi
        done
    fi
    
    print_success "Cleaned up $deleted_count old backups"
}

# Function to create backup manifest
create_manifest() {
    local backup_path="$1"
    local manifest_file="$backup_path/manifest.json"
    
    cat > "$manifest_file" << EOF
{
  "backup_type": "$BACKUP_TYPE",
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "version": "$(git rev-parse HEAD 2>/dev/null || echo 'unknown')",
  "environment": "$(hostname)",
  "files": [
EOF
    
    find "$backup_path" -type f -name "*.sql" -o -name "*.tar.gz" | while read -r file; do
        local filename=$(basename "$file")
        local size=$(stat -c%s "$file")
        local checksum=$(sha256sum "$file" | cut -d' ' -f1)
        
        echo "    {"
        echo "      \"filename\": \"$filename\","
        echo "      \"size\": $size,"
        echo "      \"checksum\": \"$checksum\""
        echo "    },"
    done | sed '$ s/,$//' >> "$manifest_file"
    
    echo "  ]" >> "$manifest_file"
    echo "}" >> "$manifest_file"
    
    print_success "Backup manifest created: $manifest_file"
}

# Main execution
main() {
    log_message "Starting backup process"
    
    # Parse command line arguments
    parse_args "$@"
    
    # Check prerequisites
    check_prerequisites
    
    # Create backup directory
    local backup_path=$(create_backup_dir)
    
    # Perform backup based on type
    case "$BACKUP_TYPE" in
        "full")
            backup_database "$backup_path"
            backup_files "$backup_path"
            ;;
        "db")
            backup_database "$backup_path"
            ;;
        "files")
            backup_files "$backup_path"
            ;;
        *)
            print_error "Invalid backup type: $BACKUP_TYPE"
            exit 1
            ;;
    esac
    
    # Create manifest
    create_manifest "$backup_path"
    
    # Compress backup
    local final_backup=$(compress_backup "$backup_path")
    
    # Upload to S3 if enabled
    upload_to_s3 "$final_backup"
    
    # Cleanup old backups
    cleanup_old_backups
    
    log_message "Backup process completed successfully"
    print_success "Backup completed: $final_backup"
}

# Trap to handle script interruption
trap 'print_error "Backup interrupted"; exit 1' INT TERM

# Run main function with all arguments
main "$@"