#!/bin/bash
set -e

# rotate-credentials.sh
# Generates secure random credentials for Vidra Core production deployment.
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
HLS_SIGNING_SECRET=$(openssl rand -base64 48)

cat <<EOF > "$OUTPUT_FILE"
# Vidra Core Production Configuration (Generated $(date))
# SECURITY WARNING: Store this file securely. Do not commit to git.

# Server
NODE_ENV=production
LOG_LEVEL=info
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

# Database
# Note: Update your database user password manually using the generated password below.
DATABASE_URL=postgres://vidra_user:${DB_PASSWORD}@localhost:5432/vidra?sslmode=require&pool_max_conns=25
DATABASE_MAX_CONNECTIONS=25

# Redis
# Note: Update your Redis configuration to require this password.
REDIS_URL=redis://:${REDIS_PASSWORD}@localhost:6379/0

# Security - Application Secrets
JWT_SECRET=${JWT_SECRET}
JWT_ACCESS_TOKEN_EXPIRY=15m
JWT_REFRESH_TOKEN_EXPIRY=7d
ACTIVITYPUB_KEY_ENCRYPTION_KEY=${ACTIVITYPUB_KEY}
ENCRYPTION_KEY=${ENCRYPTION_KEY} # For E2EE or other internal encryption
API_KEY=${API_KEY} # For internal services
HLS_SIGNING_SECRET=${HLS_SIGNING_SECRET} # For private video streaming

# External Services (Placeholders - MUST BE UPDATED MANUALLY)
# S3 / Object Storage
S3_ENABLED=true
S3_ENDPOINT=https://s3.amazonaws.com
S3_BUCKET=vidra-videos
S3_REGION=us-east-1
S3_ACCESS_KEY=<CHANGE_ME_S3_ACCESS_KEY>
S3_SECRET_KEY=<CHANGE_ME_S3_SECRET_KEY>

# SMTP / Email
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=vidra-noreply@example.com
SMTP_PASSWORD=<CHANGE_ME_SMTP_PASSWORD>
SMTP_FROM=Vidra Core <noreply@example.com>

# Virus Scanning (ClamAV)
CLAMAV_ADDRESS=clamav:3310
CLAMAV_FALLBACK_MODE=strict

# Federation (Optional)
FEDERATION_ENABLED=true
ATPROTO_ENABLED=false
EOF

echo "Done! Credentials written to $OUTPUT_FILE"
echo ""
echo "IMPORTANT MANUAL STEPS REQUIRED:"
echo "1. Update PostgreSQL user password:"
echo "   ALTER USER vidra_user WITH PASSWORD '${DB_PASSWORD}';"
echo ""
echo "2. Update Redis configuration (redis.conf) or ACL:"
echo "   requirepass ${REDIS_PASSWORD}"
echo ""
echo "3. Update External Service Credentials in $OUTPUT_FILE:"
echo "   - S3_ACCESS_KEY / S3_SECRET_KEY"
echo "   - SMTP_PASSWORD"
echo ""
echo "4. Deploy the new configuration:"
echo "   cp $OUTPUT_FILE .env.production"
echo "   docker compose up -d"
