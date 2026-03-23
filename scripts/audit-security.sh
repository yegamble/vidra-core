#!/bin/bash
set -e

# audit-security.sh
# Runs security checks including gosec and dependency analysis.

echo "========================================================"
echo "   SECURITY AUDIT"
echo "========================================================"

# Check if gosec is installed
if ! command -v gosec &> /dev/null; then
    echo "gosec not found. Installing..."
    go install github.com/securego/gosec/v2/cmd/gosec@latest
    export PATH=$PATH:$(go env GOPATH)/bin
fi

echo "Running gosec..."
# Run gosec on all files, excluding tests if preferred, but tests are useful to check too.
# We exclude 'tests' directory if it contains separate integration tests that trigger false positives.
# -no-fail to just report issues without breaking build immediately if desired,
# but for an audit we want to see output.
gosec -exclude-dir=tests -exclude-dir=testutil ./...

echo ""
echo "========================================================"
echo "   DEPENDENCY CHECK"
echo "========================================================"
echo "Checking for outdated dependencies..."
go list -u -m all | grep "\[" || echo "All dependencies up to date."

echo ""
echo "========================================================"
echo "Audit Complete."
echo "========================================================"
