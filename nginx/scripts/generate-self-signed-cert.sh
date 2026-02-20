#!/bin/sh
# Generate self-signed SSL certificates for Athena Nginx
# Usage: ./generate-self-signed-cert.sh [domain]
# Example: ./generate-self-signed-cert.sh localhost

set -e

DOMAIN="${1:-localhost}"
SSL_DIR="${SSL_DIR:-/etc/nginx/ssl}"
CERT_FILE="$SSL_DIR/self-signed.crt"
KEY_FILE="$SSL_DIR/self-signed.key"
DH_FILE="$SSL_DIR/dhparam.pem"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Create SSL directory if it doesn't exist
mkdir -p "$SSL_DIR"

# Check if certificates already exist (idempotent)
if [ -f "$CERT_FILE" ] && [ -f "$KEY_FILE" ]; then
    printf "${YELLOW}SSL certificates already exist at $CERT_FILE${NC}\n"
    echo "Skipping generation (idempotent)"
    exit 0
fi

printf "${GREEN}Generating self-signed SSL certificate for $DOMAIN${NC}\n"

# Check if openssl is available
if ! command -v openssl >/dev/null 2>&1; then
    echo "ERROR: openssl command not found"
    echo "Please install openssl: apk add openssl"
    exit 1
fi

# Generate self-signed certificate with SAN
# Using -nodes for no passphrase (automatic startup)
# 365 days validity
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout "$KEY_FILE" \
    -out "$CERT_FILE" \
    -subj "/C=US/ST=State/L=City/O=Athena/CN=$DOMAIN" \
    -addext "subjectAltName=DNS:$DOMAIN,DNS:*.$DOMAIN,DNS:localhost,IP:127.0.0.1"

# Set appropriate permissions
chmod 600 "$KEY_FILE"
chmod 644 "$CERT_FILE"

printf "${GREEN}Certificate generated successfully${NC}\n"
echo "  Cert: $CERT_FILE"
echo "  Key: $KEY_FILE"

# Generate DH parameters if not present (using fast -dsaparam method)
if [ ! -f "$DH_FILE" ]; then
    printf "${GREEN}Generating Diffie-Hellman parameters (fast method)...${NC}\n"
    openssl dhparam -dsaparam -out "$DH_FILE" 2048
    chmod 644 "$DH_FILE"
    printf "${GREEN}DH parameters generated: $DH_FILE${NC}\n"
else
    printf "${YELLOW}DH parameters already exist at $DH_FILE${NC}\n"
fi

printf "${GREEN}Self-signed certificate setup complete${NC}\n"
