#!/bin/bash
# OpenAPI Validation Script
# Validates all OpenAPI specification files for correctness and consistency

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "========================================"
echo "OpenAPI Validation Script"
echo "========================================"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counters
TOTAL_FILES=0
VALID_FILES=0
INVALID_FILES=0

# Check if spectral is installed
if ! command -v spectral &> /dev/null; then
    echo -e "${YELLOW}Warning: Spectral not found. Installing...${NC}"
    npm install -g @stoplight/spectral-cli
fi

echo "Step 1: Linting OpenAPI files with Spectral"
echo "--------------------------------------------"

# Find all OpenAPI YAML files
API_DIR="$REPO_ROOT/api"
DOCS_DIR="$REPO_ROOT/docs"

for file in "$API_DIR"/*.yaml "$DOCS_DIR"/openapi*.yaml; do
    if [ -f "$file" ]; then
        TOTAL_FILES=$((TOTAL_FILES + 1))
        filename=$(basename "$file")

        echo ""
        echo "Validating: $filename"

        if spectral lint "$file" --ruleset "$REPO_ROOT/.spectral.yaml" 2>&1; then
            echo -e "${GREEN}✓ $filename is valid${NC}"
            VALID_FILES=$((VALID_FILES + 1))
        else
            echo -e "${RED}✗ $filename has errors${NC}"
            INVALID_FILES=$((INVALID_FILES + 1))
        fi
    fi
done

echo ""
echo "Step 2: Checking for common issues"
echo "------------------------------------"

# Check for missing /api/v1 prefixes
echo ""
echo "Checking for paths missing /api/v1 prefix..."
for file in "$API_DIR"/*.yaml "$DOCS_DIR"/openapi*.yaml; do
    if [ -f "$file" ]; then
        filename=$(basename "$file")

        # Skip files that shouldn't have /api/v1 in the raw path entries.
        if [[ "$filename" == "openapi_2fa.yaml" ]] || [[ "$filename" == "openapi_auth_2fa.yaml" ]]; then
            continue
        fi

        # Some specs set /api/v1 at the server level and intentionally keep raw paths
        # relative (for example: /payments/* and legacy /notifications/* specs).
        if grep -Eq '^[[:space:]]*- url:[[:space:]]+.*/api/v1/?$' "$file"; then
            continue
        fi

        # Check for paths that don't start with /api/v1 or /.well-known
        suspicious_paths=$(grep -n "^  /[a-z]" "$file" | grep -v "/api/v1" | grep -v "/.well-known" | grep -v "/oauth" | grep -v "/auth" | grep -v "/health" | grep -v "/ready" | grep -v "/oembed" | grep -v "/inbox" | grep -v "/users" | grep -v "/nodeinfo" || true)

        if [ -n "$suspicious_paths" ]; then
            echo -e "${YELLOW}⚠ $filename may have paths missing /api/v1 prefix:${NC}"
            echo "$suspicious_paths"
        fi
    fi
done

# Check for response wrappers
echo ""
echo "Checking for response wrapper schema..."
for file in "$API_DIR"/*.yaml "$DOCS_DIR"/openapi*.yaml; do
    if [ -f "$file" ]; then
        filename=$(basename "$file")

        if ! grep -q "SuccessResponse" "$file" && ! grep -q "ErrorResponse" "$file"; then
            echo -e "${YELLOW}⚠ $filename doesn't reference SuccessResponse/ErrorResponse schemas${NC}"
        fi
    fi
done

# Check for security schemes
echo ""
echo "Checking for security scheme definitions..."
for file in "$API_DIR"/*.yaml "$DOCS_DIR"/openapi*.yaml; do
    if [ -f "$file" ]; then
        filename=$(basename "$file")

        if ! grep -q "bearerAuth" "$file"; then
            echo -e "${YELLOW}⚠ $filename doesn't define bearerAuth security scheme${NC}"
        fi
    fi
done

echo ""
echo "Step 3: Validating against OpenAPI 3.0 spec"
echo "---------------------------------------------"

# Use openapi-generator to validate
if command -v openapi-generator-cli &> /dev/null; then
    for file in "$API_DIR"/openapi.yaml; do
        if [ -f "$file" ]; then
            filename=$(basename "$file")
            echo ""
            echo "Validating $filename with openapi-generator..."

            if openapi-generator-cli validate -i "$file"; then
                echo -e "${GREEN}✓ $filename passes openapi-generator validation${NC}"
            else
                echo -e "${RED}✗ $filename fails openapi-generator validation${NC}"
            fi
        fi
    done
else
    echo -e "${YELLOW}Skipping openapi-generator validation (not installed)${NC}"
    echo "Install with: npm install -g @openapitools/openapi-generator-cli"
fi

echo ""
echo "Step 4: Testing schema compilation"
echo "------------------------------------"

# Try generating a TypeScript client to test schema validity
TEST_OUTPUT="/tmp/vidra-openapi-test-$$"
mkdir -p "$TEST_OUTPUT"

if command -v openapi-generator-cli &> /dev/null; then
    echo "Generating TypeScript client to verify schemas..."

    if openapi-generator-cli generate \
        -i "$API_DIR/openapi.yaml" \
        -g typescript-axios \
        -o "$TEST_OUTPUT" \
        --skip-validate-spec 2>&1; then

        echo -e "${GREEN}✓ TypeScript client generated successfully${NC}"

        # Try to compile it
        cd "$TEST_OUTPUT"
        if npm install --silent && npm run build --silent; then
            echo -e "${GREEN}✓ TypeScript client compiles successfully${NC}"
        else
            echo -e "${YELLOW}⚠ TypeScript client has compilation warnings${NC}"
        fi
        cd - > /dev/null
    else
        echo -e "${RED}✗ Failed to generate TypeScript client${NC}"
    fi

    # Clean up
    rm -rf "$TEST_OUTPUT"
else
    echo -e "${YELLOW}Skipping client generation test (openapi-generator not installed)${NC}"
fi

echo ""
echo "========================================"
echo "Validation Summary"
echo "========================================"
echo ""
echo "Total files checked: $TOTAL_FILES"
echo -e "${GREEN}Valid files: $VALID_FILES${NC}"
if [ $INVALID_FILES -gt 0 ]; then
    echo -e "${RED}Invalid files: $INVALID_FILES${NC}"
else
    echo "Invalid files: 0"
fi

echo ""
echo "========================================"
echo "Next Steps"
echo "========================================"
echo ""
echo "1. Fix any errors reported above"
echo "2. Review warnings (yellow) - they may indicate issues"
echo "3. Run this script again after making fixes"
echo "4. Consider setting up as a pre-commit hook"
echo ""
echo "For detailed fix instructions, see:"
echo "  - OPENAPI_AUDIT_REPORT.md (detailed analysis)"
echo "  - OPENAPI_FIXES_CHECKLIST.md (step-by-step fixes)"
echo "  - OPENAPI_AUDIT_SUMMARY.md (executive summary)"
echo ""

# Exit with error if any files are invalid
if [ $INVALID_FILES -gt 0 ]; then
    exit 1
else
    exit 0
fi
