# Blue/Green Deployment Implementation Guide

**Version:** 1.0
**Status:** Implementation Ready
**Last Updated:** 2025-11-17

## Quick Links

- [Strategic Overview](./BLUE_GREEN_DEPLOYMENT_STRATEGY.md)
- [Kubernetes Deployment Guide](./KUBERNETES_DEPLOYMENT.md)
- [Operations Runbook](./OPERATIONS_RUNBOOK.md)

## Overview

This guide provides step-by-step instructions for implementing blue/green deployments for the Vidra Core video platform.

## Prerequisites

Before implementing blue/green deployments:

- [ ] Kubernetes cluster running (1.24+)
- [ ] kubectl configured with cluster admin access
- [ ] Vidra Core currently deployed (single environment)
- [ ] Database migrations strategy reviewed
- [ ] Monitoring stack deployed (Prometheus + Grafana)
- [ ] GitHub Actions configured with KUBE_CONFIG secret

## Implementation Timeline

**Total Time:** 5 weeks (part-time)

- **Week 1:** Infrastructure preparation
- **Week 2:** CI/CD integration
- **Week 3:** Database migration patterns
- **Week 4:** Testing and validation
- **Week 5:** Production rollout

---

## Phase 1: Infrastructure Preparation (Week 1)

### Step 1.1: Create Blue Environment from Current Deployment

```bash
# Navigate to project root
cd /home/user/vidra

# Backup current deployment
kubectl get all -n vidra -o yaml > backup-current-deployment.yaml

# Label current deployment as "blue"
kubectl label deployment vidra-api version=blue -n vidra --overwrite
kubectl label deployment vidra-encoding-worker version=blue -n vidra --overwrite

# Update service selector to use version label
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"blue"}}}'

# Verify
kubectl get service vidra-api -n vidra -o jsonpath='{.spec.selector}' | jq
```

**Expected Output:**

```json
{
  "app": "vidra",
  "component": "api",
  "version": "blue"
}
```

### Step 1.2: Test Blue Environment Stability

```bash
# Run health check
curl http://vidra.example.com/health

# Run readiness check
curl http://vidra.example.com/ready | jq

# Check traffic is flowing to blue pods
kubectl logs -l version=blue -n vidra --tail=10
```

### Step 1.3: Deploy Green Environment (Test)

```bash
# Apply green environment (with current image)
kubectl apply -k k8s/overlays/green/

# Wait for green pods to be ready
kubectl wait --for=condition=ready pod -l version=green --timeout=5m -n vidra

# Verify green environment (without switching traffic)
kubectl run test-green \
  --image=curlimages/curl \
  --restart=Never \
  --rm \
  -i \
  -n vidra \
  -- curl -f http://vidra-api-green/health

# Clean up green environment
kubectl delete -k k8s/overlays/green/
```

**Checkpoint:** Both blue and green environments can coexist without conflict.

---

## Phase 2: CI/CD Integration (Week 2)

### Step 2.1: Configure GitHub Secrets

```bash
# Generate kubeconfig (base64 encoded)
cat ~/.kube/config | base64 > kubeconfig-base64.txt

# Add to GitHub secrets
gh secret set KUBE_CONFIG < kubeconfig-base64.txt

# Add database URL (for migrations)
gh secret set DATABASE_URL --body "postgres://user:pass@host:5432/vidra"

# Optional: Slack webhook
gh secret set SLACK_WEBHOOK_URL --body "https://hooks.slack.com/services/..."
```

### Step 2.2: Test GitHub Actions Workflow

```bash
# Trigger workflow manually (with current version)
gh workflow run blue-green-deploy.yml \
  --field image_tag=v1.0.0 \
  --field skip_staging=true \
  --field canary_percentage=10 \
  --field auto_promote=false

# Monitor workflow
gh run watch

# View logs
gh run view --log
```

### Step 2.3: Set Up Deployment Environments

In GitHub UI:

1. Go to Settings > Environments
2. Create environments:
   - `staging`
   - `production-migration` (with required reviewers)
   - `production-blue` (with required reviewers)
   - `production-green` (with required reviewers)
   - `production-canary` (with required reviewers)
   - `production-full-rollout` (with required reviewers)

3. Configure protection rules:
   - Required reviewers: 1-2 people
   - Wait timer: 0 minutes (manual gates only)

**Checkpoint:** GitHub Actions workflow runs successfully end-to-end.

---

## Phase 3: Database Migration Strategy (Week 3)

### Step 3.1: Document Current Schema

```bash
# Export current schema
pg_dump "$DATABASE_URL" --schema-only > schema-before-bluegreen.sql

# Document tables and indexes
psql "$DATABASE_URL" -c "\dt" > tables-list.txt
psql "$DATABASE_URL" -c "\di" > indexes-list.txt
```

### Step 3.2: Create Expand-Contract Migration Template

Create migration template in `migrations/`:

```sql
-- migrations/064_example_expand_contract.sql
-- +goose Up
-- Expand phase: Add new schema without breaking old code

-- Example: Adding new column
ALTER TABLE videos ADD COLUMN new_column_name TEXT;

-- Backfill data (if needed)
UPDATE videos SET new_column_name = old_column_name WHERE new_column_name IS NULL;

-- Add index (concurrently to avoid locks)
CREATE INDEX CONCURRENTLY idx_videos_new_column ON videos(new_column_name);

-- +goose Down
ALTER TABLE videos DROP COLUMN new_column_name;
```

Next release migration (contract phase):

```sql
-- migrations/065_example_contract.sql
-- +goose Up
-- Contract phase: Remove old schema (after blue fully replaced)

ALTER TABLE videos DROP COLUMN old_column_name;

-- +goose Down
ALTER TABLE videos ADD COLUMN old_column_name TEXT;
```

### Step 3.3: Test Migration Rollback

```bash
# Test migration up
make migrate-up

# Test migration down (rollback)
make migrate-down

# Verify database consistency
psql "$DATABASE_URL" -c "SELECT COUNT(*) FROM videos;"
```

**Checkpoint:** Migration rollback works without data loss.

---

## Phase 4: Testing & Validation (Week 4)

### Step 4.1: Staging Blue/Green Test

```bash
# Deploy to staging with blue/green
gh workflow run blue-green-deploy.yml \
  --field image_tag=v1.1.0-staging \
  --field skip_staging=false \
  --field canary_percentage=50 \
  --field auto_promote=true

# Monitor staging deployment
kubectl get pods -n vidra-staging --watch
```

### Step 4.2: Load Test During Switchover

```bash
# Start load test (k6)
cd tests/load
k6 run --vus 100 --duration 30m video-platform-load-test.js &

# Trigger blue/green deployment while load test runs
gh workflow run blue-green-deploy.yml --field image_tag=v1.1.0-staging

# Monitor error rates during switchover
kubectl logs -f deployment/vidra-api-green -n vidra-staging | grep ERROR
```

### Step 4.3: Test Rollback Procedure

```bash
# Deploy green
kubectl apply -k k8s/overlays/green/

# Switch traffic to green
kubectl patch service vidra-api -n vidra-staging -p '{"spec":{"selector":{"version":"green"}}}'

# Simulate failure scenario
kubectl exec -it deployment/vidra-api-green -n vidra-staging -- killall -9 vidra

# Execute rollback
./scripts/rollback-deployment.sh

# Verify traffic back on blue
curl http://staging.vidra.example.com/health
```

### Step 4.4: Validate Federation During Switchover

```bash
# Set up ActivityPub monitoring
watch -n 5 'curl -s http://staging.vidra.example.com/.well-known/nodeinfo | jq'

# Trigger deployment
gh workflow run blue-green-deploy.yml --field image_tag=v1.1.0-staging

# Verify federation endpoints remain accessible
# - Webfinger
# - Actor endpoints
# - Inbox/Outbox
# - NodeInfo
```

**Checkpoint:** Blue/green deployment works in staging with zero downtime.

---

## Phase 5: Production Rollout (Week 5)

### Step 5.1: Pre-Deployment Preparation

**One week before:**

```bash
# Review production metrics baseline
# - Average request rate
# - p99 latency
# - Error rate
# - Database connections
# - Queue depth

# Document current version
kubectl get deployment vidra-api -n vidra -o jsonpath='{.spec.template.spec.containers[0].image}'

# Backup database
pg_dump "$DATABASE_URL" | gzip > vidra-backup-$(date +%Y%m%d).sql.gz

# Verify monitoring alerts are configured
kubectl get prometheusrules -n monitoring
```

**One day before:**

```bash
# Send notification to team
echo "Blue/green deployment scheduled for $(date)"

# Verify rollback script is tested
./scripts/rollback-deployment.sh --dry-run

# Prepare incident response team
# - On-call engineer
# - Backup engineer
# - Database administrator
```

### Step 5.2: Execute Production Deployment

**Schedule:** Choose low-traffic period (e.g., Tuesday 10am UTC)

```bash
# Start deployment
gh workflow run blue-green-deploy.yml \
  --field image_tag=v1.2.0 \
  --field skip_staging=false \
  --field canary_percentage=10 \
  --field auto_promote=false

# Monitor workflow
gh run watch

# Approve staging gate (after manual review)
gh run view --web  # Click "Review deployments"

# Approve migration gate (after reviewing migration logs)
gh run view --web

# Approve canary gate (after 10% traffic validation)
# Check metrics:
# - Error rate < 0.1%
# - p99 latency < 2s
# - No increase in database errors
gh run view --web

# Approve full rollout (after canary metrics look good)
gh run view --web
```

### Step 5.3: Post-Deployment Validation

**Immediate (0-30 minutes):**

```bash
# Verify traffic is flowing
curl https://vidra.example.com/health

# Check error rates
kubectl logs -l version=green -n vidra --tail=100 | grep ERROR

# Monitor Grafana dashboard
# - HTTP request rate
# - Error rate
# - Latency (p50, p95, p99)
# - Federation delivery success
```

**Short-term (30 minutes - 2 hours):**

```bash
# Scale down blue environment
kubectl scale deployment vidra-api-blue --replicas=1 -n vidra

# Monitor for stability
watch -n 60 'kubectl top pods -n vidra'

# Verify federation is working
curl https://vidra.example.com/.well-known/nodeinfo | jq
```

**Long-term (2-24 hours):**

```bash
# After 2 hours, fully scale down blue
kubectl scale deployment vidra-api-blue --replicas=0 -n vidra
kubectl scale deployment vidra-encoding-worker-blue --replicas=0 -n vidra

# After 24 hours, label as inactive
kubectl label deployment vidra-api-blue status=inactive -n vidra --overwrite

# Schedule contract migration for next release
echo "TODO: Apply contract migration in v1.3.0" >> docs/deployment/migration-checklist.md
```

### Step 5.4: Document Lessons Learned

```bash
# Create post-deployment report
cat > docs/deployment/deployment-$(date +%Y%m%d)-report.md <<EOF
# Deployment Report: v1.2.0

**Date:** $(date)
**Duration:** [Deployment start to full rollout]
**Deployed by:** [Your name]

## Metrics

- **Downtime:** 0 seconds
- **Switchover time:** [minutes]
- **Error rate during deployment:** [percentage]
- **Rollback required:** No

## Issues Encountered

1. [Issue 1]
2. [Issue 2]

## Improvements for Next Time

1. [Improvement 1]
2. [Improvement 2]

## Success Criteria

- [x] Zero downtime achieved
- [x] Federation remained operational
- [x] No encoding jobs interrupted
- [x] Rollback capability verified
EOF
```

**Checkpoint:** Production blue/green deployment successful!

---

## Operational Procedures

### Daily Operations

**Deploying a new version:**

```bash
# 1. Build and push Docker image
docker build -t ghcr.io/yegamble/vidra-core:v1.3.0 .
docker push ghcr.io/yegamble/vidra-core:v1.3.0

# 2. Trigger GitHub Actions workflow
gh workflow run blue-green-deploy.yml \
  --field image_tag=v1.3.0 \
  --field canary_percentage=10

# 3. Monitor and approve gates
gh run watch
```

**Emergency rollback:**

```bash
# Instant rollback script
./scripts/rollback-deployment.sh

# Manual rollback
kubectl patch service vidra-api -n vidra -p '{"spec":{"selector":{"version":"blue"}}}'
```

### Monitoring Checklist

Monitor these metrics during deployment:

- [ ] HTTP request rate (should remain steady)
- [ ] Error rate (should stay < 0.1%)
- [ ] p99 latency (should stay < 2s)
- [ ] Database connection pool utilization (should stay < 80%)
- [ ] Encoding queue depth (should stay < 1000)
- [ ] Federation delivery success rate (should stay > 99%)
- [ ] IPFS gateway health (should stay healthy)
- [ ] ClamAV response time (should stay < 5s)

### Troubleshooting Guide

**Pods stuck in Pending:**

```bash
kubectl describe pod <pod-name> -n vidra
# Check: Resource quotas, PVC availability, node capacity
```

**High error rate after switchover:**

```bash
# Immediate rollback
./scripts/rollback-deployment.sh

# Check logs
kubectl logs -l version=green -n vidra --tail=500 | grep ERROR
```

**Database connection pool exhausted:**

```bash
# Check active connections
psql "$DATABASE_URL" -c "SELECT count(*) FROM pg_stat_activity WHERE state = 'active';"

# Increase connection limits in deployment
kubectl set env deployment/vidra-api-green \
  DATABASE_MAX_CONNECTIONS=50 \
  -n vidra
```

**Encoding jobs stuck:**

```bash
# Check Redis queue
redis-cli -u "$REDIS_URL" LLEN encoding:queue

# Check worker status
kubectl logs -l component=encoding-worker,version=green -n vidra
```

---

## Maintenance & Optimization

### Monthly Review

Review these metrics monthly:

- Deployment frequency (target: multiple per week)
- Average deployment time (target: < 30 minutes)
- Rollback frequency (target: < 5% of deployments)
- Downtime during deployments (target: 0 seconds)
- Cost overhead from blue/green (target: < 0.1% monthly)

### Continuous Improvement

Areas for optimization:

1. **Faster Health Checks**
   - Reduce health check timeout
   - Parallelize validation jobs

2. **Automated Canary Analysis**
   - Integrate Prometheus API for automated metric checks
   - Auto-promote or auto-rollback based on thresholds

3. **Database Migration Automation**
   - Automated backward-compatibility checks
   - Dry-run validation in CI

4. **Cost Reduction**
   - Spot instances for canary phase
   - Faster scale-down of old environment

---

## Success Metrics

After implementation, measure:

- **Deployment Confidence:** Team comfort deploying multiple times per day
- **Downtime:** 0 seconds per deployment
- **Rollback Time:** < 30 seconds
- **User Impact:** Zero user-visible issues during deployments
- **Developer Velocity:** Faster feature releases

---

## Next Steps

After successful implementation:

1. **Automate More:** Reduce manual approval gates for non-breaking changes
2. **Improve Monitoring:** Add custom dashboards for deployment metrics
3. **Document Patterns:** Create runbooks for common scenarios
4. **Train Team:** Ensure all engineers can execute blue/green deployments
5. **Iterate:** Continuously improve based on lessons learned

---

## Support & Resources

- **Strategy Document:** `/home/user/vidra/docs/deployment/BLUE_GREEN_DEPLOYMENT_STRATEGY.md`
- **Kubernetes Manifests:** `/home/user/vidra/k8s/overlays/{blue,green}/`
- **GitHub Actions Workflow:** `/home/user/vidra/.github/workflows/blue-green-deploy.yml`
- **Rollback Script:** `/home/user/vidra/scripts/rollback-deployment.sh`
- **Slack Channel:** #vidra-deployments
- **On-Call:** PagerDuty rotation

---

**Document History:**

- 2025-11-17: Initial implementation guide created
