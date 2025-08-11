#!/bin/sh
set -e

echo "Running database migrations..."
atlas migrate apply --dir file://migrations --url "$DATABASE_URL"

echo "Starting server..."
exec ./server
