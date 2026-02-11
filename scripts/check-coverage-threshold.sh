#!/usr/bin/env bash

set -euo pipefail

COVERAGE_FILE="${1:-coverage.out}"
MIN_COVERAGE="${2:-23.8}"

if [[ ! -f "${COVERAGE_FILE}" ]]; then
  echo "Coverage file not found: ${COVERAGE_FILE}" >&2
  exit 1
fi

TOTAL_COVERAGE="$(
  go tool cover -func="${COVERAGE_FILE}" | awk '/^total:/ { gsub("%","",$3); print $3 }'
)"

if [[ -z "${TOTAL_COVERAGE}" ]]; then
  echo "Could not parse total coverage from ${COVERAGE_FILE}" >&2
  exit 1
fi

echo "Total coverage: ${TOTAL_COVERAGE}% (minimum: ${MIN_COVERAGE}%)"

if awk -v total="${TOTAL_COVERAGE}" -v min="${MIN_COVERAGE}" 'BEGIN { exit !(total + 0 >= min + 0) }'; then
  echo "Coverage check passed."
else
  echo "Coverage check failed: ${TOTAL_COVERAGE}% is below ${MIN_COVERAGE}%." >&2
  exit 1
fi
