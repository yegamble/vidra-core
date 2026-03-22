#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README_FILE="$ROOT_DIR/README.md"

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

tmp_all_files="$(mktemp)"
tmp_go_files="$(mktemp)"
tmp_non_test_go_files="$(mktemp)"
tmp_test_files="$(mktemp)"
tmp_migration_files="$(mktemp)"
tmp_api_openapi_files="$(mktemp)"
tmp_docs_openapi_files="$(mktemp)"
tmp_file="$(mktemp)"
trap 'rm -f "$tmp_file" "$tmp_all_files" "$tmp_go_files" "$tmp_non_test_go_files" "$tmp_test_files" "$tmp_migration_files" "$tmp_api_openapi_files" "$tmp_docs_openapi_files"' EXIT

collect_file_inventory() {
	if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		{
			git ls-files
			git ls-files --others --exclude-standard
		} | awk 'NF > 0 && $0 !~ /(^|\/)\./' | sort -u > "$tmp_all_files"
		return
	fi

	find . -type f -not -path './.git/*' -print | sed 's#^\./##' | awk '$0 !~ /(^|\/)\./' > "$tmp_all_files"
}

sum_line_counts() {
	local list_file="$1"
	local total=0
	local file=""
	local line_count=0

	while IFS= read -r file; do
		[[ -z "$file" ]] && continue
		[[ ! -f "$file" ]] && continue

		line_count="$(wc -l < "$file")"
		line_count="${line_count//[[:space:]]/}"
		total=$((total + line_count))
	done < "$list_file"

	printf "%s" "$total"
}

count_test_functions() {
	local list_file="$1"
	local total=0
	local file=""
	local count=0

	while IFS= read -r file; do
		[[ -z "$file" ]] && continue
		[[ ! -f "$file" ]] && continue

		count="$(grep -E -c '^func Test' "$file" || true)"
		count="${count//[[:space:]]/}"
		[[ -z "$count" ]] && count=0
		total=$((total + count))
	done < "$list_file"

	printf "%s" "$total"
}

collect_file_inventory

awk '/\.go$/ {print}' "$tmp_all_files" > "$tmp_go_files"
awk '/_test\.go$/ {print}' "$tmp_all_files" > "$tmp_test_files"
awk '/\.go$/ && $0 !~ /_test\.go$/ {print}' "$tmp_all_files" > "$tmp_non_test_go_files"
awk '/^migrations\/.*\.sql$/ {print}' "$tmp_all_files" > "$tmp_migration_files"
awk '/^api\/openapi.*\.yaml$/ {print}' "$tmp_all_files" > "$tmp_api_openapi_files"
awk '/^docs\/openapi.*\.yaml$/ {print}' "$tmp_all_files" > "$tmp_docs_openapi_files"

go_files="$(awk 'END{print NR+0}' "$tmp_go_files")"
non_test_go_files="$(awk 'END{print NR+0}' "$tmp_non_test_go_files")"
test_files="$(awk 'END{print NR+0}' "$tmp_test_files")"
migrations="$(awk 'END{print NR+0}' "$tmp_migration_files")"
automated_tests="$(count_test_functions "$tmp_test_files")"
api_openapi_specs="$(awk 'END{print NR+0}' "$tmp_api_openapi_files")"
docs_openapi_specs="$(awk 'END{print NR+0}' "$tmp_docs_openapi_files")"
openapi_total=$((api_openapi_specs + docs_openapi_specs))

source_lines="$(sum_line_counts "$tmp_non_test_go_files")"
test_lines="$(sum_line_counts "$tmp_test_files")"
total_lines=$((source_lines + test_lines))

go_files_fmt="$(format_int "$go_files")"
non_test_go_files_fmt="$(format_int "$non_test_go_files")"
test_files_fmt="$(format_int "$test_files")"
source_lines_fmt="$(format_int "$source_lines")"
test_lines_fmt="$(format_int "$test_lines")"
total_lines_fmt="$(format_int "$total_lines")"
automated_tests_fmt="$(format_int "$automated_tests")"
openapi_total_fmt="$(format_int "$openapi_total")"

awk \
	-v go_files_fmt="$go_files_fmt" \
	-v non_test_go_files_fmt="$non_test_go_files_fmt" \
	-v test_files_fmt="$test_files_fmt" \
	-v source_lines_fmt="$source_lines_fmt" \
	-v test_lines_fmt="$test_lines_fmt" \
	-v total_lines_fmt="$total_lines_fmt" \
	-v migrations="$migrations" \
	-v openapi_total_fmt="$openapi_total_fmt" \
	-v api_openapi_specs="$api_openapi_specs" \
	-v docs_openapi_specs="$docs_openapi_specs" \
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
		print "| **OpenAPI Files** | " openapi_total_fmt " | " api_openapi_specs " specs in `api/` plus " docs_openapi_specs " legacy standalone spec in `docs/` |"
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
		print "| **OpenAPI Files** | " openapi_total_fmt " | " api_openapi_specs " specs in `api/` plus " docs_openapi_specs " legacy standalone spec in `docs/` |"
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
