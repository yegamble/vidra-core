#!/bin/bash

# Validate that all required Postman test files exist and have the correct file signatures

POSTMAN_DIR="../postman"
if [ ! -z "$1" ]; then
    POSTMAN_DIR="$1"
fi

echo "Validating Postman test files in: $POSTMAN_DIR"

# Check if files exist
FILES=(
    "avatar.png"
    "avatar.jpg"
    "avatar.webp"
    "avatar.gif"
    "avatar.tiff"
    "avatar.heic"
    "document.pdf"
    "malware.png"
)

missing_files=()

for file in "${FILES[@]}"; do
    filepath="$POSTMAN_DIR/$file"
    if [ ! -f "$filepath" ]; then
        missing_files+=("$file")
    else
        echo "✓ Found: $file"

        # Check file signatures
        case "$file" in
            "avatar.png")
                if ! file "$filepath" | grep -q "PNG image"; then
                    echo "⚠️  Warning: $file may not be a valid PNG"
                fi
                ;;
            "avatar.jpg")
                if ! file "$filepath" | grep -q "JPEG image"; then
                    echo "⚠️  Warning: $file may not be a valid JPEG"
                fi
                ;;
            "avatar.gif")
                if ! file "$filepath" | grep -q "GIF image"; then
                    echo "⚠️  Warning: $file may not be a valid GIF"
                fi
                ;;
            "document.pdf")
                if ! file "$filepath" | grep -q "PDF document"; then
                    echo "⚠️  Warning: $file may not be a valid PDF"
                fi
                ;;
            "malware.png")
                if ! file "$filepath" | grep -q "ELF.*executable"; then
                    echo "⚠️  Warning: $file may not be a valid executable (for testing)"
                fi
                ;;
        esac
    fi
done

if [ ${#missing_files[@]} -eq 0 ]; then
    echo ""
    echo "✅ All Postman test files are present!"
    exit 0
else
    echo ""
    echo "❌ Missing files:"
    for file in "${missing_files[@]}"; do
        echo "   - $file"
    done
    echo ""
    echo "Run 'go run scripts/create_postman_test_files.go postman' to create missing files."
    exit 1
fi
