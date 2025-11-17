# Blue/Green Deployment Quick Start

**For the impatient: Get blue/green deployments running in 30 minutes**

## Prerequisites

- Athena already deployed on Kubernetes
- kubectl configured
- GitHub Actions enabled

## 5-Minute Setup

### 1. Label Current Deployment as "Blue"

```bash
cd /home/user/athena

# Label existing deployment
kubectl label deployment athena-api version=blue -n athena --overwrite
kubectl label deployment athena-encoding-worker version=blue -n athena --overwrite

# Update service to use version selector
kubectl patch service athena-api -n athena -p '{"spec":{"selector":{"version":"blue"}}}'
```

### 2. Configure GitHub Secrets

```bash
# Add kubeconfig
cat ~/.kube/config | base64 | gh secret set KUBE_CONFIG --body-file=-

# Add database URL
gh secret set DATABASE_URL --body "postgres://user:pass@host:5432/athena"
```

### 3. Test Green Deployment

```bash
# Deploy green (without switching traffic)
kubectl apply -k k8s/overlays/green/

# Verify green pods running
kubectl get pods -l version=green -n athena

# Test green health
kubectl run test --image=curlimages/curl --restart=Never --rm -i -n athena \
  -- curl http://athena-api-green/health

# Clean up
kubectl delete -k k8s/overlays/green/
```

## First Production Deployment

### Option A: Automated (GitHub Actions)

```bash
# Trigger deployment
gh workflow run blue-green-deploy.yml \
  --field image_tag=v1.2.0 \
  --field canary_percentage=10

# Monitor
gh run watch

# Approve gates in GitHub UI when ready
```

### Option B: Manual (Direct kubectl)

```bash
# 1. Deploy green
kubectl apply -k k8s/overlays/green/

# 2. Wait for ready
kubectl wait --for=condition=ready pod -l version=green --timeout=5m -n athena

# 3. Run validation
kubectl apply -f k8s/jobs/pre-switch-validation.yaml
kubectl logs job/pre-switch-validation -n athena

# 4. Switch traffic (10% canary)
kubectl apply -f k8s/overlays/green/ingress-canary.yaml
kubectl patch ingress athena-ingress-green-canary -n athena --type=merge \
  -p '{"metadata":{"annotations":{"nginx.ingress.kubernetes.io/canary-weight":"10"}}}'

# 5. Monitor for 10 minutes
# Check Grafana: error rate, latency, etc.

# 6. Full switch
kubectl patch service athena-api -n athena -p '{"spec":{"selector":{"version":"green"}}}'
kubectl delete ingress athena-ingress-green-canary -n athena

# 7. Scale down blue
kubectl scale deployment athena-api-blue --replicas=1 -n athena
```

## Emergency Rollback

```bash
# Instant rollback script
./scripts/rollback-deployment.sh

# Or manual
kubectl patch service athena-api -n athena -p '{"spec":{"selector":{"version":"blue"}}}'
```

## What You Get

- Zero-downtime deployments
- Instant rollback (< 30 seconds)
- Canary testing (gradual rollout)
- Automated validation
- Federation-aware switchover
- Cost-optimized (< 0.1% overhead)

## Next Steps

1. Review [Strategy Document](./BLUE_GREEN_DEPLOYMENT_STRATEGY.md) for architecture details
2. Follow [Implementation Guide](./BLUE_GREEN_IMPLEMENTATION_GUIDE.md) for production hardening
3. Customize validation jobs for your specific needs
4. Set up monitoring dashboards

## Key Files

- **Strategy:** `docs/deployment/BLUE_GREEN_DEPLOYMENT_STRATEGY.md`
- **Implementation:** `docs/deployment/BLUE_GREEN_IMPLEMENTATION_GUIDE.md`
- **Manifests:** `k8s/overlays/{blue,green}/`
- **Workflow:** `.github/workflows/blue-green-deploy.yml`
- **Rollback:** `scripts/rollback-deployment.sh`

## Common Issues

**Pods stuck pending?**
```bash
kubectl describe pod <pod-name> -n athena
# Check PVC, resources, node capacity
```

**Service selector not working?**
```bash
kubectl get endpoints athena-api -n athena
# Should show pods with matching labels
```

**Can't access green directly?**
```bash
# Port-forward for testing
kubectl port-forward svc/athena-api-green 8080:80 -n athena
curl http://localhost:8080/health
```

## Support

- GitHub Issues: https://github.com/yegamble/athena/issues
- Documentation: `docs/deployment/`
- Slack: #athena-deployments
