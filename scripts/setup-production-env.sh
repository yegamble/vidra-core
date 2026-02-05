#!/bin/bash
set -e

# setup-production-env.sh
# Helper to apply generated credentials and setup production environment.

NEW_ENV_FILE=".env.production.new"
TARGET_ENV_FILE=".env.production"

echo "Checking for generated credentials in $NEW_ENV_FILE..."

if [ ! -f "$NEW_ENV_FILE" ]; then
    echo "Error: $NEW_ENV_FILE not found."
    echo "Please run ./scripts/rotate-credentials.sh first."
    exit 1
fi

echo "Found $NEW_ENV_FILE."
echo "Setting permissions to 600..."
chmod 600 "$NEW_ENV_FILE"

echo "Comparing with existing $TARGET_ENV_FILE..."
if [ -f "$TARGET_ENV_FILE" ]; then
    echo "Warning: $TARGET_ENV_FILE already exists."
    read -p "Do you want to backup the existing file and replace it? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted. Please manually merge the files."
        exit 1
    fi
    mv "$TARGET_ENV_FILE" "${TARGET_ENV_FILE}.bak.$(date +%s)"
    echo "Backed up existing file."
fi

cp "$NEW_ENV_FILE" "$TARGET_ENV_FILE"
echo "Applied new configuration to $TARGET_ENV_FILE."

# Clean up
read -p "Do you want to remove the temporary file $NEW_ENV_FILE? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    rm "$NEW_ENV_FILE"
    echo "Removed $NEW_ENV_FILE."
fi

echo ""
echo "========================================================"
echo "Production environment setup complete!"
echo "Next steps:"
echo "1. Verify the contents of $TARGET_ENV_FILE"
echo "2. Run database migrations: make migrate-up"
echo "3. Restart the application service."
echo "========================================================"
