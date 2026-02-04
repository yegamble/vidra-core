#!/bin/bash
set -e

# setup-production-env.sh
# Helpers for setting up a production environment using rotated credentials.

GENERATED_ENV=".env.production.new"

echo "========================================================"
echo "   Athena Production Environment Setup"
echo "========================================================"

if [ ! -f "$GENERATED_ENV" ]; then
    echo "Error: $GENERATED_ENV not found."
    echo "Please run './scripts/rotate-credentials.sh' first."
    exit 1
fi

echo "Found generated credentials in $GENERATED_ENV"
echo ""
echo "Security Checks:"
# Check for weak passwords (basic length check)
if grep -q "postgres://.*:athena_password@" "$GENERATED_ENV"; then
    echo "WARNING: Default database password detected!"
else
    echo "✅ Database password looks changed."
fi

if grep -q "JWT_SECRET=your-super-secret-jwt-key" "$GENERATED_ENV"; then
    echo "WARNING: Default JWT secret detected!"
else
    echo "✅ JWT Secret looks changed."
fi

echo ""
echo "Instructions:"
echo "1. Review the contents of $GENERATED_ENV"
echo "2. Move it to your production .env file:"
echo "   mv $GENERATED_ENV .env"
echo "   (Or copy the values to your production secret manager)"
echo "3. Update your database users with the new passwords."
echo ""
read -p "Do you want to preview the file (secrets will be shown)? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    cat "$GENERATED_ENV"
fi

echo ""
echo "Setup helper complete."
