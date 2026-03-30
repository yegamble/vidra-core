#!/usr/bin/env bash

# Enforce Vidra Core autonomous-mode guardrails on staged changes.
# This script is shared by Claude hooks and git pre-commit so the rules stay aligned.

set -euo pipefail

if [ -n "${REPO_ROOT:-}" ]; then
    repo_root="$REPO_ROOT"
elif command -v git >/dev/null 2>&1 && git rev-parse --show-toplevel >/dev/null 2>&1; then
    repo_root="$(git rev-parse --show-toplevel)"
else
    repo_root="$(pwd)"
fi

cd "$repo_root"

staged_files=()
added_files=()
failures=0

collect_staged_files() {
    if [ -n "${VIDRA_STOP_HOOK_STAGED_FILES:-}" ]; then
        while IFS= read -r file; do
            [ -n "$file" ] && staged_files+=("$file")
        done <<EOF
${VIDRA_STOP_HOOK_STAGED_FILES}
EOF
        return
    fi

    while IFS= read -r -d '' file; do
        staged_files+=("$file")
    done < <(git diff --cached --name-only --diff-filter=ACMR -z 2>/dev/null || true)
}

collect_added_files() {
    if [ -n "${VIDRA_STOP_HOOK_ADDED_FILES:-}" ]; then
        while IFS= read -r file; do
            [ -n "$file" ] && added_files+=("$file")
        done <<EOF
${VIDRA_STOP_HOOK_ADDED_FILES}
EOF
        return
    fi

    while IFS= read -r -d '' file; do
        added_files+=("$file")
    done < <(git diff --cached --name-only --diff-filter=A -z 2>/dev/null || true)
}

matches_any() {
    local file="$1"
    shift

    for pattern in "$@"; do
        case "$file" in
            $pattern)
                return 0
                ;;
        esac
    done

    return 1
}

stop() {
    printf 'STOP: %s\n' "$1" >&2
    failures=$((failures + 1))
}

collect_staged_files
collect_added_files

if [ "${#staged_files[@]}" -eq 0 ]; then
    exit 0
fi

prod_go_changed=false
go_tests_changed=false
route_or_spec_changed=false
openapi_changed=false
postman_changed=false
registry_changed=false
docs_changed=false
generated_changed=false
postman_results_changed=false
added_prod_artifacts=false

for file in "${staged_files[@]}"; do
    if matches_any "$file" ".env" ".env.*" "*credentials*" "*secret*"; then
        stop "Refusing to commit potential secrets file: $file"
    fi

    if matches_any "$file" "internal/generated/*"; then
        generated_changed=true
    fi

    if matches_any "$file" "postman/results-*" "postman/test-results.json"; then
        postman_results_changed=true
    fi

    if matches_any "$file" "*_test.go" "tests/*"; then
        go_tests_changed=true
    fi

    if matches_any "$file" "postman/*.postman_collection.json"; then
        postman_changed=true
    fi

    if matches_any "$file" "api/*.yaml"; then
        openapi_changed=true
        route_or_spec_changed=true
    fi

    if matches_any "$file" "internal/httpapi/routes.go"; then
        route_or_spec_changed=true
    fi

    if matches_any "$file" ".claude/rules/feature-parity-registry.md"; then
        registry_changed=true
    fi

    if matches_any "$file" \
        "README.md" \
        "CLAUDE.md" \
        "AGENTS.md" \
        "docs/*" \
        "internal/*/CLAUDE.md" \
        "internal/*/AGENTS.md" \
        "migrations/CLAUDE.md" \
        "migrations/AGENTS.md"; then
        docs_changed=true
    fi

    if matches_any "$file" "cmd/*.go" "internal/*.go" "pkg/*.go"; then
        if ! matches_any "$file" "internal/generated/*" "*_test.go"; then
            prod_go_changed=true
        fi
    fi
done

if [ "${#added_files[@]}" -gt 0 ]; then
    for file in "${added_files[@]}"; do
        if matches_any "$file" "cmd/*.go" "internal/*.go" "pkg/*.go" "migrations/*.sql" "api/*.yaml"; then
            if ! matches_any "$file" "internal/generated/*" "*_test.go"; then
                added_prod_artifacts=true
            fi
        fi
    done
fi

if [ "$generated_changed" = true ] && [ "$openapi_changed" = false ]; then
    stop "Generated code under internal/generated/ changed without a matching api/*.yaml update. Edit the spec and regenerate instead."
fi

if [ "$postman_results_changed" = true ]; then
    stop "Do not stage Newman/Postman result artifacts. Commit the source collection, not results-*.json or test-results.json."
fi

if [ "$prod_go_changed" = true ] && [ "$go_tests_changed" = false ]; then
    stop "Production Go changes are staged without Go test updates. Add or update *_test.go coverage before committing."
fi

if [ "$route_or_spec_changed" = true ] && [ "$openapi_changed" = false ]; then
    stop "API route changes are staged without an OpenAPI spec update. Keep PeerTube-facing contracts in api/*.yaml."
fi

if [ "$route_or_spec_changed" = true ] && [ "$postman_changed" = false ]; then
    stop "API route or OpenAPI changes are staged without Postman/Newman coverage updates."
fi

if [ "$route_or_spec_changed" = true ] && [ "$docs_changed" = false ]; then
    stop "API surface changes are staged without README/CLAUDE/docs updates. Document the behavior to avoid vision drift."
fi

if [ "$added_prod_artifacts" = true ] && [ "$registry_changed" = false ]; then
    stop "New production artifacts are staged without updating .claude/rules/feature-parity-registry.md."
fi

if [ "$failures" -gt 0 ]; then
    printf 'Autonomous stop hooks failed with %d issue(s).\n' "$failures" >&2
    exit 1
fi

echo "✓ Autonomous stop hooks passed"
