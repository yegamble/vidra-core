# Kubernetes Manifests for Athena

This directory contains Kubernetes manifests for deploying Athena video platform to production.

## Directory Structure

```
k8s/
├── base/                      # Base Kubernetes manifests
│   ├── deployment.yaml        # API servers and encoding workers
│   ├── service.yaml           # ClusterIP services
│   ├── configmap.yaml         # Application configuration
│   ├── pvc.yaml               # Persistent volume claims
│   ├── hpa.yaml               # Horizontal Pod Autoscaler
│   ├── ingress.yaml           # Ingress controller configuration
│   └── secret.yaml.example    # Secret template (copy and customize)
├── monitoring/                # Monitoring stack
│   ├── prometheus-config.yaml # Prometheus scrape config and alert rules
│   └── grafana-dashboard.json # Grafana dashboard definition
└── overlays/                  # Kustomize overlays (optional)
    ├── staging/               # Staging environment overrides
    └── production/            # Production environment overrides
```

## Quick Start

See [docs/deployment/KUBERNETES_DEPLOYMENT.md](../docs/deployment/KUBERNETES_DEPLOYMENT.md) for comprehensive deployment guide.

### TL;DR

```bash
# 1. Create secrets
kubectl create secret generic athena-secrets \
  --from-literal=database-url="postgres://..." \
  --from-literal=redis-url="redis://..." \
  --from-literal=jwt-secret="$(openssl rand -base64 64)"

# 2. Update ingress hostname
sed -i 's/athena.example.com/your-domain.com/g' base/ingress.yaml

# 3. Deploy
kubectl apply -f base/

# 4. Check status
kubectl get pods -l app=athena
```

## Components

### 1. **API Servers** (`deployment.yaml`)

- **Replicas**: 3 (autoscales to 20)
- **Resources**: 2Gi RAM, 1 CPU (limits: 4Gi RAM, 2 CPU)
- **Ports**: 8080 (HTTP), 9090 (metrics)
- **Health checks**: `/health` (liveness), `/ready` (readiness)

### 2. **Encoding Workers** (`deployment.yaml`)

- **Replicas**: 2 (autoscales to 10)
- **Resources**: 4Gi RAM, 2 CPU (limits: 8Gi RAM, 4 CPU)
- **Purpose**: Video transcoding (FFmpeg)

### 3. **Autoscaling** (`hpa.yaml`)

- **API HPA**: CPU 70%, Memory 80%
- **Worker HPA**: CPU 75%, Memory 85%
- **Scale down delay**: 5-10 minutes

### 4. **Ingress** (`ingress.yaml`)

- **Controller**: nginx-ingress
- **Max body size**: 5GB (for video uploads)
- **Timeouts**: 3600s (1 hour for large uploads)
- **CORS**: Enabled with configurable origins

### 5. **Storage** (`pvc.yaml`)

- **athena-storage**: 500Gi (videos, thumbnails, HLS segments)
- **athena-quarantine**: 10Gi (quarantined malware files)
- **Access Mode**: ReadWriteMany (shared across pods)

### 6. **Monitoring** (`monitoring/`)

- **Prometheus**: Metrics collection with custom alert rules
- **Grafana**: Pre-configured dashboard for Athena metrics

## Configuration

### Environment Variables (ConfigMap)

Edit `base/configmap.yaml`:

```yaml
data:
  ipfs-api: "http://ipfs:5001"
  clamav-address: "clamav:3310"
  log-level: "info"
  enable-activitypub: "true"
```

### Secrets

**Required secrets:**
- `database-url`: PostgreSQL connection string
- `redis-url`: Redis connection string
- `jwt-secret`: JWT signing key

**Optional secrets:**
- `hls-signing-secret`: For private streaming
- `s3-access-key` / `s3-secret-key`: For S3 storage
- `iota-encryption-key`: For IOTA payments

## Deployment Environments

Athena uses a Blue/Green deployment strategy for zero-downtime updates.

### Blue Environment (Active)

```bash
kubectl apply -k overlays/blue/
```

### Green Environment (Standby/New)

```bash
kubectl apply -k overlays/green/
```

## Resource Requirements

### Minimum (Development/Testing)

- 3 nodes: t3.xlarge (4 vCPU, 16GB RAM each)
- Total: 12 vCPU, 48GB RAM
- Storage: 500GB

### Recommended (Production)

- 5+ nodes: t3.2xlarge (8 vCPU, 32GB RAM each)
- Total: 40+ vCPU, 160GB+ RAM
- Storage: 1TB+ with high IOPS (gp3, SSD)

### Cost Estimate (AWS EKS)

- **Small**: ~$400-600/month (3x t3.xlarge + RDS + ElastiCache)
- **Medium**: ~$800-1200/month (5x t3.2xlarge + RDS Multi-AZ + ElastiCache cluster)
- **Large**: ~$2000-3000/month (10x c6i.4xlarge + RDS + ElastiCache + CloudFront)

## External Dependencies

The following services must be deployed separately:

1. **PostgreSQL 15+**: Managed service recommended (AWS RDS, Google Cloud SQL, etc.)
2. **Redis 7+**: Managed service recommended (AWS ElastiCache, Redis Cloud, etc.)
3. **IPFS Node**: Can run in-cluster or use managed service
4. **ClamAV**: Included in deployment as sidecar

## Monitoring & Observability

### Prometheus Metrics

Athena exposes metrics at `:9090/metrics`:

- `athena_http_requests_total`: HTTP request counter
- `athena_http_request_duration_seconds`: Request latency histogram
- `athena_database_connections_in_use`: Active database connections
- `athena_encoding_queue_depth`: Pending encoding jobs
- `athena_virus_scan_total`: Virus scan results
- `athena_activitypub_delivery_total`: Federation delivery status

### Grafana Dashboards

Import `monitoring/grafana-dashboard.json` into Grafana for:

- HTTP request rate and latency
- Error rates and status codes
- Database and Redis health
- Encoding queue depth
- IPFS gateway health

### Alerts

Prometheus alert rules in `monitoring/prometheus-config.yaml`:

- API server down
- High error rate (>5%)
- High latency (p99 >2s)
- Database connection pool exhaustion
- Redis unavailable
- High encoding queue (>100 jobs)
- ClamAV down (blocks uploads)
- Malware detected

## Troubleshooting

### Pods Stuck in Pending

```bash
# Check PVC status
kubectl get pvc
kubectl describe pvc athena-storage

# Check node resources
kubectl describe nodes
```

### High Memory Usage

```bash
# Check pod memory
kubectl top pods

# Increase limits in deployment.yaml
resources:
  limits:
    memory: "8Gi"  # Increase from 4Gi
```

### ClamAV Not Ready

```bash
# ClamAV takes 2-5 minutes to load virus signatures
kubectl logs deployment/clamav

# Check startup probe configuration
initialDelaySeconds: 120  # Increase if needed
```

### Database Connection Errors

```bash
# Test connection from pod
kubectl run psql --rm -i --image=postgres:15 -- \
  psql "$DATABASE_URL"

# Check network policies
kubectl get networkpolicies
```

## Security Considerations

1. **Network Policies**: Restrict pod-to-pod traffic
2. **Pod Security Standards**: Enforce `restricted` PSS
3. **Secrets Management**: Use External Secrets Operator or Sealed Secrets
4. **Image Scanning**: Scan container images for vulnerabilities (Trivy, Snyk)
5. **RBAC**: Limit service account permissions
6. **Egress Control**: Whitelist external domains (IPFS, federation)

## Maintenance

### Backup

```bash
# Database backups (automated via RDS/Cloud SQL)
# or manual:
pg_dump "$DATABASE_URL" | gzip > athena-backup-$(date +%Y%m%d).sql.gz

# Storage backups (use Velero or cloud-native snapshots)
velero backup create athena-storage --include-namespaces athena
```

### Upgrades

```bash
# Update image tag
kubectl set image deployment/athena-api athena=ghcr.io/yegamble/athena:v1.2.0

# Monitor rollout
kubectl rollout status deployment/athena-api

# Rollback if needed
kubectl rollout undo deployment/athena-api
```

### Scaling

```bash
# Manual scaling
kubectl scale deployment/athena-api --replicas=10

# Check autoscaler status
kubectl get hpa
```

## Blue/Green Deployments

Athena supports **zero-downtime blue/green deployments** for production:

```bash
# Quick start
kubectl label deployment athena-api version=blue -n athena --overwrite
kubectl apply -k overlays/green/
kubectl patch service athena-api -n athena -p '{"spec":{"selector":{"version":"green"}}}'
```

**Learn more:**
- [Blue/Green Strategy](../docs/deployment/BLUE_GREEN_DEPLOYMENT_STRATEGY.md) - Architecture and design
- [Implementation Guide](../docs/deployment/BLUE_GREEN_IMPLEMENTATION_GUIDE.md) - Step-by-step setup
- [Quick Start](../docs/deployment/BLUE_GREEN_QUICK_START.md) - Get running in 30 minutes
- [GitHub Actions Workflow](../.github/workflows/blue-green-deploy.yml) - Automated deployments
- [Rollback Script](../scripts/rollback-deployment.sh) - Emergency rollback

**Key Features:**
- Zero downtime (< 1 second switchover)
- Instant rollback (< 30 seconds)
- Gradual traffic shifting (canary deployments)
- Automated health checks and validation
- Federation-aware (ActivityPub/BlueSky)
- Cost-optimized (< 0.1% monthly overhead)

## Additional Resources

- [Full Deployment Guide](../docs/deployment/KUBERNETES_DEPLOYMENT.md)
- [Blue/Green Deployments](../docs/deployment/BLUE_GREEN_DEPLOYMENT_STRATEGY.md) ⭐ NEW
- [Operations Runbook](../docs/operations/RUNBOOK.md)
- [Production Guide](../docs/deployment/PRODUCTION.md)
- [Monitoring Guide](../docs/operations/MONITORING.md)
- [Performance Tuning](../docs/operations/PERFORMANCE.md)

## Support

For issues or questions:
- GitHub Issues: https://github.com/yegamble/athena/issues
- Documentation: https://github.com/yegamble/athena/docs
