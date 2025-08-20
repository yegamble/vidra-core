#!/usr/bin/env bash
set -euo pipefail

CONFIG_FILE="oapi-codegen.yaml"
SPEC_FILE="api/openapi.yaml"

if ! command -v oapi-codegen >/dev/null 2>&1; then
  echo "oapi-codegen not found. Install with: go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@latest" >&2
  exit 1
fi

oapi-codegen -config "$CONFIG_FILE" "$SPEC_FILE"
echo "OpenAPI types regenerated into internal/generated"

