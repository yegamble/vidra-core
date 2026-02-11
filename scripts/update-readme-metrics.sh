#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README_FILE="$ROOT_DIR/README.md"
API_README_FILE="$ROOT_DIR/api/README.md"
BASELINE_REPORT_FILE="$ROOT_DIR/docs/development/TEST_BASELINE_REPORT.md"

CHECK_MODE=0
if [[ "${1:-}" == "--check" ]]; then
	CHECK_MODE=1
fi

format_int() {
	local n="$1"
	local out=""

	while [[ ${#n} -gt 3 ]]; do
		out=",${n: -3}${out}"
		n="${n:0:${#n}-3}"
	done

	printf "%s%s" "$n" "$out"
}

cd "$ROOT_DIR"

go_files="$(rg --files -g '*.go' | wc -l | tr -d ' ')"
non_test_go_files="$(rg --files -g '*.go' -g '!**/*_test.go' | wc -l | tr -d ' ')"
test_files="$(rg --files -g '*_test.go' | wc -l | tr -d ' ')"
migrations="$(rg --files migrations -g '*.sql' | wc -l | tr -d ' ')"
automated_tests="$(rg -n '^func Test' --glob '*_test.go' | wc -l | tr -d ' ')"

source_lines="$(rg --files -0 -g '*.go' -g '!**/*_test.go' | xargs -0 wc -l | awk 'END{print $1+0}')"
test_lines="$(rg --files -0 -g '*_test.go' | xargs -0 wc -l | awk 'END{print $1+0}')"
total_lines=$((source_lines + test_lines))

api_endpoints="$(sed -nE 's/^\| \*\*TOTAL\*\* \| \*\*(~?[0-9]+)\*\* \|.*$/\1/p' "$API_README_FILE" | head -n 1)"
if [[ -z "$api_endpoints" ]]; then
	api_endpoints="unknown"
fi

coverage_baseline="$(sed -nE 's/^- \*\*Overall Code Coverage\*\*: ([0-9.]+%).*$/\1/p' "$BASELINE_REPORT_FILE" | head -n 1)"
if [[ -z "$coverage_baseline" ]]; then
	coverage_baseline="unknown"
fi

coverage_date="$(sed -nE 's/^\*\*Generated\*\*: ([0-9]{4}-[0-9]{2}-[0-9]{2}).*$/\1/p' "$BASELINE_REPORT_FILE" | head -n 1)"
if [[ -z "$coverage_date" ]]; then
	coverage_date="unknown date"
fi

go_files_fmt="$(format_int "$go_files")"
non_test_go_files_fmt="$(format_int "$non_test_go_files")"
test_files_fmt="$(format_int "$test_files")"
source_lines_fmt="$(format_int "$source_lines")"
test_lines_fmt="$(format_int "$test_lines")"
total_lines_fmt="$(format_int "$total_lines")"
automated_tests_fmt="$(format_int "$automated_tests")"

tmp_file="$(mktemp)"
trap 'rm -f "$tmp_file"' EXIT

awk \
	-v go_files_fmt="$go_files_fmt" \
	-v non_test_go_files_fmt="$non_test_go_files_fmt" \
	-v test_files_fmt="$test_files_fmt" \
	-v source_lines_fmt="$source_lines_fmt" \
	-v test_lines_fmt="$test_lines_fmt" \
	-v total_lines_fmt="$total_lines_fmt" \
	-v migrations="$migrations" \
	-v api_endpoints="$api_endpoints" \
	-v coverage_baseline="$coverage_baseline" \
	-v coverage_date="$coverage_date" \
	-v automated_tests_fmt="$automated_tests_fmt" '
BEGIN { state = "" }
{
	if ($0 == "## 📊 Project Metrics") {
		print $0
		print ""
		print "| Metric | Count | Description |"
		print "|--------|-------|-------------|"
		print "| **Go Files** | " go_files_fmt " | Total Go files (" non_test_go_files_fmt " non-test) |"
		print "| **Test Files** | " test_files_fmt " | Test files across unit, integration, and E2E suites |"
		print "| **Lines of Code** | " total_lines_fmt "+ | ~" source_lines_fmt " source + ~" test_lines_fmt " test code |"
		print "| **Database Migrations** | " migrations " | Goose SQL migrations |"
		print "| **API Endpoints** | " api_endpoints " | RESTful + WebSocket + Federation (OpenAPI-documented) |"
		print "| **Coverage Baseline** | " coverage_baseline " | Latest full-repo baseline (`docs/development/TEST_BASELINE_REPORT.md`, " coverage_date ") |"
		print "| **Security Tests** | 50+ | Including SSRF, virus scanning, auth |"
		print "| **Automated Tests** | " automated_tests_fmt " | `func Test*` count across `*_test.go` files |"
		state = "skip_project_metrics_table"
		next
	}

	if (state == "skip_project_metrics_table") {
		if ($0 ~ /^[[:space:]]*\|/ || $0 ~ /^[[:space:]]*$/) {
			next
		}
		print ""
		state = ""
	}

	if ($0 == "### Test Metrics") {
		print $0
		print ""
		print "| Metric | Count | Description |"
		print "|--------|-------|-------------|"
		print "| **Go Files** | " go_files_fmt " | Total Go files (" non_test_go_files_fmt " non-test) |"
		print "| **Test Files** | " test_files_fmt " | Test files across unit, integration, and E2E suites |"
		print "| **Lines of Code** | " total_lines_fmt "+ | ~" source_lines_fmt " source + ~" test_lines_fmt " test code |"
		print "| **Database Migrations** | " migrations " | Goose SQL migrations |"
		print "| **API Endpoints** | " api_endpoints " | RESTful + WebSocket + Federation (OpenAPI-documented) |"
		print "| **Test Coverage** | " coverage_baseline " baseline | Latest full-repo baseline (`docs/development/TEST_BASELINE_REPORT.md`, " coverage_date ") |"
		print "| **Security Tests** | 50+ | SSRF, virus scanning, auth, input validation |"
		print "| **Automated Tests** | " automated_tests_fmt " | `func Test*` count across `*_test.go` files |"
		state = "skip_test_metrics_table"
		next
	}

	if (state == "skip_test_metrics_table") {
		if ($0 ~ /^[[:space:]]*\|/ || $0 ~ /^[[:space:]]*$/) {
			next
		}
		print ""
		state = ""
	}

	print $0
}
' "$README_FILE" > "$tmp_file"

if cmp -s "$README_FILE" "$tmp_file"; then
	echo "README metrics are already up to date."
	exit 0
fi

if [[ "$CHECK_MODE" -eq 1 ]]; then
	echo "README metrics are out of date. Run: make update-readme-metrics"
	exit 1
fi

mv "$tmp_file" "$README_FILE"
echo "Updated README metric tables in $README_FILE"
