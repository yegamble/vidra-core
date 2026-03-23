# Vidra Core Nginx Reverse Proxy
# Extends official nginx:alpine with openssl and curl for SSL generation and healthchecks

FROM nginx:1.27-alpine

# Install openssl (full OpenSSL, not LibreSSL) and curl
RUN apk add --no-cache openssl curl

# Copy SSL certificate generation scripts
COPY scripts/generate-self-signed-cert.sh /etc/nginx/scripts/
COPY scripts/entrypoint.sh /etc/nginx/scripts/

# Make scripts executable
RUN chmod +x /etc/nginx/scripts/*.sh

# Create SSL directory
RUN mkdir -p /etc/nginx/ssl

# Set custom entrypoint
ENTRYPOINT ["/etc/nginx/scripts/entrypoint.sh"]
