#!/usr/bin/env bash
set -euo pipefail

# verify-openapi.sh - Verify that generated OpenAPI types are up-to-date.
# Regenerates types from the spec and fails if the result differs from what is committed.
# Used in CI to enforce that developers run `make generate-openapi` after spec changes.

GENERATED_FILE="internal/generated/types.go"

echo "Regenerating OpenAPI types from spec..."
scripts/gen-openapi.sh

echo "Checking for uncommitted changes in $GENERATED_FILE..."
if ! git diff --exit-code -- "$GENERATED_FILE"; then
  echo ""
  echo "ERROR: Generated OpenAPI types are out of date."
  echo "The file $GENERATED_FILE does not match the current api/openapi.yaml spec."
  echo ""
  echo "To fix: run 'make generate-openapi' and commit the result."
  exit 1
fi

echo "OpenAPI types are up to date."
