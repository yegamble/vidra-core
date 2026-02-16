#!/bin/bash
# Nginx entrypoint script for Athena
# Handles conditional SSL certificate generation before starting nginx

set -e

# Environment variables with defaults
NGINX_PROTOCOL="${NGINX_PROTOCOL:-http}"
NGINX_TLS_MODE="${NGINX_TLS_MODE:-self-signed}"
NGINX_DOMAIN="${NGINX_DOMAIN:-localhost}"
SSL_DIR="${SSL_DIR:-/etc/nginx/ssl}"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}Athena Nginx Entrypoint${NC}"
echo "Protocol: $NGINX_PROTOCOL"
echo "Domain: $NGINX_DOMAIN"

# Certificate generation logic
if [ "$NGINX_PROTOCOL" = "https" ]; then
    echo -e "${GREEN}HTTPS mode enabled${NC}"

    CERT_FILE="$SSL_DIR/self-signed.crt"
    KEY_FILE="$SSL_DIR/self-signed.key"

    # Check if certificates exist
    if [ "$NGINX_TLS_MODE" = "self-signed" ]; then
        if [ ! -f "$CERT_FILE" ] || [ ! -f "$KEY_FILE" ]; then
            echo -e "${GREEN}Generating self-signed certificates...${NC}"

            # Run certificate generation script
            if /etc/nginx/scripts/generate-self-signed-cert.sh "$NGINX_DOMAIN"; then
                echo -e "${GREEN}Self-signed certificates generated successfully${NC}"
            else
                echo -e "${RED}WARNING: Certificate generation failed${NC}"
                echo -e "${YELLOW}Falling back to HTTP mode${NC}"
                export NGINX_PROTOCOL="http"
            fi
        else
            echo -e "${YELLOW}SSL certificates already exist${NC}"
        fi
    elif [ "$NGINX_TLS_MODE" = "letsencrypt" ]; then
        # Let's Encrypt certificates are managed by certbot container
        # Check if they exist
        LE_CERT="/etc/letsencrypt/live/$NGINX_DOMAIN/fullchain.pem"
        if [ ! -f "$LE_CERT" ]; then
            echo -e "${YELLOW}Let's Encrypt certificates not yet available${NC}"
            echo "Waiting for certbot to acquire certificates..."
            echo "If this is first run, ensure certbot container is running and domain points to this server"
            # Note: Nginx will start anyway, serving HTTP-only config until certs are available
        fi
    fi
else
    echo -e "${YELLOW}HTTP mode - no certificates needed${NC}"
fi

# Start nginx (replace current process)
echo -e "${GREEN}Starting Nginx...${NC}"
exec nginx -g "daemon off;"
