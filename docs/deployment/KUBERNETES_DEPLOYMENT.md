# Kubernetes Deployment Guide

This guide covers deploying Athena to a Kubernetes cluster with full production readiness.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Deployment Steps](#deployment-steps)
- [Monitoring](#monitoring)
- [Scaling](#scaling)
- [Troubleshooting](#troubleshooting)

## Prerequisites

### Required Tools

- `kubectl` >= 1.24
- `helm` >= 3.10 (optional, for dependency charts)
- Access to a Kubernetes cluster (1.24+)
- Container registry access (GitHub Container Registry, Docker Hub, etc.)

### Cluster Requirements

**Minimum Resources:**
- 3 worker nodes (t3.xlarge or equivalent)
- 12 CPU cores total
- 24GB RAM total
- 500GB persistent storage
- LoadBalancer support or Ingress controller

**Recommended for Production:**
- 5+ worker nodes (t3.2xlarge or equivalent)
- 32+ CPU cores total
- 64GB+ RAM total
- 1TB+ persistent storage with high IOPS
- Managed PostgreSQL (RDS, Cloud SQL, etc.)
- Managed Redis (ElastiCache, MemoryStore, etc.)

### External Services

- **PostgreSQL 15+** (managed service recommended)
- **Redis 7+** (managed service recommended for HA)
- **IPFS Node** (can run in-cluster or external)
- **ClamAV** (runs in-cluster via sidecar or separate deployment)
- **S3-compatible storage** (optional, for hot/warm/cold tiers)

## Quick Start

```bash
# 1. Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# 2. Create namespace
kubectl create namespace athena

# 3. Set up secrets
kubectl create secret generic athena-secrets \
  --from-literal=database-url="postgres://user:pass@postgres:5432/athena?sslmode=require" \
  --from-literal=redis-url="redis://redis:6379/0" \
  --from-literal=jwt-secret="$(openssl rand -base64 64)" \
  --from-literal=hls-signing-secret="$(openssl rand -base64 48)" \
  --from-literal=activitypub-key-encryption-key="$(openssl rand -base64 48)" \
  --namespace athena

# 4. Update ingress hostname
sed -i 's/athena.example.com/your-domain.com/g' k8s/base/ingress.yaml

# 5. Deploy
kubectl apply -f k8s/base/ --namespace athena

# 6. Check status
kubectl get pods --namespace athena
kubectl get ingress --namespace athena
```

## Configuration

### 1. Secrets (Required)

Create a `k8s/base/secret.yaml` file based on `secret.yaml.example`:

```bash
cp k8s/base/secret.yaml.example k8s/base/secret.yaml
# Edit with your actual credentials
vim k8s/base/secret.yaml
```

**Critical secrets to change:**
- `database-url`: PostgreSQL connection string
- `redis-url`: Redis connection string
- `jwt-secret`: JWT signing key (minimum 32 characters)
- `activitypub-key-encryption-key`: For encrypting federation keys

**Optional secrets:**
- `hls-signing-secret`: For private video streaming
- `s3-access-key` / `s3-secret-key`: For S3 storage
- `iota-encryption-key`: For IOTA payment wallet encryption

### 2. ConfigMap

Edit `k8s/base/configmap.yaml` for your environment:

```yaml
data:
  # IPFS endpoints
  ipfs-api: "http://ipfs:5001"
  ipfs-cluster-api: "http://ipfs-cluster:9094"

  # ClamAV virus scanner
  clamav-address: "clamav:3310"

  # Feature flags
  enable-activitypub: "true"  # Enable federation
  enable-iota: "false"  # Enable IOTA payments
  enable-s3: "true"  # Enable S3 storage
```

### 3. Storage Classes

Update `k8s/base/pvc.yaml` with your cluster's storage class:

```yaml
spec:
  storageClassName: standard  # Change to: gp3, ssd, premium-rwo, etc.
```

**Storage class options by provider:**
- **AWS EKS**: `gp3`, `gp2`, `io1`, `efs-sc`
- **GKE**: `standard`, `ssd`, `balanced`
- **Azure AKS**: `default`, `managed-premium`
- **DigitalOcean**: `do-block-storage`

### 4. Ingress

Edit `k8s/base/ingress.yaml`:

```yaml
spec:
  rules:
  - host: your-domain.com  # Change this
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: athena-api
            port:
              number: 80
```

**Enable TLS (recommended for production):**

```yaml
metadata:
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
  - hosts:
    - your-domain.com
    secretName: athena-tls-cert
```

## Deployment Steps

### Step 1: Prepare External Services

#### PostgreSQL Setup

```sql
-- Create database and user
CREATE DATABASE athena;
CREATE USER athena WITH PASSWORD 'your-secure-password';
GRANT ALL PRIVILEGES ON DATABASE athena TO athena;

-- Enable required extensions
\c athena
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "unaccent";
CREATE EXTENSION IF NOT EXISTS "btree_gin";
```

#### Redis Setup

- Enable persistence (AOF + RDB)
- Set maxmemory policy: `allkeys-lru`
- Configure eviction: `maxmemory 4gb`

### Step 2: Deploy Supporting Services

> **Note:** The `k8s/ipfs/` and `k8s/clamav/` manifests are currently Work In Progress and not yet available in the repository. Please refer to the Docker Compose setup or deploy these services manually for now.

```bash
# Deploy IPFS (if running in-cluster)
# kubectl apply -f k8s/ipfs/  # TODO: Pending implementation

# Deploy ClamAV
# kubectl apply -f k8s/clamav/  # TODO: Pending implementation

# Deploy PostgreSQL exporter (for monitoring)
helm install postgres-exporter prometheus-community/prometheus-postgres-exporter \
  --set config.datasource="postgresql://athena:password@postgres:5432/athena?sslmode=require" \
  --namespace monitoring

# Deploy Redis exporter
helm install redis-exporter prometheus-community/prometheus-redis-exporter \
  --set redisAddress="redis://redis:6379" \
  --namespace monitoring
```

### Step 3: Deploy Athena

```bash
# Create namespace
kubectl create namespace athena

# Apply secrets
kubectl apply -f k8s/base/secret.yaml --namespace athena

# Apply configuration
kubectl apply -f k8s/base/configmap.yaml --namespace athena

# Deploy storage
kubectl apply -f k8s/base/pvc.yaml --namespace athena

# Deploy application
kubectl apply -f k8s/base/deployment.yaml --namespace athena
kubectl apply -f k8s/base/service.yaml --namespace athena
kubectl apply -f k8s/base/ingress.yaml --namespace athena

# Deploy autoscaling
kubectl apply -f k8s/base/hpa.yaml --namespace athena
```

### Step 4: Run Database Migrations

```bash
# Create migration job
kubectl run athena-migrate \
  --image=ghcr.io/yegamble/athena:latest \
  --restart=Never \
  --namespace=athena \
  --env="DATABASE_URL=$(kubectl get secret athena-secrets -n athena -o jsonpath='{.data.database-url}' | base64 -d)" \
  --command -- /app/athena migrate up

# Check migration logs
kubectl logs athena-migrate --namespace athena

# Clean up job
kubectl delete pod athena-migrate --namespace athena
```

### Step 5: Verify Deployment

```bash
# Check pod status
kubectl get pods --namespace athena
kubectl describe pod athena-api-<pod-id> --namespace athena

# Check logs
kubectl logs -f deployment/athena-api --namespace athena

# Check health endpoints
kubectl run curl --image=curlimages/curl -i --rm --restart=Never -- \
  curl http://athena-api/health

# Check readiness
kubectl run curl --image=curlimages/curl -i --rm --restart=Never -- \
  curl http://athena-api/ready
```

## Monitoring

### Deploy Prometheus Stack

```bash
# Create monitoring namespace
kubectl create namespace monitoring

# Deploy Prometheus
kubectl apply -f k8s/monitoring/prometheus-config.yaml
helm install prometheus prometheus-community/prometheus \
  --namespace monitoring \
  --set server.configMapOverrideName=prometheus-config

# Deploy Grafana
helm install grafana grafana/grafana \
  --namespace monitoring \
  --set persistence.enabled=true \
  --set persistence.size=10Gi \
  --set adminPassword='admin'

# Import Athena dashboard
kubectl create configmap grafana-dashboard-athena \
  --from-file=k8s/monitoring/grafana-dashboard.json \
  --namespace monitoring
```

### Access Monitoring

```bash
# Port-forward Prometheus
kubectl port-forward -n monitoring svc/prometheus-server 9090:80

# Port-forward Grafana
kubectl port-forward -n monitoring svc/grafana 3000:80

# Open in browser
open http://localhost:3000
# Username: admin, Password: admin
```

### Key Metrics to Monitor

- **Request Rate**: `rate(athena_http_requests_total[5m])`
- **Error Rate**: `rate(athena_http_requests_total{status=~"5.."}[5m])`
- **Latency (p99)**: `histogram_quantile(0.99, rate(athena_http_request_duration_seconds_bucket[5m]))`
- **Database Connections**: `athena_database_connections_in_use`
- **Encoding Queue**: `athena_encoding_queue_depth`
- **IPFS Health**: `athena_ipfs_gateway_health`

## Scaling

### Horizontal Pod Autoscaling (HPA)

HPA is automatically configured in `k8s/base/hpa.yaml`:

```yaml
# API servers: 3-20 replicas based on CPU/memory
# Encoding workers: 2-10 replicas based on queue depth
```

**Monitor autoscaling:**

```bash
kubectl get hpa --namespace athena --watch
```

### Vertical Scaling (Resource Limits)

Edit deployment resource requests/limits:

```yaml
resources:
  requests:
    memory: "4Gi"  # Increase for more concurrent requests
    cpu: "2000m"
  limits:
    memory: "8Gi"
    cpu: "4000m"
```

### Database Scaling

- **Read Replicas**: Configure read replicas for analytics queries
- **Connection Pooling**: Use PgBouncer for connection pooling
- **Sharding**: Consider sharding for >1M videos

### Storage Scaling

- **Expand PVC**: Most cloud providers support dynamic PVC expansion
- **S3 Lifecycle**: Move cold videos to Glacier after 90 days
- **IPFS Cluster**: Scale IPFS cluster nodes for redundancy

## Troubleshooting

### Pods Not Starting

```bash
# Check events
kubectl describe pod <pod-name> --namespace athena

# Check logs
kubectl logs <pod-name> --namespace athena --previous

# Common issues:
# - ImagePullBackOff: Check image name and registry access
# - CrashLoopBackOff: Check application logs
# - Pending: Check resource availability and PVC status
```

### Database Connection Issues

```bash
# Test connection from pod
kubectl run psql --image=postgres:15-alpine -i --rm --restart=Never --namespace athena -- \
  psql "$(kubectl get secret athena-secrets -n athena -o jsonpath='{.data.database-url}' | base64 -d)"

# Check network policies
kubectl get networkpolicies --namespace athena
```

### High Latency

1. Check database query performance
2. Verify Redis is responding
3. Check IPFS gateway health
4. Review HPA scaling events
5. Analyze Prometheus metrics

### Out of Memory (OOM)

```bash
# Check memory usage
kubectl top pods --namespace athena

# Increase memory limits in deployment
# Add memory requests to ensure QoS
```

### ClamAV Not Responding

```bash
# Check ClamAV pod status
kubectl logs deployment/clamav --namespace athena

# ClamAV startup can take 2-5 minutes to load virus signatures
# Check fallback mode in configmap (should be "strict" in production)
```

## Production Checklist

- [ ] Database backups configured (daily, 30-day retention)
- [ ] Redis persistence enabled (AOF + RDB)
- [ ] TLS certificates configured (Let's Encrypt)
- [ ] Monitoring and alerting active (PagerDuty/Opsgenie)
- [ ] Log aggregation configured (ELK, Loki, CloudWatch)
- [ ] Resource limits set on all pods
- [ ] Network policies configured (restrict pod-to-pod traffic)
- [ ] Secrets rotated from defaults
- [ ] HPA tested under load
- [ ] Backup and restore procedures documented
- [ ] Incident response runbook created
- [ ] Load testing completed (1000+ concurrent users)
- [ ] Disaster recovery plan documented

## Additional Resources

- [Helm Chart](../helm/README.md) (coming soon)
- [Terraform Modules](../../infrastructure/README.md) (coming soon)
- [Production Runbook](../operations/RUNBOOK.md) (coming soon)
- [Performance Tuning](../operations/PERFORMANCE.md) (coming soon)
