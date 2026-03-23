#!/bin/sh
# Nginx entrypoint script for Vidra Core
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

printf "${GREEN}Vidra Core Nginx Entrypoint${NC}\n"
echo "Protocol: $NGINX_PROTOCOL"
echo "Domain: $NGINX_DOMAIN"

# Certificate generation logic
if [ "$NGINX_PROTOCOL" = "https" ]; then
    printf "${GREEN}HTTPS mode enabled${NC}\n"

    CERT_FILE="$SSL_DIR/self-signed.crt"
    KEY_FILE="$SSL_DIR/self-signed.key"

    # Check if certificates exist
    if [ "$NGINX_TLS_MODE" = "self-signed" ]; then
        if [ ! -f "$CERT_FILE" ] || [ ! -f "$KEY_FILE" ]; then
            printf "${GREEN}Generating self-signed certificates...${NC}\n"

            # Run certificate generation script
            if /etc/nginx/scripts/generate-self-signed-cert.sh "$NGINX_DOMAIN"; then
                printf "${GREEN}Self-signed certificates generated successfully${NC}\n"
            else
                printf "${RED}WARNING: Certificate generation failed${NC}\n"
                printf "${YELLOW}Falling back to HTTP mode${NC}\n"
                export NGINX_PROTOCOL="http"
            fi
        else
            printf "${YELLOW}SSL certificates already exist${NC}\n"
        fi
    elif [ "$NGINX_TLS_MODE" = "letsencrypt" ]; then
        # Let's Encrypt certificates are managed by certbot container
        # Check if they exist
        LE_CERT="/etc/letsencrypt/live/$NGINX_DOMAIN/fullchain.pem"
        if [ ! -f "$LE_CERT" ]; then
            printf "${YELLOW}Let's Encrypt certificates not yet available${NC}\n"
            echo "Waiting for certbot to acquire certificates..."
            echo "If this is first run, ensure certbot container is running and domain points to this server"
            # Note: Nginx will start anyway, serving HTTP-only config until certs are available
        fi
    fi
else
    printf "${YELLOW}HTTP mode - no certificates needed${NC}\n"
fi

# Start nginx (replace current process)
printf "${GREEN}Starting Nginx...${NC}\n"
exec nginx -g "daemon off;"
