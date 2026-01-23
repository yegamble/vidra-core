#!/bin/bash
set -e

# rotate-credentials.sh
# Generates secure random credentials for Athena production deployment.
# Usage: ./scripts/rotate-credentials.sh [output_file]
# Default output is .env.production.new

OUTPUT_FILE="${1:-.env.production.new}"

echo "Generating secure credentials to $OUTPUT_FILE..."

# Generate secrets
JWT_SECRET=$(openssl rand -hex 32)
DB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+' | cut -c1-24)
REDIS_PASSWORD=$(openssl rand -hex 24)
ENCRYPTION_KEY=$(openssl rand -hex 32)
ACTIVITYPUB_KEY=$(openssl rand -base64 32)
API_KEY=$(openssl rand -hex 16)

cat <<EOF > "$OUTPUT_FILE"
# Athena Production Configuration (Generated $(date))
# SECURITY WARNING: Store this file securely. Do not commit to git.

# Server
NODE_ENV=production
LOG_LEVEL=info

# Database
DATABASE_URL=postgres://athena_user:${DB_PASSWORD}@localhost:5432/athena?sslmode=require&pool_max_conns=25

# Redis
REDIS_URL=redis://:${REDIS_PASSWORD}@localhost:6379/0

# Security
JWT_SECRET=${JWT_SECRET}
ACTIVITYPUB_KEY_ENCRYPTION_KEY=${ACTIVITYPUB_KEY}
ENCRYPTION_KEY=${ENCRYPTION_KEY} # For E2EE or other internal encryption
API_KEY=${API_KEY} # For internal services

# ... Add other non-secret config from .env.example ...
EOF

echo "Done! Credentials written to $OUTPUT_FILE"
echo "IMPORTANT: Update your database and Redis users with these new passwords manually."
echo "Example:"
echo "  ALTER USER athena_user WITH PASSWORD '${DB_PASSWORD}';"
