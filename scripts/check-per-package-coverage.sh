#!/usr/bin/env bash
#
# check-per-package-coverage.sh - Enforce per-package coverage thresholds
#
# Usage: ./scripts/check-per-package-coverage.sh <coverage.out> [thresholds-file]
#
# Runs per-package coverage checks by invoking `go test -cover` for each
# package listed in the thresholds file and comparing against minimum
# thresholds. Exits non-zero if any package falls below its threshold.

set -euo pipefail

COVERAGE_FILE="${1:-coverage.out}"
THRESHOLDS_FILE="${2:-scripts/coverage-thresholds.txt}"

if [[ ! -f "${COVERAGE_FILE}" ]]; then
  echo "Coverage file not found: ${COVERAGE_FILE}" >&2
  exit 1
fi

if [[ ! -f "${THRESHOLDS_FILE}" ]]; then
  echo "Thresholds file not found: ${THRESHOLDS_FILE}" >&2
  exit 1
fi

# Use go tool cover to get per-function breakdown, then aggregate by package
FUNC_OUTPUT="$(go tool cover -func="${COVERAGE_FILE}" 2>/dev/null)"

FAILED=0
CHECKED=0

while IFS= read -r line; do
  # Skip comments and blank lines
  [[ "${line}" =~ ^[[:space:]]*# ]] && continue
  [[ -z "${line// /}" ]] && continue

  # Parse: package_path  threshold
  PKG="$(echo "${line}" | awk '{print $1}')"
  MIN="$(echo "${line}" | awk '{print $2}')"

  if [[ -z "${PKG}" || -z "${MIN}" ]]; then
    continue
  fi

  # Compute per-package coverage from the func output.
  # Extract all function lines for this package (prefix "vidra-core/<PKG>/")
  # and compute the average coverage.
  PKG_COVERAGE="$(echo "${FUNC_OUTPUT}" \
    | grep "^vidra-core/${PKG}/" \
    | grep -v '^total:' \
    | awk '
      {
        gsub("%", "", $NF);
        sum += $NF;
        count++;
      }
      END {
        if (count > 0) printf "%.1f", sum / count;
        else print "";
      }
    ')"

  # If no coverage data found, the package might have 0% or no test files
  if [[ -z "${PKG_COVERAGE}" ]]; then
    # Check if the package appears at all in the coverage file
    if grep -q "vidra-core/${PKG}/" "${COVERAGE_FILE}" 2>/dev/null; then
      PKG_COVERAGE="0.0"
    else
      echo "  SKIP  ${PKG} (not in coverage profile)"
      continue
    fi
  fi

  CHECKED=$((CHECKED + 1))

  if awk -v cov="${PKG_COVERAGE}" -v min="${MIN}" 'BEGIN { exit !(cov + 0 >= min + 0) }'; then
    printf "  PASS  %-45s %6s%% >= %s%%\n" "${PKG}" "${PKG_COVERAGE}" "${MIN}"
  else
    printf "  FAIL  %-45s %6s%% <  %s%%\n" "${PKG}" "${PKG_COVERAGE}" "${MIN}"
    FAILED=$((FAILED + 1))
  fi
done < "${THRESHOLDS_FILE}"

echo ""
echo "Per-package coverage: ${CHECKED} checked, ${FAILED} failed"

if [[ ${FAILED} -gt 0 ]]; then
  echo "Coverage check FAILED: ${FAILED} package(s) below threshold." >&2
  exit 1
fi

echo "All per-package coverage thresholds met."
