#!/usr/bin/env bash
set -euo pipefail

SPEC_FILE="api/openapi.yaml"
OUT_DIR="internal/generated"
OUT_FILE="$OUT_DIR/types.go"
PKG="generated"

mkdir -p "$OUT_DIR"

if command -v oapi-codegen >/dev/null 2>&1; then
  oapi-codegen -generate types -o "$OUT_FILE" -package "$PKG" "$SPEC_FILE"
else
  echo "oapi-codegen not found in PATH; using go run to execute generator" >&2
  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -generate types -o "$OUT_FILE" -package "$PKG" "$SPEC_FILE"
fi

echo "OpenAPI types regenerated into $OUT_FILE"
