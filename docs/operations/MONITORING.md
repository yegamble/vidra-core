# Monitoring Setup Guide

This guide explains how to set up monitoring for the Vidra Core Video Platform using Prometheus and Grafana.
For operational procedures and incident response, see the [Operations Runbook](RUNBOOK.md).

## Prerequisites

- Docker and Docker Compose
- Running Vidra Core instance (using `docker-compose.yml`)

## Quick Start

1. **Ensure the main application is running:**

   ```bash
   docker compose up -d
   ```

2. **Start the monitoring stack (if available):**

   ```bash
   # Note: monitoring stack configuration is project-specific
   # Check docs/deployment/monitoring/ for docker compose files
   cd docs/deployment/monitoring
   docker compose -f docker-compose.monitoring.yml up -d
   ```

3. **Access the dashboards:**
   - **Prometheus:** <http://localhost:9090>
   - **Grafana:** <http://localhost:3000> (Default login: admin/admin)

## Configuration

### Prometheus

The configuration is in `prometheus.yml`. It is configured to scrape:

- Vidra Core App (metrics endpoint `:9090/metrics`)
- Redis Exporter
- Postgres Exporter

### Grafana

1. Login to Grafana.
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
- **Network:** The monitoring stack expects to attach to the default network. The main application uses the `vidra-network` network (see `docker-compose.yml`). You may need to configure the monitoring stack to connect to this network. Check `docker network ls` to find the correct network name.

## Troubleshooting

- **Targets Down:** Check Prometheus targets at `http://localhost:9090/targets`. If `app` is down, ensure `ENABLE_METRICS=true` and `METRICS_ADDR=:9090` are set in your `.env`.
