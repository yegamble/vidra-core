#!/bin/sh
set -e

# Auto-migration is handled by the Go app on startup
# Set AUTO_MIGRATE=false to disable if needed
echo "Starting server..."
exec ./server
