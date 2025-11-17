#!/usr/bin/env bash
set -euo pipefail

# Idempotent migration applier for Postgres.
# Usage:
#   DATABASE_URL=postgres://user:pass@host:port/db ./scripts/migrate_idempotent.sh

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "ERROR: DATABASE_URL is not set" >&2
  exit 2
fi

shopt -s nullglob
MIG_FILES=(migrations/*.sql)
if (( ${#MIG_FILES[@]} == 0 )); then
  echo "No migration files found in migrations/" >&2
  exit 0
fi

echo "Applying migrations to ${DATABASE_URL} (idempotent)"

for f in "${MIG_FILES[@]}"; do
  echo "-- Applying ${f}"
  # Capture stderr to a temp file to inspect errors while keeping output clean
  TMP_ERR=$(mktemp)
  if psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f" 1>/dev/null 2>"$TMP_ERR"; then
    echo "   OK"
    rm -f "$TMP_ERR"
    continue
  fi

  ERR_MSG=$(tr '\n' ' ' < "$TMP_ERR")
  rm -f "$TMP_ERR"

  # Allow common idempotent errors and continue
  if echo "$ERR_MSG" | grep -Eqi "(already exists|duplicate key|duplicate object|relation .* exists|function .* exists|index .* exists|type .* exists|trigger .* exists|schema .* exists)"; then
    echo "   Skipping (idempotent): $ERR_MSG"
    continue
  fi

  echo "ERROR applying ${f}: $ERR_MSG" >&2
  exit 1
done

echo "All migrations applied (idempotent)."
