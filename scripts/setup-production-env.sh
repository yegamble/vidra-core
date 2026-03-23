#!/bin/bash
set -e

# setup-production-env.sh
# Applies rotated credentials to the production environment.
# Usage: ./scripts/setup-production-env.sh [input_file]
# Default input file: .env.production.new

INPUT_FILE="${1:-.env.production.new}"
TARGET_FILE=".env.production"

if [ ! -f "$INPUT_FILE" ]; then
    echo "Error: Input file '$INPUT_FILE' not found."
    exit 1
fi

echo "Applying credentials from $INPUT_FILE to $TARGET_FILE..."

# Backup existing config
if [ -f "$TARGET_FILE" ]; then
    BACKUP_FILE="${TARGET_FILE}.bak.$(date +%Y%m%d%H%M%S)"
    echo "Backing up existing $TARGET_FILE to $BACKUP_FILE..."
    cp "$TARGET_FILE" "$BACKUP_FILE"
fi

# Copy new config
cp "$INPUT_FILE" "$TARGET_FILE"
chmod 600 "$TARGET_FILE"

echo "Success! Updated $TARGET_FILE with secure permissions (0600)."
echo ""
echo "IMPORTANT: You must manually update your database and Redis users to match these new credentials."
echo "Check the comments in $TARGET_FILE for the generated passwords."
