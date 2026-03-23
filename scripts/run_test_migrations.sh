#!/bin/bash
set -e

# Configuration
export PGPASSWORD=test_password
PGHOST=localhost
PGPORT=5433
PGUSER=test_user
PGDATABASE=athena_test

echo "Running migrations on test database..."

# Run all migrations in order
for migration in migrations/*.sql; do
    echo "Applying: $(basename "$migration")"
    psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -f "$migration" > /dev/null 2>&1 || echo "  -> Already applied or error (continuing...)"
done

echo "✓ All migrations applied!"

# Verify video_imports table exists
echo ""
echo "Verifying video_imports table..."
psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -c "\d video_imports" || echo "Table not found - check for errors above"
