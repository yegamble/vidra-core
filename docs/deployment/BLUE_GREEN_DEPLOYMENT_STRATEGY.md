# Blue/Green Deployment Strategy for Vidra Core

**Version:** 1.0
**Status:** Strategic Planning
**Last Updated:** 2025-11-17

## Executive Summary

This document outlines a comprehensive blue/green deployment strategy for the Vidra Core decentralized video platform, specifically designed to address the unique challenges of:

- **Zero-downtime deployments** for a federated video platform
- **Stateful component management** (PostgreSQL, Redis, IPFS content)
- **Long-running video encoding jobs** that must complete without interruption
- **Federation requirements** (ActivityPub/BlueSky must see continuous uptime)
- **Cost optimization** (minimize duplicate resource usage during switchover)

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Component Classification](#component-classification)
3. [Traffic Management Strategy](#traffic-management-strategy)
4. [Database Migration Handling](#database-migration-handling)
5. [Deployment Workflow](#deployment-workflow)
6. [Rollback Procedures](#rollback-procedures)
7. [Monitoring & Validation](#monitoring--validation)
8. [Cost Optimization](#cost-optimization)
9. [GitHub Actions Integration](#github-actions-integration)
10. [Federation-Specific Considerations](#federation-specific-considerations)

---

## Architecture Overview

### Blue/Green Model for Vidra Core

```
┌─────────────────────────────────────────────────────────────────┐
│                         Ingress / Load Balancer                  │
│                    (Traffic Switching Point)                     │
└────────────────┬────────────────────────────────────────────────┘
                 │
      ┌──────────┴──────────┐
      │                     │
      ▼                     ▼
┌─────────────┐      ┌─────────────┐
│   BLUE      │      │   GREEN     │
│ Environment │      │ Environment │
├─────────────┤      ├─────────────┤
│ API Servers │      │ API Servers │
│  (v1.2.0)   │      │  (v1.3.0)   │
│             │      │             │
│ Encoders    │      │ Encoders    │
│  (active)   │      │  (standby)  │
└─────────────┘      └─────────────┘
      │                     │
      └──────────┬──────────┘
                 │
                 ▼
┌─────────────────────────────────────┐
│        SHARED STATEFUL LAYER        │
├─────────────────────────────────────┤
│ PostgreSQL (single instance)        │
│ Redis (single cluster)              │
│ IPFS (shared cluster)               │
│ MinIO/S3 (shared storage)           │
│ ClamAV (shared service)             │
└─────────────────────────────────────┘
```

### Key Principles

1. **Shared State**: Database, Redis, and storage are NOT duplicated
2. **Dual Application Stacks**: Blue and Green API/worker deployments coexist temporarily
3. **Atomic Traffic Switch**: Ingress switches traffic instantly via label selectors
4. **Gradual Rollout Support**: Traffic can be split (e.g., 90/10) for canary testing
5. **Independent Encoding Workers**: Long-running jobs continue on Blue while Green accepts new jobs

---

## Component Classification

### Stateless Components (Duplicated for Blue/Green)

**API Servers:**

- Deployment: `vidra-api-blue` and `vidra-api-green`
- Fully independent
- Can run simultaneously without conflict
- Tagged with version labels

**Encoding Workers:**

- Deployment: `vidra-encoding-worker-blue` and `vidra-encoding-worker-green`
- Special handling: Blue workers complete existing jobs, Green starts new jobs
- Coordinated via Redis job queue with version tagging

### Stateful Components (Shared)

**PostgreSQL:**

- Single instance (managed RDS/Cloud SQL recommended)
- Schema migrations applied before traffic switch
- Backward-compatible migrations required
- Connection pooling via PgBouncer (if needed)

**Redis:**

- Single cluster (ElastiCache/MemoryStore recommended)
- Session data shared between Blue/Green
- Queue job versioning to route to correct workers

**IPFS Cluster:**

- Shared cluster (content-addressed, immutable)
- No duplication needed
- Metadata updates handled via database

**Object Storage (MinIO/S3):**

- Shared bucket
- No duplication needed

**ClamAV:**

- Shared service
- Stateless but expensive to duplicate (virus definitions)

---

## Traffic Management Strategy

### Option 1: Kubernetes Service Label Selector (Recommended)

**Mechanism:** Update Service selector to switch between Blue and Green

**Advantages:**

- Native Kubernetes feature
- Instant switch (no external dependencies)
- Works with existing HPA
- No additional cost

**Implementation:**

```yaml
# Blue Service (active)
apiVersion: v1
kind: Service
metadata:
  name: vidra-api
  labels:
    app: vidra
    component: api
spec:
  selector:
    app: vidra
    component: api
    version: blue      # <-- Switch this label
  ports:
  - port: 80
    targetPort: 8080
```

**Switch command:**

```bash
kubectl patch service vidra-api -p '{"spec":{"selector":{"version":"green"}}}'
```

### Option 2: Ingress Annotation Switching

**Mechanism:** Use NGINX Ingress canary annotations for gradual rollout

**Advantages:**

- Gradual traffic shifting (10%, 50%, 100%)
- A/B testing capabilities
- Automatic weight-based routing

**Implementation:**

```yaml
# Green Ingress (canary)
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: vidra-ingress-green
  annotations:
    nginx.ingress.kubernetes.io/canary: "true"
    nginx.ingress.kubernetes.io/canary-weight: "10"  # 10% traffic to green
spec:
  rules:
  - host: vidra.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: vidra-api-green
            port:
              number: 80
```

**Gradual rollout:**

```bash
# 10% traffic to green
kubectl patch ingress vidra-ingress-green --type=merge -p '{"metadata":{"annotations":{"nginx.ingress.kubernetes.io/canary-weight":"10"}}}'

# 50% traffic to green
kubectl patch ingress vidra-ingress-green --type=merge -p '{"metadata":{"annotations":{"nginx.ingress.kubernetes.io/canary-weight":"50"}}}'

# 100% traffic to green (switch complete)
kubectl patch service vidra-api -p '{"spec":{"selector":{"version":"green"}}}'
kubectl delete ingress vidra-ingress-green
```

### Option 3: External Load Balancer (Advanced)

**Mechanism:** Use external LB (AWS ALB, GCP LB, Cloudflare) for traffic splitting

**Advantages:**

- Independent of Kubernetes
- Advanced routing rules (geography, headers, etc.)
- DDoS protection
- CDN integration

**Disadvantages:**

- Additional cost
- More complex setup
- External dependency

**Recommendation:** Use Option 1 for initial implementation, Option 2 for gradual rollouts.

---

## Database Migration Handling

### Challenge: Zero-Downtime Schema Changes

Video platforms require continuous uptime. Database migrations must be:

1. **Backward-compatible** with the Blue version
2. **Forward-compatible** with the Green version
3. **Non-blocking** (no table locks)

### Strategy: Expand-Contract Pattern

#### Phase 1: Expand (Add New Schema)

- Add new columns/tables (nullable or with defaults)
- Blue version ignores new schema
- Green version uses new schema

#### Phase 2: Migration Window

- Both Blue and Green write to old AND new schema
- Backfill data if needed
- Validate data consistency

#### Phase 3: Contract (Remove Old Schema)

- After traffic fully on Green
- Remove old columns/tables in next deployment
- Clean up migration code

### Example: Renaming a Column

**Migration 1 (Expand):**

```sql
-- Up
ALTER TABLE videos ADD COLUMN new_status TEXT;
UPDATE videos SET new_status = old_status WHERE new_status IS NULL;
CREATE INDEX idx_videos_new_status ON videos(new_status);

-- Down (rollback)
ALTER TABLE videos DROP COLUMN new_status;
```

**Application Code (Green version):**

```go
// Write to BOTH columns during migration
func UpdateVideoStatus(videoID, status string) error {
    _, err := db.Exec(`
        UPDATE videos
        SET old_status = $1, new_status = $1
        WHERE id = $2
    `, status, videoID)
    return err
}
```

**Migration 2 (Contract - next release):**

```sql
-- Up
ALTER TABLE videos DROP COLUMN old_status;

-- Down
ALTER TABLE videos ADD COLUMN old_status TEXT;
UPDATE videos SET old_status = new_status WHERE old_status IS NULL;
```

### Migration Workflow

```bash
# 1. Apply expand migration (before deploying Green)
kubectl run vidra-migrate \
  --image=ghcr.io/yegamble/vidra-core:v1.3.0 \
  --restart=Never \
  --namespace=vidra \
  --env="DATABASE_URL=$(kubectl get secret vidra-secrets -n vidra -o jsonpath='{.data.database-url}' | base64 -d)" \
  --command -- /app/vidra migrate up

# 2. Wait for migration to complete
kubectl wait --for=condition=complete --timeout=600s job/vidra-migrate -n vidra

# 3. Verify migration
kubectl logs vidra-migrate -n vidra

# 4. Deploy Green (uses new schema)
kubectl apply -f k8s/overlays/green/

# 5. Switch traffic to Green

# 6. In next release, apply contract migration
```

### Migration Validation

**Pre-flight checks:**

```sql
-- Check for blocking locks
SELECT pid, usename, query, state, wait_event_type, wait_event
FROM pg_stat_activity
WHERE state != 'idle' AND wait_event_type IS NOT NULL;

-- Check migration file
goose -dir migrations validate

-- Dry-run on test database
goose -dir migrations postgres "$TEST_DATABASE_URL" up
```

**Post-migration validation:**

```bash
# Health check includes database connectivity
curl http://vidra-api-green/ready | jq '.checks.database'

# Run smoke tests
kubectl run smoke-test --image=curlimages/curl -i --rm --restart=Never -- \
  sh -c "curl -f http://vidra-api-green/api/v1/videos | grep -q 'videos'"
```

---

## Deployment Workflow

### Pre-Deployment Phase

1. **Build and Push Docker Image**

   ```bash
   docker build -t ghcr.io/yegamble/vidra-core:v1.3.0 .
   docker push ghcr.io/yegamble/vidra-core:v1.3.0
   ```

2. **Run Pre-Flight Tests**
   - Unit tests
   - Integration tests
   - E2E tests (against staging)
   - Load tests (k6)
   - Security scans (Trivy)

3. **Validate Migration**
   - Test migration on staging database
   - Verify backward compatibility
   - Check for blocking operations

### Deployment Phase

#### Step 1: Apply Database Migrations

```bash
# Apply expand migration
kubectl apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: vidra-migrate-$(date +%s)
  namespace: vidra
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: migrate
        image: ghcr.io/yegamble/vidra-core:v1.3.0
        command: ["/app/vidra"]
        args: ["migrate", "up"]
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: vidra-secrets
              key: database-url
EOF
```

#### Step 2: Deploy Green Environment

```bash
# Deploy Green API servers
kubectl apply -f k8s/overlays/green/deployment-api.yaml

# Wait for pods to be ready
kubectl wait --for=condition=ready pod -l app=vidra,component=api,version=green --timeout=300s -n vidra

# Check health
kubectl run curl --image=curlimages/curl -i --rm --restart=Never -n vidra -- \
  curl -f http://vidra-api-green/health
```

#### Step 3: Run Smoke Tests

```bash
# Automated smoke test suite
kubectl apply -f k8s/jobs/smoke-tests.yaml

# Monitor smoke test results
kubectl logs -f job/smoke-tests -n vidra
```

#### Step 4: Gradual Traffic Shift (Canary)

```bash
# Start with 10% traffic
kubectl apply -f k8s/overlays/green/ingress-canary-10.yaml

# Monitor for 10 minutes
# Check error rates, latency, etc.

# Increase to 50%
kubectl patch ingress vidra-ingress-green -n vidra --type=merge \
  -p '{"metadata":{"annotations":{"nginx.ingress.kubernetes.io/canary-weight":"50"}}}'

# Monitor for 10 minutes

# Full switch to Green
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"green"}}}'
kubectl delete ingress vidra-ingress-green -n vidra
```

#### Step 5: Encoding Worker Migration

```bash
# Pause new job assignment to Blue workers
redis-cli HSET worker:config:blue accepting_jobs false

# Wait for Blue workers to complete existing jobs (monitor queue)
watch 'redis-cli LLEN encoding:queue:blue'

# Deploy Green workers
kubectl apply -f k8s/overlays/green/deployment-workers.yaml

# Enable job assignment to Green workers
redis-cli HSET worker:config:green accepting_jobs true
```

#### Step 6: Scale Down Blue Environment

```bash
# Wait for 30 minutes (ensure stability)
sleep 1800

# Scale down Blue API servers
kubectl scale deployment vidra-api-blue --replicas=1 -n vidra

# Wait 1 hour (keep minimal Blue capacity for emergency rollback)
sleep 3600

# Fully scale down Blue
kubectl scale deployment vidra-api-blue --replicas=0 -n vidra
kubectl scale deployment vidra-encoding-worker-blue --replicas=0 -n vidra

# Update labels
kubectl label deployment vidra-api-blue status=inactive -n vidra --overwrite
```

### Post-Deployment Phase

1. **Monitor for Issues**
   - Check error rates in Prometheus
   - Review logs for warnings
   - Monitor federation delivery success
   - Check video upload/encoding pipeline

2. **Update DNS/CDN (if applicable)**
   - Update CDN origin to point to Green
   - Clear CDN cache for API endpoints

3. **Document Deployment**
   - Record deployment time
   - Document any issues encountered
   - Update runbook if needed

4. **Schedule Contract Migration**
   - Plan contract migration for next release
   - Update migration tracking document

---

## Rollback Procedures

### Instant Rollback (First 30 Minutes)

If issues detected immediately after traffic switch:

```bash
# 1. Revert traffic to Blue
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"blue"}}}'

# 2. Verify Blue health
curl http://vidra.example.com/health

# 3. Scale up Blue if needed
kubectl scale deployment vidra-api-blue --replicas=3 -n vidra

# 4. Investigate Green issues
kubectl logs -l app=vidra,version=green -n vidra --tail=500
```

**Expected Downtime:** < 5 seconds (time for service selector propagation)

### Database Rollback (Migration Issues)

If database migration causes issues:

```bash
# 1. Revert traffic to Blue immediately
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"blue"}}}'

# 2. Run migration rollback
kubectl run vidra-migrate-rollback \
  --image=ghcr.io/yegamble/vidra-core:v1.3.0 \
  --restart=Never \
  --namespace=vidra \
  --env="DATABASE_URL=..." \
  --command -- /app/vidra migrate down

# 3. Verify database state
kubectl run psql --image=postgres:15-alpine -i --rm --restart=Never -n vidra -- \
  psql "$DATABASE_URL" -c "\dt"

# 4. Clean up Green deployment
kubectl delete -f k8s/overlays/green/
```

**Expected Downtime:** < 30 seconds (service switch + verification)

### Delayed Rollback (After 1+ Hours)

If issues discovered after Blue scaled down:

```bash
# 1. Scale up Blue immediately
kubectl scale deployment vidra-api-blue --replicas=3 -n vidra
kubectl wait --for=condition=ready pod -l version=blue --timeout=120s -n vidra

# 2. Switch traffic back to Blue
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"blue"}}}'

# 3. Verify Blue serving traffic
curl http://vidra.example.com/health

# 4. Investigate Green issues
kubectl describe pod -l version=green -n vidra
```

**Expected Downtime:** 1-2 minutes (time to scale up Blue pods)

### Rollback Decision Matrix

| Time Since Deployment | Blue Status | Rollback Time | Procedure |
|-----------------------|-------------|---------------|-----------|
| < 30 minutes | Active (scaled) | < 5 seconds | Instant service switch |
| 30 min - 1 hour | Scaled to 1 | < 30 seconds | Switch + scale Blue |
| 1-2 hours | Scaled to 0 | 1-2 minutes | Deploy Blue + switch |
| > 2 hours | Deleted | 5-10 minutes | Redeploy Blue from scratch |

**Recommendation:** Keep Blue at minimal capacity (1 replica) for 2 hours after traffic switch.

---

## Monitoring & Validation

### Pre-Switch Health Checks

**Automated checks before traffic switch:**

```yaml
# k8s/jobs/pre-switch-validation.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: pre-switch-validation
  namespace: vidra
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: validation
        image: curlimages/curl:latest
        command:
        - sh
        - -c
        - |
          set -e

          # Health check
          curl -f http://vidra-api-green/health || exit 1

          # Readiness check
          READY=$(curl -s http://vidra-api-green/ready | jq -r '.status')
          if [ "$READY" != "ok" ]; then
            echo "Readiness check failed"
            exit 1
          fi

          # Database connectivity
          DB_STATUS=$(curl -s http://vidra-api-green/ready | jq -r '.checks.database')
          if [ "$DB_STATUS" != "ok" ]; then
            echo "Database check failed"
            exit 1
          fi

          # Redis connectivity
          REDIS_STATUS=$(curl -s http://vidra-api-green/ready | jq -r '.checks.redis')
          if [ "$REDIS_STATUS" != "ok" ]; then
            echo "Redis check failed"
            exit 1
          fi

          # IPFS connectivity
          IPFS_STATUS=$(curl -s http://vidra-api-green/ready | jq -r '.checks.ipfs')
          if [ "$IPFS_STATUS" != "ok" ]; then
            echo "IPFS check failed"
            exit 1
          fi

          # Queue depth check
          QUEUE_STATUS=$(curl -s http://vidra-api-green/ready | jq -r '.checks.queue')
          if [ "$QUEUE_STATUS" != "ok" ]; then
            echo "Queue check failed"
            exit 1
          fi

          echo "All pre-switch validations passed"
```

### Post-Switch Monitoring

**Key metrics to monitor after traffic switch:**

1. **Error Rate**

   ```promql
   rate(vidra_http_requests_total{status=~"5.."}[5m]) > 0.01
   ```

   **Alert threshold:** > 1% error rate

2. **Response Latency**

   ```promql
   histogram_quantile(0.99, rate(vidra_http_request_duration_seconds_bucket[5m])) > 2.0
   ```

   **Alert threshold:** p99 > 2 seconds

3. **Federation Delivery Success**

   ```promql
   rate(vidra_activitypub_delivery_total{status="failed"}[5m]) / rate(vidra_activitypub_delivery_total[5m]) > 0.05
   ```

   **Alert threshold:** > 5% failure rate

4. **Database Connection Pool**

   ```promql
   vidra_database_connections_in_use / vidra_database_connections_max > 0.9
   ```

   **Alert threshold:** > 90% utilization

5. **Encoding Queue Depth**

   ```promql
   vidra_encoding_queue_depth > 1000
   ```

   **Alert threshold:** > 1000 jobs

### Automated Rollback Triggers

**Prometheus alerting rules:**

```yaml
# k8s/monitoring/prometheus-alerts.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-alerts
  namespace: monitoring
data:
  alerts.yml: |
    groups:
    - name: deployment
      interval: 30s
      rules:
      - alert: HighErrorRateAfterDeployment
        expr: |
          rate(vidra_http_requests_total{status=~"5.."}[5m]) > 0.05
          and
          time() - kube_deployment_status_observed_generation{deployment="vidra-api-green"} < 1800
        for: 2m
        labels:
          severity: critical
          action: rollback
        annotations:
          summary: "High error rate detected after Green deployment"
          description: "Error rate is {{ $value | humanizePercentage }} in the last 5 minutes"

      - alert: HighLatencyAfterDeployment
        expr: |
          histogram_quantile(0.99, rate(vidra_http_request_duration_seconds_bucket[5m])) > 3.0
          and
          time() - kube_deployment_status_observed_generation{deployment="vidra-api-green"} < 1800
        for: 5m
        labels:
          severity: warning
          action: investigate
        annotations:
          summary: "High latency detected after Green deployment"
          description: "p99 latency is {{ $value }}s"

      - alert: DatabaseConnectionPoolExhaustion
        expr: vidra_database_connections_in_use / vidra_database_connections_max > 0.95
        for: 2m
        labels:
          severity: critical
          action: rollback
        annotations:
          summary: "Database connection pool near exhaustion"
          description: "{{ $value | humanizePercentage }} of connections in use"
```

### Manual Validation Checklist

After traffic switch, validate:

- [ ] Homepage loads (`curl https://vidra.example.com`)
- [ ] User login works
- [ ] Video upload works
- [ ] Video playback works (HLS streaming)
- [ ] Federation delivery works (check ActivityPub outbox)
- [ ] Search works
- [ ] Comments/ratings work
- [ ] Notifications work
- [ ] API authentication works
- [ ] Metrics endpoint accessible

---

## Cost Optimization

### Challenge: Running Two Environments Simultaneously

Blue/green deployments temporarily double compute costs. For a video platform with encoding workers, this can be significant.

### Optimization Strategies

#### 1. Minimize Overlap Time

**Target:** Complete switch in < 30 minutes

- Automate health checks
- Pre-warm Green environment
- Use fast rollout strategy (canary 10% → 100%)

**Cost Savings:** If deployment takes 30 minutes vs 2 hours:

- **Savings:** 75% reduction in dual-environment runtime
- **Example:** $500/month infrastructure = $5/deployment saved

#### 2. Scale Blue Conservatively

**Strategy:** Scale Blue to minimum immediately after traffic switch

```bash
# After traffic switch, scale Blue to 1 replica
kubectl scale deployment vidra-api-blue --replicas=1 -n vidra

# After 1 hour, scale to 0
kubectl scale deployment vidra-api-blue --replicas=0 -n vidra
```

**Cost Savings:**

- API servers: ~60% reduction (3 replicas → 1 → 0)
- Encoding workers: ~90% reduction (pause immediately)

#### 3. Use Spot/Preemptible Instances for Green

**Strategy:** Deploy Green on cheaper spot instances initially

```yaml
# k8s/overlays/green/deployment-api.yaml
spec:
  template:
    spec:
      nodeSelector:
        node.kubernetes.io/instance-type: spot
      tolerations:
      - key: spot
        operator: Equal
        value: "true"
        effect: NoSchedule
```

**Cost Savings:** 60-80% reduction for Green environment during initial deployment

**Note:** After traffic switch and Blue scales down, migrate Green to on-demand instances.

#### 4. Encoding Worker Optimization

**Challenge:** Encoding workers are expensive (CPU/memory intensive)

**Strategy:** Graceful job completion on Blue

```yaml
# k8s/overlays/blue/deployment-workers.yaml
spec:
  template:
    spec:
      terminationGracePeriodSeconds: 7200  # 2 hours
      containers:
      - name: encoding-worker
        lifecycle:
          preStop:
            exec:
              command:
              - /bin/sh
              - -c
              - |
                # Stop accepting new jobs
                redis-cli HSET worker:$(hostname):status accepting_jobs false
                # Wait for current job to complete (max 2 hours)
                while redis-cli HGET worker:$(hostname):current_job | grep -q 'job_'; do
                  echo "Waiting for job to complete..."
                  sleep 30
                done
```

**Cost Savings:**

- Avoid interrupting in-progress encoding jobs
- No need to re-encode videos (saves compute + time)
- Can scale down Blue workers immediately after last job completes

#### 5. Shared Stateful Components

**Already optimized:**

- PostgreSQL: Single instance (not duplicated)
- Redis: Single cluster (not duplicated)
- IPFS: Shared cluster (not duplicated)
- MinIO/S3: Shared storage (not duplicated)
- ClamAV: Shared service (not duplicated)

**Cost Impact:** ~70% of infrastructure is shared, only API/workers duplicated

### Cost Comparison

**Traditional Blue/Green (2 hours overlap):**

```
Blue: 3 API + 2 Workers = 100% cost for 2 hours
Green: 3 API + 2 Workers = 100% cost for 2 hours
Total: 200% cost for 2 hours = 0.33% monthly increase
```

**Optimized Blue/Green (30 min overlap):**

```
Blue: 3 API + 2 Workers = 100% cost for 30 min
Green: 3 API + 2 Workers = 100% cost for 30 min
Total: 200% cost for 30 min = 0.08% monthly increase
```

**Net Cost Impact:** < 0.1% monthly infrastructure increase for zero-downtime deployments

---

## GitHub Actions Integration

### Workflow Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     GitHub Actions Workflow                      │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ 1. BUILD & TEST                                                  │
│    - Build Docker image                                          │
│    - Run unit tests                                              │
│    - Run integration tests                                       │
│    - Run E2E tests                                               │
│    - Security scan (Trivy)                                       │
│    - Push to ghcr.io                                             │
└─────────────────┬───────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────┐
│ 2. STAGING DEPLOYMENT                                            │
│    - Deploy to staging environment                               │
│    - Run smoke tests                                             │
│    - Run load tests (k6)                                         │
│    - Await manual approval                                       │
└─────────────────┬───────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────┐
│ 3. PRODUCTION BLUE/GREEN                                         │
│    - Validate current state                                      │
│    - Apply database migrations                                   │
│    - Deploy Green environment                                    │
│    - Run pre-switch validation                                   │
│    - Gradual traffic shift (10% → 50% → 100%)                   │
│    - Monitor metrics (auto-rollback on failure)                  │
│    - Scale down Blue                                             │
│    - Send success notification                                   │
└─────────────────────────────────────────────────────────────────┘
```

### Workflow Implementation

See `k8s/workflows/blue-green-deploy.yaml` for full workflow (created below).

### Secrets Configuration

**Required GitHub Secrets:**

```yaml
KUBE_CONFIG: Base64-encoded kubeconfig file
DOCKER_REGISTRY_TOKEN: GitHub token for ghcr.io
DATABASE_URL: Production database URL (for migrations)
SLACK_WEBHOOK_URL: Slack webhook for notifications (optional)
PAGERDUTY_API_KEY: PagerDuty API key for critical alerts (optional)
```

**Setup:**

```bash
# Encode kubeconfig
cat ~/.kube/config | base64 | pbcopy

# Add to GitHub secrets
gh secret set KUBE_CONFIG --body "$(cat ~/.kube/config | base64)"
```

### Manual Approval Gate

**Require manual approval before production deployment:**

```yaml
jobs:
  deploy-production:
    runs-on: ubuntu-latest
    needs: [deploy-staging]
    environment:
      name: production
      url: https://vidra.example.com
    steps:
      # Manual approval required via GitHub UI
```

**GitHub UI:** Navigate to Actions → Workflow Run → Review Deployments

---

## Federation-Specific Considerations

### ActivityPub Continuous Uptime

**Challenge:** External Mastodon/PeerTube instances expect Vidra Core to be always reachable.

**Solutions:**

1. **HTTP Signature Validity:**
   - Ensure RSA keys remain accessible during switchover
   - Store keys in database (already implemented)
   - No key rotation during deployment

2. **Inbox Delivery:**
   - Blue and Green can both receive ActivityPub messages
   - Messages processed idempotently (duplicate detection via `id` field)
   - No message loss during switchover

3. **Outbox Delivery:**
   - Queue-based delivery (Redis)
   - Both Blue and Green can process delivery queue
   - Use distributed lock to prevent duplicate deliveries

4. **Webfinger Lookups:**
   - Must work during entire switchover
   - Test: `curl https://vidra.example.com/.well-known/webfinger?resource=acct:user@vidra.example.com`

### BlueSky/ATProto Considerations

**Challenge:** ATProto uses Personal Data Servers (PDS) that must remain reachable.

**Solutions:**

1. **DID Resolution:**
   - DID documents must remain accessible
   - Test: `curl https://vidra.example.com/.well-known/did.json`

2. **Lexicon API Compatibility:**
   - Ensure new Green version maintains backward compatibility
   - Test ATProto endpoints in smoke tests

3. **Session Continuity:**
   - ATProto sessions stored in Redis (shared)
   - No session interruption during switchover

### Federation Health Check

**Add federation-specific health checks to pre-switch validation:**

```yaml
- name: Test Federation Endpoints
  run: |
    # ActivityPub actor
    curl -f -H "Accept: application/activity+json" \
      http://vidra-api-green/users/testuser

    # Webfinger
    curl -f http://vidra-api-green/.well-known/webfinger?resource=acct:testuser@vidra.example.com

    # ATProto DID
    curl -f http://vidra-api-green/.well-known/did.json

    # NodeInfo
    curl -f http://vidra-api-green/.well-known/nodeinfo
```

---

## Implementation Checklist

### Phase 1: Infrastructure Preparation (Week 1)

- [ ] Create Blue/Green deployment manifests
- [ ] Configure service label selectors
- [ ] Set up canary ingress resources
- [ ] Create smoke test job
- [ ] Create pre-switch validation job
- [ ] Configure Prometheus alerting rules
- [ ] Document rollback procedures

### Phase 2: CI/CD Integration (Week 2)

- [ ] Create GitHub Actions workflow
- [ ] Configure GitHub secrets
- [ ] Set up manual approval gates
- [ ] Implement automated rollback logic
- [ ] Add Slack/PagerDuty notifications
- [ ] Test workflow in staging environment

### Phase 3: Database Migration Strategy (Week 3)

- [ ] Document expand-contract pattern
- [ ] Create migration validation scripts
- [ ] Test backward-compatible migrations
- [ ] Set up migration rollback procedures
- [ ] Create migration runbook

### Phase 4: Testing & Validation (Week 4)

- [ ] Run blue/green deployment in staging
- [ ] Test gradual rollout (canary)
- [ ] Test instant rollback
- [ ] Test database migration rollback
- [ ] Load test during switchover
- [ ] Validate federation during switchover
- [ ] Document lessons learned

### Phase 5: Production Rollout (Week 5)

- [ ] Schedule maintenance window (optional, for first deployment)
- [ ] Run production blue/green deployment
- [ ] Monitor for 24 hours
- [ ] Gather metrics (downtime, switchover time, etc.)
- [ ] Refine procedures based on feedback
- [ ] Remove maintenance window requirement for future deployments

---

## Success Metrics

### Deployment KPIs

- **Downtime:** < 1 second (target: 0 seconds)
- **Switchover Time:** < 30 minutes (build to traffic switch)
- **Rollback Time:** < 30 seconds
- **Error Rate During Switchover:** < 0.1%
- **Federation Delivery Success:** > 99.5%
- **Cost Overhead:** < 0.1% monthly
- **Deployment Frequency:** Daily (if desired)

### Monitoring Dashboard

Create a Grafana dashboard with:

1. **Deployment Timeline** (annotations)
2. **Traffic Split** (Blue vs Green)
3. **Error Rates** (by version)
4. **Latency** (by version)
5. **Database Connections** (by version)
6. **Encoding Queue Depth**
7. **Federation Delivery Success**

---

## Conclusion

This blue/green deployment strategy enables Vidra Core to achieve:

1. **True zero-downtime deployments** for a federated video platform
2. **Instant rollback** capability with minimal risk
3. **Gradual traffic shifting** for confident production releases
4. **Cost-optimized** dual-environment operation
5. **Federation-aware** switchover process

By leveraging Kubernetes native features (service label selectors, HPA, ingress annotations) and implementing careful database migration patterns, Vidra Core can deploy multiple times per day without user-visible downtime or federation disruption.

**Next Steps:**

1. Review this strategy with the engineering team
2. Create Kubernetes manifests (see next file)
3. Implement GitHub Actions workflow
4. Test in staging environment
5. Execute first production blue/green deployment

---

**Document History:**

- 2025-11-17: Initial strategic plan created
