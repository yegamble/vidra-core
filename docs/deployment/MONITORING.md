# Monitoring Setup Guide

This guide explains how to set up monitoring for the Athena Video Platform using Prometheus and Grafana.

## Prerequisites

- Docker and Docker Compose
- Athena codebase

## Quick Start

To run the monitoring stack alongside the main application, it is recommended to use Docker Compose's multiple file support. This ensures all services are in the same network context and configuration overrides are applied correctly.

1. **Start the application with monitoring:**
   ```bash
   # From the root of the repository
   docker compose -f docker-compose.yml -f docs/deployment/monitoring/docker-compose.monitoring.yml up -d
   ```

   This command merges the main configuration with the monitoring configuration. It also ensures that the `app` service is configured to expose metrics (via `ENABLE_ENCODING=true`).

2. **Access the dashboards:**
   - **Prometheus:** http://localhost:9090
   - **Grafana:** http://localhost:3000 (Default login: admin/admin)

## Configuration

### Prometheus

The configuration is in `prometheus.yml`. It is configured to scrape:
- Athena App (metrics endpoint `:9090/metrics`)
- Redis Exporter (for Redis metrics)
- Postgres Exporter (for Database metrics)

### Grafana

1. Login to Grafana (default: admin/admin).
2. Go to **Configuration > Data Sources**.
3. Add **Prometheus** as a data source:
   - URL: `http://prometheus:9090`
   - Click "Save & Test".
4. Import Dashboards:
   - You can find standard Go, Redis, and Postgres dashboards on the Grafana dashboard marketplace.
   - Example ID for Go: `10826`
   - Example ID for Redis: `763`

## Production Notes

- **Persistence:** The `docker-compose.monitoring.yml` uses Docker volumes (`prometheus_data`, `grafana_data`) to persist data.
- **Security:**
    - Change the Grafana admin password immediately.
    - Put Grafana behind a reverse proxy (Nginx) with SSL.
    - Restrict access to port 9090 (Prometheus) if not needed externally.
- **Network:** The monitoring services attach to the `athena-network` defined in the main `docker-compose.yml`.

## Troubleshooting

- **Targets Down:** Check Prometheus targets at `http://localhost:9090/targets`.
- **App Metrics Missing:** The Athena metrics server runs on a separate port (default 9090). Ensure `ENABLE_ENCODING=true` is set (as this currently controls the metrics server start) and `METRICS_ADDR=:9090` is configured. The provided `docker-compose.monitoring.yml` sets these automatically.
