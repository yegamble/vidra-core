#!/bin/bash

# Script to convert Atlas migrations to Goose format
# This adds Goose directives to existing SQL files

set -e

MIGRATION_DIR="migrations"
BACKUP_DIR="migrations_atlas_backup"

echo "Converting Atlas migrations to Goose format..."

# Create backup directory
if [ ! -d "$BACKUP_DIR" ]; then
    mkdir -p "$BACKUP_DIR"
    echo "Created backup directory: $BACKUP_DIR"
fi

# Backup existing migrations
cp -r "$MIGRATION_DIR"/*.sql "$BACKUP_DIR"/ 2>/dev/null || true
echo "Backed up existing migrations to $BACKUP_DIR"

# Function to add Goose directives to a migration file
convert_migration() {
    local file="$1"
    local filename=$(basename "$file")

    echo "Converting $filename..."

    # Read the original content
    content=$(cat "$file")

    # Create new content with Goose directives
    {
        echo "-- +goose Up"
        echo "-- +goose StatementBegin"
        echo "$content"
        echo "-- +goose StatementEnd"
        echo ""
        echo "-- +goose Down"
        echo "-- NOTE: Add rollback statements here if needed"
        echo "-- For now, we'll keep migrations forward-only for safety"
    } > "$file.tmp"

    # Replace original file
    mv "$file.tmp" "$file"
}

# Convert each SQL file
for file in "$MIGRATION_DIR"/*.sql; do
    if [ -f "$file" ]; then
        # Check if already has Goose directives
        if grep -q "+goose" "$file"; then
            echo "Skipping $file (already has Goose directives)"
        else
            convert_migration "$file"
        fi
    fi
done

echo ""
echo "✅ Migration conversion complete!"
echo ""
echo "Next steps:"
echo "1. Review the converted migrations in $MIGRATION_DIR"
echo "2. Original migrations are backed up in $BACKUP_DIR"
echo "3. Test with: goose -dir migrations postgres \"\$DATABASE_URL\" status"
echo "4. Apply migrations with: goose -dir migrations postgres \"\$DATABASE_URL\" up"
